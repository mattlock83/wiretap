package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattlock83/wiretap/internal/core"
)

func TestRunEndpointsStartAndStopProxy(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()

	cfg.Artifact.OutputDir = t.TempDir()
	proxyServer := newProxyServer(cfg.Proxy, nil, cfg.Capture.MaxBodyBytes)
	appInstance, err := New(Deps{Config: cfg, Proxy: proxyServer})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/startRun", "application/json", nil)
	if err != nil {
		t.Fatalf("start request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected start status: %d", resp.StatusCode)
	}

	if proxyServer.ListenAddr() == "" {
		t.Fatalf("expected proxy to start and report listen address")
	}

	resp, err = http.Post(server.URL+"/v1/finishRun", "application/json", nil)
	if err != nil {
		t.Fatalf("stop request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected stop status: %d", resp.StatusCode)
	}
}
