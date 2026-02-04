package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/phrond/evermock/core"
)

func TestTestCaseLifecycleWritesArtifact(t *testing.T) {
	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()
	cfg.Mock.ListenAddr = "127.0.0.1:0"
	cfg.Artifact.OutputDir = outputDir

	store := NewTestCaseStore(cfg.Artifact.OutputDir)
	proxyComponent, err := NewProxyComponent(cfg.Proxy, nil, store)
	if err != nil {
		t.Fatalf("new proxy component: %v", err)
	}
	mockComponent := NewMockComponent(cfg.Mock)

	appInstance, err := New(Deps{Config: cfg, Proxy: proxyComponent, Mock: mockComponent})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	startPayload := map[string]any{"name": "basic"}
	startBody, _ := json.Marshal(startPayload)
	resp, err := http.Post(server.URL+"/v1/testcases/start", "application/json", bytes.NewReader(startBody))
	if err != nil {
		t.Fatalf("start test case: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected start status: %d", resp.StatusCode)
	}

	finishPayload := map[string]any{"status": "pass"}
	finishBody, _ := json.Marshal(finishPayload)
	resp, err = http.Post(server.URL+"/v1/testcases/finish", "application/json", bytes.NewReader(finishBody))
	if err != nil {
		t.Fatalf("finish test case: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected finish status: %d", resp.StatusCode)
	}

	var finishResp struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&finishResp); err != nil {
		t.Fatalf("decode finish response: %v", err)
	}
	if finishResp.Path == "" {
		t.Fatalf("expected artifact path")
	}

	if !filepath.IsAbs(finishResp.Path) {
		finishResp.Path = filepath.Join(outputDir, finishResp.Path)
	}
	if _, err := os.Stat(finishResp.Path); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}

	artifactResp, err := http.Get(server.URL + "/v1/artifact")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	artifactResp.Body.Close()
	if artifactResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected artifact status: %d", artifactResp.StatusCode)
	}
}
