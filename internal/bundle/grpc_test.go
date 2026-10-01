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
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func grpcBundleFixture(t *testing.T) (string, Attempt, Run) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(grpc_health_v1.File_grpc_health_v1_health_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "empty protobuf request", StartedAt: time.Now().UTC(), Status: "pass"}
	frozen, err := w.Register(tc, []artifact.SchemaRef{{ID: "health", AssetID: "health", Role: "sut", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(raw)}}})
	if err != nil {
		t.Fatal(err)
	}
	request := &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Encoding: "json", Text: `{}`}, Wire: &artifact.Body{Encoding: "base64"}}}}
	wire, _ := proto.Marshal(&grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING})
	response := &artifact.GrpcMessage{Kind: "grpc", Procedure: request.Procedure, Service: request.Service, Method: request.Method, SchemaRef: request.SchemaRef, HTTPStatus: 200, Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Encoding: "json", Text: `{"status":"SERVING"}`}, Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire)}}}}
	path, err := w.Commit(&artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen, Traffic: []artifact.Flow{{ID: "flow-1", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: request}, Response: artifact.Message{GRPC: response}}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var attempt Attempt
	if err := json.Unmarshal(data, &attempt); err != nil {
		t.Fatal(err)
	}
	runData, err := os.ReadFile(filepath.Join(w.Path(), "evidence/run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run Run
	if err := json.Unmarshal(runData, &run); err != nil {
		t.Fatal(err)
	}
	return w.Path(), attempt, run
}

func rewriteGRPCAttempt(t *testing.T, root string, attempt Attempt, run Run) {
	t.Helper()
	raw, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, run.Attempts[0].Path), raw, 0600); err != nil {
		t.Fatal(err)
	}
	run.Attempts[0].SHA256 = digest(raw)
	raw, err = json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "evidence/run.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGRPCEmptyWireMessageHasVerifiedAttachment(t *testing.T) {
	root, attempt, _ := grpcBundleFixture(t)
	if len(attempt.Bodies) != 4 {
		t.Fatalf("expected wire and projection for both directions, got %d", len(attempt.Bodies))
	}
	found := false
	for _, body := range attempt.Bodies {
		if body.Kind == "wire" && body.Phase == "request" {
			found = body.SHA256 == digest(nil) && body.RetainedBytes == 0
		}
	}
	if !found {
		t.Fatal("empty protobuf message has no hashed attachment")
	}
	_, captures, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if captures[0].Traffic[0].Request.GRPC.Messages[0].Wire == nil {
		t.Fatal("empty message lost wire representation")
	}
}

func TestGRPCBundleRejectsInconsistentEvidence(t *testing.T) {
	cases := map[string]struct {
		change func(*Attempt)
		want   string
	}{
		"wrong protocol":  {func(a *Attempt) { a.Capture.Traffic[0].Protocol = "http" }, "protocol disagrees"},
		"old evidence":    {func(a *Attempt) { a.EvidenceVersion = "1.0" }, "requires evidence version 2"},
		"unordered index": {func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.Messages[0].Index = 1 }, "unordered"},
		"wrong schema":    {func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.SchemaRef = "missing" }, "unavailable boundary schema"},
		"redacted wire":   {func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.Messages[0].Redacted = true }, "retains redacted wire"},
		"missing empty attachment": {func(a *Attempt) {
			for i, b := range a.Bodies {
				if b.Kind == "wire" && b.Phase == "request" {
					a.Bodies = append(a.Bodies[:i], a.Bodies[i+1:]...)
					return
				}
			}
		}, "without attachment"},
		"wrong projection": {func(a *Attempt) {
			for i, b := range a.Bodies {
				if b.Kind == "" && b.Phase == "response" {
					a.Bodies[i].FileRef = a.Bodies[0].FileRef
					a.Bodies[i].RetainedBytes = a.Bodies[0].RetainedBytes
					return
				}
			}
		}, "projection disagrees"},
		"duplicate body": {func(a *Attempt) { a.Bodies = append(a.Bodies, a.Bodies[0]) }, "duplicate body reference"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root, a, run := grpcBundleFixture(t)
			tc.change(&a)
			rewriteGRPCAttempt(t, root, a, run)
			if _, _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

func TestGRPCContractResolutionIntegrity(t *testing.T) {
	for _, state := range []string{"pending", "failed", "resolved"} {
		t.Run(state, func(t *testing.T) {
			root, a, run := grpcBundleFixture(t)
			run.Contracts = []ContractResolution{{APIID: "health", Role: "sut", Protocol: "grpc", Source: "reflection", State: state, Services: []string{"grpc.health.v1.Health"}}}
			rewriteGRPCAttempt(t, root, a, run)
			if _, _, err := Load(root); err != nil {
				t.Fatal(err)
			}
			if state == "resolved" {
				run.Contracts[0].Services = []string{"other.Service"}
			} else {
				run.RunState = "complete"
			}
			rewriteGRPCAttempt(t, root, a, run)
			if _, _, err := Load(root); err == nil {
				t.Fatal("inconsistent contract resolution accepted")
			}
		})
	}
}
