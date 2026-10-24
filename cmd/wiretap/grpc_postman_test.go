package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/postman"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func postmanGRPCBundleFixture(t *testing.T, mixed, incomplete bool) string {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(bundle.Identity{Application: &bundle.Application{ID: "health-app", Name: "Health"}}); err != nil {
		t.Fatal(err)
	}
	descriptor, _ := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}})
	tc := artifact.TestCase{ID: "attempt", Name: "health check", Status: "pass", StartedAt: time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)}
	schemas, err := w.Register(tc, []artifact.SchemaRef{{ID: "health", Role: "sut", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(descriptor)}}})
	if err != nil {
		t.Fatal(err)
	}
	message := func(payload proto.Message, seq uint64) *artifact.GrpcMessage {
		wire, _ := proto.Marshal(payload)
		projection, _ := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(payload)
		observed := int64(len(wire) + 5)
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", StreamKind: "unary", ObservedBytes: observed, Observation: &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, ObservedBytes: observed, CountsComplete: true, EOF: true, End: "eof"}, Messages: []artifact.GrpcPayload{{Index: 0, DecodeState: "decoded", Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire), OriginalBytes: int64(len(wire))}, Body: &artifact.Body{Encoding: "json", Text: string(projection)}, Observation: &artifact.GrpcPayloadObservation{WireBytes: uint64(len(wire)), StartSequence: seq, CompleteSequence: seq + 1, StartedOffsetNS: int64(seq), CompletedOffsetNS: int64(seq + 1)}}}}
	}
	req := message(&healthpb.HealthCheckRequest{}, 0)
	res := message(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, 2)
	res.HTTPStatus, res.StatusSource = 200, "trailers"
	res.Trailers = artifact.Headers{"Grpc-Status": {"0"}}
	res.CaptureTruncated = incomplete
	flows := []artifact.Flow{{ID: "grpc-flow", Protocol: "grpc", Channel: "test_to_sut", StartedAt: tc.StartedAt, Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: res}}}
	if mixed {
		flows = append(flows, artifact.Flow{ID: "http-flow", Protocol: "http", Channel: "test_to_sut", StartedAt: tc.StartedAt, Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://health.test/status"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}})
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 1, TestCase: tc, Schemas: schemas, Traffic: flows}); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}

func TestCLIPostmanGRPCExports(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "grpc only", true: "mixed"}[mixed], func(t *testing.T) {
			root := postmanGRPCBundleFixture(t, mixed, false)
			dir := t.TempDir()
			if code := execute(t.Context(), []string{"export", "--bundle", root, "--format", "postman", "--output-dir", dir}); code != 0 {
				t.Fatalf("export = %d", code)
			}
			entries, err := os.ReadDir(dir)
			wantCount := 1
			if mixed {
				wantCount = 2
			}
			if err != nil || len(entries) != wantCount {
				t.Fatalf("exports = %v, %v", entries, err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "sut-health-app.grpc.postman_collection.json"))
			if err != nil {
				t.Fatal(err)
			}
			var collection postman.GRPCCollection
			if err := json.Unmarshal(raw, &collection); err != nil {
				t.Fatal(err)
			}
			if collection.Kind != "collection" || len(collection.Items) != 1 || collection.Items[0].Items[0].Examples[0].Response.Code != 0 {
				t.Fatalf("invalid gRPC export: %s", raw)
			}
			code, stdout := executeCapturingStdout(t, []string{"export", "--bundle", root, "--format", "postman"})
			if mixed {
				if code != 2 || len(stdout) != 0 {
					t.Fatalf("mixed stdout = %d, %s", code, stdout)
				}
			} else if code != 0 || string(stdout) != string(raw) {
				t.Fatalf("grpc stdout = %d, %s", code, stdout)
			}
			custom := filepath.Join(t.TempDir(), "custom.json")
			if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "postman", "--output", custom}); code != 0 {
				t.Fatalf("custom export = %d", code)
			}
			primary, err := os.ReadFile(custom)
			if err != nil {
				t.Fatal(err)
			}
			if mixed != strings.Contains(string(primary), "v2.1.0") {
				t.Fatalf("wrong primary collection: %s", primary)
			}
		})
	}
}

