package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/phrond/evermock/core"
)

func TestLifecycleEndpointsStartComponents(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()

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

	resp, err := http.Post(server.URL+"/v1/lifecycle/start", "application/json", nil)
	if err != nil {
		t.Fatalf("start request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected start status: %d", resp.StatusCode)
	}

	if proxyComponent.Info().ListenAddr == "" {
		t.Fatalf("expected proxy to start and report listen address")
	}

	resp, err = http.Post(server.URL+"/v1/lifecycle/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("stop request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected stop status: %d", resp.StatusCode)
	}
}
