package mock

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServerRecordsExchange(t *testing.T) {
	registry := NewRegistry()
	recorder := NewRecorder()
	server := NewServer(registry, recorder)

	registry.Register(Stub{
		ID: "stub-1",
		Match: StubMatch{
			Method: "POST",
			Path:   "/echo",
		},
		Response: StubResponse{
			Status: 200,
			Body:   "ok",
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	addr, err := server.Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = server.Stop(ctx)
	}()

	req, err := http.NewRequest("POST", "http://"+addr+"/echo", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if string(bodyBytes) != "ok" {
		t.Fatalf("unexpected response body: %q", string(bodyBytes))
	}

	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(exchanges))
	}
	exchange := exchanges[0]
	if !exchange.Matched || exchange.MatchedStubID != "stub-1" {
		t.Fatalf("expected matched stub-1, got matched=%v id=%q", exchange.Matched, exchange.MatchedStubID)
	}
	if exchange.Request.Body != "hello" {
		t.Fatalf("unexpected recorded request body: %q", exchange.Request.Body)
	}
}