func TestCLIPostmanIncompleteGRPCPreservesOutputs(t *testing.T) {
	root := postmanGRPCBundleFixture(t, true, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "sut-health-app.postman_collection.json")
	if err := os.WriteFile(path, []byte("keep existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := execute(t.Context(), []string{"export", "--bundle", root, "--format", "postman", "--output-dir", dir}); code != 1 {
		t.Fatalf("incomplete export = %d", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "keep existing" {
		t.Fatalf("failed export modified existing collection: %s, %v", raw, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed export created partial outputs: %v, %v", entries, err)
	}
}

func TestCLIPostmanOmissionsAreExplicitAndKeepHealthyHTTP(t *testing.T) {
	root := postmanGRPCBundleFixture(t, true, true)
	directory := t.TempDir()
	if code := execute(t.Context(), []string{"export", "--bundle", root, "--format", "postman", "--postman-omit-incomplete", "--output-dir", directory}); code != 0 {
		t.Fatalf("opt-in omission = %d", code)
	}
	diagnostic, err := os.ReadFile(filepath.Join(directory, "wiretap-postman-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var omissions []postman.Diagnostic
	if err := json.Unmarshal(diagnostic, &omissions); err != nil || len(omissions) != 1 || omissions[0].FlowID != "grpc-flow" {
		t.Fatalf("missing omission provenance: %s, %v", diagnostic, err)
	}
	collection, err := os.ReadFile(filepath.Join(directory, "sut-health-app.postman_collection.json"))
	if err != nil || !strings.Contains(string(collection), "http://health.test/status") {
		t.Fatalf("healthy HTTP lost: %s %v", collection, err)
	}
}

func TestCLIPostmanCopiesConfiguredGRPCSource(t *testing.T) {
	root := postmanGRPCBundleFixture(t, false, false)
	configDir := t.TempDir()
	source := `syntax = "proto3";
package grpc.health.v1;
message HealthCheckRequest {string service=1;}
message HealthCheckResponse {enum ServingStatus {UNKNOWN=0; SERVING=1; NOT_SERVING=2; SERVICE_UNKNOWN=3;} ServingStatus status=1;}
service Health {rpc Check(HealthCheckRequest) returns (HealthCheckResponse);}
`
	if err := os.WriteFile(filepath.Join(configDir, "health.proto"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configDir, "postman.json")
	if err := os.WriteFile(config, []byte(`{"grpc":{"sut":{"url":"https://localhost:50051","proto_root":".","proto_file":"health.proto"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if code := execute(t.Context(), []string{"export", "--bundle", root, "--format", "postman", "--postman-config", config, "--output-dir", directory}); code != 0 {
		t.Fatalf("configured gRPC export = %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "sut-health-app.grpc.postman_collection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var collection postman.GRPCCollection
	if err := json.Unmarshal(raw, &collection); err != nil {
		t.Fatal(err)
	}
	request := collection.Items[0].Items[0]
	if request.URL != "localhost:50051" || request.Schema == nil || request.Settings == nil || !request.Settings.SecureConnection {
		t.Fatalf("mapping not applied: %+v", request)
	}
	copied, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(request.Schema.Location)))
	if err != nil || string(copied) != source {
		t.Fatalf("source schema not preserved: %s %v", copied, err)
	}
	if code, _ := executeCapturingStdout(t, []string{"export", "--bundle", root, "--format", "postman", "--postman-config", config}); code != 2 {
		t.Fatalf("stdout cannot write companions, got %d", code)
	}
	if code := execute(t.Context(), []string{"report", "--bundle", root, "--format", "json", "--postman-config", config}); code != 2 {
		t.Fatalf("Postman flag accepted for JSON, got %d", code)
	}
}
