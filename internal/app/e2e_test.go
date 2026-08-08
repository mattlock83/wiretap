package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/evermock/core"
)

func TestEndToEndCapturesTestToSUTTraffic(t *testing.T) {
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	defer sut.Close()

	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.Mode = "passthrough"
	cfg.Artifact.OutputDir = outputDir
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	control := httptest.NewServer(appInstance.routes())
	defer control.Close()

	start := map[string]any{"proxy": map[string]any{"target_url": sut.URL}}
	startBody, _ := json.Marshal(start)
	resp, err := http.Post(control.URL+"/v1/startServer", "application/json", bytes.NewReader(startBody))
	if err != nil {
		t.Fatalf("start capture: %v", err)
	}
	var runtime struct {
		SUTURL string `json:"sut_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runtime); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	setupBody, _ := json.Marshal(map[string]any{"name": "hello world happy flow", "sut_base_url": sut.URL, "generate_schema": true})
	resp, err = http.Post(control.URL+"/v1/setupTest", "application/json", bytes.NewReader(setupBody))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = http.Get(runtime.SUTURL + "/hello")
	if err != nil {
		t.Fatalf("call captured SUT: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}

	teardownBody, _ := json.Marshal(map[string]any{"session_id": session.SessionID, "status": "pass"})
	resp, err = http.Post(control.URL+"/v1/teardownTest", "application/json", bytes.NewReader(teardownBody))
	if err != nil {
		t.Fatal(err)
	}
	var finished struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&finished); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	artifactPath := finished.Path
	if !filepath.IsAbs(artifactPath) {
		artifactPath = filepath.Join(outputDir, artifactPath)
	}
	payload, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, "hello world happy flow") || !strings.Contains(text, "test_to_sut") || strings.Contains(text, "sut_to_mock") {
		t.Fatalf("unexpected artifact:\n%s", text)
	}
}
