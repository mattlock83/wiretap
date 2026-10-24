package bundle

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
	"github.com/phrond/wiretap/internal/redact"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
)

func richBundleFixture(t *testing.T, rules ...artifact.Redaction) (string, Attempt, Run) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rawContract, _ := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}})
	contract, err := grpccontract.Parse(rawContract, []string{"grpc.health.v1.Health"})
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "rich-error", Name: "expected invalid argument", Status: "pass", StartedAt: time.Now().UTC()}
	schemas, err := w.Register(tc, []artifact.SchemaRef{{ID: "health", Role: "sut", Type: "protobuf", Services: contract.Services, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(rawContract)}}})
	if err != nil {
		t.Fatal(err)
	}
	standard, _ := anypb.New(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "token", Description: "rich-status-secret-f38ad973"}}})
	local, _ := anypb.New(&healthpb.HealthCheckRequest{Service: "local detail"})
	envelope, _ := proto.Marshal(&statuspb.Status{Code: 3, Message: "invalid token", Details: []*anypb.Any{standard, local}})
	request := &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", Messages: []artifact.GrpcPayload{{Index: 0, Wire: &artifact.Body{Encoding: "base64"}, Body: &artifact.Body{Encoding: "json", Text: "{}"}}}}
	response := &artifact.GrpcMessage{Kind: "grpc", Procedure: request.Procedure, Service: request.Service, Method: request.Method, SchemaRef: request.SchemaRef, HTTPStatus: 200, StatusCode: 3, Trailers: artifact.Headers{"Grpc-Status-Details-Bin": {base64.RawStdEncoding.EncodeToString(envelope)}}}
	grpccontract.CaptureStatusDetails(response, contract)
	capture := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: schemas, Traffic: []artifact.Flow{{ID: "flow", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: request}, Response: artifact.Message{GRPC: response}}}}
	if err := redact.Apply(capture, rules); err != nil {
		t.Fatal(err)
	}
	path, err := w.Commit(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	var attempt Attempt
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &attempt); err != nil {
		t.Fatal(err)
	}
	var run Run
	data, _ = os.ReadFile(filepath.Join(w.Path(), "bundle.json"))
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return w.Path(), attempt, run
}

func TestRichStatusRoundTripAndTampering(t *testing.T) {
	root, _, _ := richBundleFixture(t)
	_, captures, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	response := captures[0].Traffic[0].Response.GRPC
	if response.StatusCode != 3 || len(response.Messages) != 0 || response.StatusDetails.Details[0].Type != "google.rpc.BadRequest" || response.StatusDetails.Details[1].DescriptorSource != "boundary" {
		t.Fatalf("bad response: %+v", response)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Attempt, string)
	}{
		{"projection", func(a *Attempt, root string) {
			for i, ref := range a.Bodies {
				if ref.Kind == "status-detail-json" && *ref.StatusDetailIndex == 0 {
					raw := []byte(`{"field_violations":[{"field":"injected"}]}`)
					path := "payloads/" + digest(raw) + ".bin"
					if err := os.WriteFile(filepath.Join(root, path), raw, 0600); err != nil {
						t.Fatal(err)
					}
					a.Bodies[i].Path, a.Bodies[i].SHA256, a.Bodies[i].RetainedBytes = path, digest(raw), len(raw)
				}
			}
		}},
		{"detail index", func(a *Attempt, _ string) { a.Capture.Traffic[0].Response.GRPC.StatusDetails.Details[0].Index = 1 }},
		{"envelope code", func(a *Attempt, _ string) { value := 7; a.Capture.Traffic[0].Response.GRPC.StatusDetails.Code = &value }},
		{"envelope message", func(a *Attempt, _ string) { a.Capture.Traffic[0].Response.GRPC.StatusDetails.Message = "forged" }},
		{"missing body", func(a *Attempt, _ string) {
			for i, r := range a.Bodies {
				if r.Kind == "status-envelope" {
					a.Bodies = append(a.Bodies[:i], a.Bodies[i+1:]...)
					break
				}
			}
		}},
		{"duplicate body", func(a *Attempt, _ string) {
			for _, r := range a.Bodies {
				if r.Kind == "status-envelope" {
					a.Bodies = append(a.Bodies, r)
					break
				}
			}
		}},
		{"descriptor identity", func(a *Attempt, _ string) {
			a.Capture.Traffic[0].Response.GRPC.StatusDetails.Details[0].DescriptorSHA256 = strings.Repeat("0", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, a, run := richBundleFixture(t)
			tc.mutate(&a, root)
			rewriteGRPCAttempt(t, root, a, run)
			if _, _, err := Load(root); err == nil {
				t.Fatal("accepted corrupt status evidence")
			}
		})
	}
}

