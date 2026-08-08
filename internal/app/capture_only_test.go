package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/phrond/evermock/core"
)

func TestMockRoutesAreRemoved(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	for _, path := range []string{"/v1/queue-response", "/v1/verify", "/v1/stubs", "/v1/mock/start"} {
		resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader([]byte("{}")))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestCaptureAPIRejectsRemovedMockConfiguration(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/startServer", "application/json", bytes.NewReader([]byte(`{"mock":{"listen_addr":"127.0.0.1:0"}}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
