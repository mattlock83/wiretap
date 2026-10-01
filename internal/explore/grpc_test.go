package explore

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/hooks"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func grpcExploreSeed(t *testing.T, includeHTTP bool, channel string) (string, *spec) {
	t.Helper()
	_, seed, schema := makeSeed(t, testSpec, nil)
	if !includeHTTP {
		seed.Traffic, seed.Schemas = nil, nil
	}
	role := "sut"
	if channel == "sut_to_dependency" {
		role = "dependency"
	}
	descriptors, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	seed.Schemas = append(seed.Schemas, artifact.SchemaRef{ID: "health", Role: role, AssetID: "health", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(descriptors)}})
	message := func() *artifact.GrpcMessage {
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Encoding: "json", Text: "{}"}, Wire: &artifact.Body{Encoding: "base64"}}}}
	}
	flow := artifact.Flow{ID: "grpc-check", Protocol: "grpc", Channel: channel, AssetID: "health", Request: artifact.Message{GRPC: message()}, Response: artifact.Message{GRPC: message()}}
	flow.Response.GRPC.HTTPStatus = 200
	seed.Traffic = append(seed.Traffic, flow)
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(bundle.Identity{APIID: "orders", SUTTargetKind: "service"}); err != nil {
		t.Fatal(err)
	}
	seed.Schemas, err = w.Register(seed.TestCase, seed.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(seed); err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return w.Path(), schema
}

func TestGRPCProviderExploreRejectsBeforeHooksOrTraffic(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed=%t", mixed), func(t *testing.T) {
			source, _ := grpcExploreSeed(t, mixed, "test_to_sut")
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			defer server.Close()
			marker := filepath.Join(t.TempDir(), "hook-called")
			t.Setenv("WIRETAP_BLOCKED_HOOK_MARKER", marker)
			command := &hooks.Command{Command: []string{os.Args[0], "-test.run=^TestGRPCExploreHookHelper$"}}
			result, err := Run(t.Context(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: Config{Hooks: hooks.Lifecycle{BeforeRun: command, AfterRun: command}}})
			if err == nil || !strings.Contains(err.Error(), "gRPC provider exploration is not supported") {
				t.Fatalf("unexpected exploration result: %+v (%v)", result, err)
			}
			if requests.Load() != 0 || result.Bundle != "" {
				t.Fatalf("unsupported exploration had side effects: requests=%d result=%+v", requests.Load(), result)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("unsupported exploration invoked a hook")
			}
		})
	}
}

func TestGRPCExploreHookHelper(t *testing.T) {
	marker := os.Getenv("WIRETAP_BLOCKED_HOOK_MARKER")
	if marker == "" {
		t.Skip("hook child process only")
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Print("{}")
	os.Exit(0)
}

func TestHTTPProviderWithGRPCDependencyExploreReplaysOnlyHTTP(t *testing.T) {
	source, schema := grpcExploreSeed(t, true, "sut_to_dependency")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/orders" {
			t.Errorf("replayed dependency request: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if schema.validate(request{Method: r.Method, URL: "http://provider" + r.URL.String(), Headers: r.Header, Body: body}) != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid input"}`)
		} else {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"fresh-order"}`)
		}
	}))
	defer server.Close()
	result, err := Run(t.Context(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: Config{MaxCases: 1}})
	if err != nil || result.Passed != 1 || requests.Load() != 2 {
		t.Fatalf("HTTP provider replay: %+v, requests=%d (%v)", result, requests.Load(), err)
	}
	_, captures, err := bundle.Load(result.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		for _, flow := range capture.Traffic {
			if flow.Protocol != "http" || flow.Channel != "test_to_sut" {
				t.Fatalf("explorer claimed replay of a dependency: %+v", flow)
			}
		}
	}
}
