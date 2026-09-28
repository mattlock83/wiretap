package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/phrond/evermock/internal/core"
	"github.com/phrond/evermock/internal/artifact"
)

func TestTestCaseLifecycleWritesArtifact(t *testing.T) {
	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()
	cfg.Artifact.OutputDir = outputDir

	store := NewTestCaseStore(cfg.Artifact.OutputDir)
	proxyComponent, err := NewProxyComponent(cfg.Proxy, nil, store)
	if err != nil {
		t.Fatalf("new proxy component: %v", err)
	}
	appInstance, err := New(Deps{Config: cfg, Proxy: proxyComponent})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	openapiSpec := "openapi: 3.0.0\ninfo:\n  title: Test\n  version: 1.0.0\npaths:\n  /process:\n    post:\n      requestBody:\n        required: true\n        content:\n          application/json:\n            schema:\n              type: object\n      responses:\n        \"200\":\n          description: ok\n          content:\n            application/json:\n              schema:\n                type: object\n"
	startPayload := map[string]any{
		"name": "basic",
		"schemas": []map[string]any{
			{
				"id":   "sut-schema",
				"role": "sut",
				"type": "openapi",
				"source": map[string]any{
					"type":    "inline",
					"content": openapiSpec,
				},
			},
		},
	}
	startBody, _ := json.Marshal(startPayload)
	resp, err := http.Post(server.URL+"/v1/testcases/start", "application/json", bytes.NewReader(startBody))
	if err != nil {
		t.Fatalf("start test case: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected start status: %d", resp.StatusCode)
	}

	redactionPayload := map[string]any{"field": "request.headers.authorization", "rule": "mask"}
	redactionBody, _ := json.Marshal(redactionPayload)
	resp, err = http.Post(server.URL+"/v1/redactions", "application/json", bytes.NewReader(redactionBody))
	if err != nil {
		t.Fatalf("redactions request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected redaction status: %d", resp.StatusCode)
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
		Path     string            `json:"path"`
		Artifact artifact.Artifact `json:"artifact"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&finishResp); err != nil {
		t.Fatalf("decode finish response: %v", err)
	}
	if finishResp.Path == "" {
		t.Fatalf("expected artifact path")
	}
	if len(finishResp.Artifact.Redactions) != 1 {
		t.Fatalf("expected redactions to be recorded")
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
