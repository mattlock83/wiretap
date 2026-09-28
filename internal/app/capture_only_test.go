package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/phrond/evermock/internal/core"
)

func TestCaptureAPIRejectsUnknownFields(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	for _, path := range []string{"/v1/startRun", "/v1/report"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader([]byte(`{"unexpected":true}`)))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}
