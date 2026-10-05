package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/bundlereport"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestGRPCAPIOnlyBoundariesAreFrozenAndAvailableBeforeSetup(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	upstream := health.NewServer()
	upstream.SetServingStatus("provider", healthpb.HealthCheckResponse_SERVING)
	origin := healthServer(t, upstream)
	boundary := map[string]any{"id": "api-only", "protocol": "grpc", "url": origin, "services": []string{healthService}, "kind": "service"}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{"boundaries": map[string]any{"sut": boundary, "dependencies": map[string]any{"sut": boundary}}})
	path := start["bundle_path"].(string)
	run, _, err := bundle.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Contracts) != 2 || run.Contracts[0].State != "pending" {
		t.Fatalf("missing unresolved scope: %+v", run.Contracts)
	}
	// A dependency named sut must never replace the provider's descriptor or listener.
	if start["dependencies"].(map[string]any)["sut"] == start["sut_url"] {
		t.Fatal("shared dependency/provider listener")
	}
	for _, payload := range []map[string]any{
		{"boundaries": map[string]any{"sut": boundary}},
		{"proxy": map[string]any{"target_url": "http://127.0.0.1:1"}},
	} {
		body, _ := json.Marshal(payload)
		res, err := http.Post(control.URL+"/v1/startRun", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("mid-run mutation: %d", res.StatusCode)
		}
	}
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "API only"})
	healthCall(t, setup["sut_url"].(string), "provider")
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	run, captures, err := bundle.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "complete" || len(captures) != 1 {
		t.Fatal("API-only run incomplete")
	}
	for _, state := range run.Contracts {
		if state.State != "resolved" {
			t.Fatalf("unresolved state: %+v", state)
		}
	}
	if captures[0].Traffic[0].Request.GRPC.SchemaRef != "api-only" {
		t.Fatal("wrong provider schema reference")
	}
	if _, err := bundlereport.Refresh(path); err != nil {
		t.Fatal(err)
	}
}

func TestGRPCFailedAcquisitionPersistsScopeAndCanRetry(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "fixture.proto")
	origin := healthServer(t, health.NewServer())
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{"boundaries": map[string]any{"sut": map[string]any{"id": "retry", "protocol": "grpc", "url": origin, "services": []string{"fixture.Example"}, "schema": map[string]any{"source": "proto", "files": []string{"fixture.proto"}, "import_paths": []string{dir}}}}})
	path := start["bundle_path"].(string)
	for _, endpoint := range []string{"/v1/setupTest", "/v1/finishRun"} {
		res, err := http.Post(control.URL+endpoint, "application/json", bytes.NewBufferString(`{"name":"not admitted"}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode < 400 {
			t.Fatalf("%s ignored missing contract", endpoint)
		}
	}
	run, captures, err := bundle.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "open" || len(captures) > 0 || len(run.Pending) > 0 || run.Contracts[0].State != "failed" {
		t.Fatalf("invalid failed snapshot: %+v", run)
	}
	knowledge, err := os.ReadFile(filepath.Join(path, "docs/knowledge.json"))
	if err != nil || !strings.Contains(string(knowledge), `"contract_resolution": "failed"`) {
		t.Fatalf("failed discovery was not published in knowledge: %v", err)
	}
	if err := os.WriteFile(protoPath, []byte(`syntax="proto3";package fixture;message Empty{} service Example{rpc Call(Empty)returns(Empty);}`), 0600); err != nil {
		t.Fatal(err)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	run, _, err = bundle.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "complete" || run.Contracts[0].State != "resolved" {
		t.Fatalf("retry failed: %+v", run)
	}
	if err := os.Remove(protoPath); err != nil {
		t.Fatal(err)
	}
	if _, err := bundlereport.Refresh(path); err != nil {
		t.Fatalf("offline rebuild used source files: %v", err)
	}
}

func TestGRPCDiscoveryExtendsControlWriteDeadline(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	origin := healthServer(t, health.NewServer())
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{"boundaries": map[string]any{"sut": map[string]any{"id": "slow", "protocol": "grpc", "url": origin, "services": []string{healthService}}}})
	short := httptest.NewUnstartedServer(instance.routes())
	short.Config.WriteTimeout = time.Nanosecond
	short.Start()
	defer short.Close()
	setup := postDependencyJSON(t, short.URL, "/v1/setupTest", map[string]any{"name": "discovery deadline"})
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	postDependencyJSON(t, short.URL, "/v1/finishRun", map[string]any{})
}