func TestRichStatusFieldRedactionRemovesContainingCopies(t *testing.T) {
	root, attempt, _ := richBundleFixture(t, artifact.Redaction{Field: "response.status_details[0].body.field_violations[*].description", Rule: "remove"})
	_, captures, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	status := captures[0].Traffic[0].Response.GRPC.StatusDetails
	if status.State != "redacted" || status.Wire != nil || status.Details[0].Wire != nil || status.Details[0].State != "redacted" || status.Details[1].State != "decoded" || status.Details[1].Wire == nil {
		t.Fatalf("incorrect redaction: %+v", status)
	}
	if len(attempt.Bodies) == 0 {
		t.Fatal("missing attachments")
	}
	secret := "rich-status-secret-f38ad973"
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte(secret))) {
			t.Errorf("secret retained in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMalformedRichStatusWithLargeMessageRoundTrips(t *testing.T) {
	for _, values := range [][]string{{"%%"}, {"", ""}} {
		source, _, _ := richBundleFixture(t)
		_, captures, err := Load(source)
		if err != nil {
			t.Fatal(err)
		}
		capture := captures[0]
		raw, err := os.ReadFile(capture.Schemas[0].Source.Path)
		if err != nil {
			t.Fatal(err)
		}
		contract, err := grpccontract.Parse(raw, capture.Schemas[0].Services)
		if err != nil {
			t.Fatal(err)
		}
		response := capture.Traffic[0].Response.GRPC
		response.StatusDetails = nil
		response.StatusMessage = strings.Repeat("x", 40<<10)
		response.Metadata = artifact.Headers{"Grpc-Message": {response.StatusMessage}}
		response.Trailers = artifact.Headers{"Grpc-Status-Details-Bin": values}
		grpccontract.CaptureStatusDetails(response, contract)
		writer, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas[0].Source = artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(raw)}
		capture.Schemas, err = writer.Register(capture.TestCase, capture.Schemas)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Load(writer.Path()); err != nil {
			t.Fatalf("writer emitted unreadable malformed-error evidence: %v", err)
		}
	}
}

func TestRichStatusRejectsOversizedDetailInventoryBeforeAttachmentRead(t *testing.T) {
	status := &artifact.GrpcStatusDetails{Wire: &artifact.Body{Encoding: "base64"}, Details: make([]artifact.GrpcStatusDetail, grpccontract.StatusDetailLimit+1)}
	flow := &artifact.Flow{ID: "oversized", Response: artifact.Message{GRPC: &artifact.GrpcMessage{StatusDetails: status}}}
	ref := BodyRef{FlowID: flow.ID, Phase: "response", Kind: "status-envelope", Representation: "bytes", FileRef: FileRef{Path: "missing", SHA256: strings.Repeat("0", 64)}}
	err := restoreGRPCStatusBody(t.TempDir(), map[string]*artifact.Flow{flow.ID: flow}, map[string]bool{}, ref, os.ReadFile)
	if err == nil || !strings.Contains(err.Error(), "detail inventory exceeds") {
		t.Fatalf("oversized inventory reached attachment processing: %v", err)
	}
}
