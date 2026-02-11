package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/core"
)

func TestEndToEndCaptureAndArtifact(t *testing.T) {
	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()
	cfg.Mock.ListenAddr = "127.0.0.1:0"
	cfg.Artifact.OutputDir = outputDir
	cfg.Proxy.Mode = "passthrough"

	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	srv := httptestServer(t, appInstance.routes())
	defer srv.Close()

	// Start test case (also starts proxy + mock).
	openapiSpec := "openapi: 3.0.0\ninfo:\n  title: Test\n  version: 1.0.0\npaths:\n  /hello:\n    get:\n      responses:\n        \"200\":\n          description: ok\n          content:\n            text/plain:\n              schema:\n                type: string\n"
	startBody, _ := json.Marshal(map[string]any{
		"name": "e2e",
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
			{
				"id":   "mock-schema",
				"role": "fake",
				"type": "openapi",
				"source": map[string]any{
					"type":    "inline",
					"content": openapiSpec,
				},
			},
		},
	})
	resp, err := http.Post(srv.URL+"/v1/testcases/start", "application/json", bytes.NewReader(startBody))
	if err != nil {
		t.Fatalf("start testcase: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected start status: %d", resp.StatusCode)
	}

	// Register stub on mock server.
	stubBody, _ := json.Marshal(map[string]any{
		"id": "stub-1",
		"match": map[string]any{
			"method": "GET",
			"path":   "/hello",
		},
		"response": map[string]any{
			"status": 200,
			"body":   "hi",
		},
	})
	resp, err = http.Post(srv.URL+"/v1/stubs", "application/json", bytes.NewReader(stubBody))
	if err != nil {
		t.Fatalf("register stub: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected stub status: %d", resp.StatusCode)
	}

	// Discover proxy + mock addresses by hitting lifecycle endpoints.
	proxyInfo := fetchJSON(t, srv.URL+"/v1/proxy/start")
	mockInfo := fetchJSON(t, srv.URL+"/v1/mock/start")
	proxyURL, _ := proxyInfo["listen_addr"].(string)
	if proxyURL == "" {
		proxyURL, _ = proxyInfo["ListenAddr"].(string)
	}
	if proxyURL == "" {
		t.Fatalf("expected proxy listen addr")
	}
	mockAddr, _ := mockInfo["addr"].(string)
	if mockAddr == "" {
		t.Fatalf("expected mock addr")
	}

	proxyParsed, err := url.Parse(normalizeURL(proxyURL))
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyParsed),
		},
	}

	resp, err = client.Get("http://" + mockAddr + "/hello")
	if err != nil {
		t.Fatalf("client get: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(bodyBytes) != "hi" {
		t.Fatalf("unexpected response body: %q", string(bodyBytes))
	}

	// Finish and confirm artifact content.
	finishBody, _ := json.Marshal(map[string]any{"status": "pass"})
	resp, err = http.Post(srv.URL+"/v1/testcases/finish", "application/json", bytes.NewReader(finishBody))
	if err != nil {
		t.Fatalf("finish testcase: %v", err)
	}
	defer resp.Body.Close()

	var finishResp struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&finishResp); err != nil {
		t.Fatalf("decode finish response: %v", err)
	}
	if finishResp.Path == "" {
		t.Fatalf("expected artifact path")
	}

	artifactPath := finishResp.Path
	if !filepath.IsAbs(artifactPath) {
		artifactPath = filepath.Join(outputDir, artifactPath)
	}
	payload, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !strings.Contains(string(payload), "sut_to_mock") {
		t.Fatalf("expected sut_to_mock flow")
	}
	if !strings.Contains(string(payload), "test_to_sut") {
		t.Fatalf("expected test_to_sut flow")
	}
}

func httptestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	return server
}

func fetchJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("fetch %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}
