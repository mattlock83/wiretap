package mock

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestServerCaptureLimitReturnsCompleteBuiltinResponse(t *testing.T) {
	registry := NewRegistry()
	recorder := NewRecorder()
	server := NewServer(registry, recorder, 5)
	requestBody := []byte("complete-request-body")
	responseBody := "complete-response-body"

	registry.Register(Stub{
		ID:    "large-stub",
		Match: StubMatch{Method: "POST", Path: "/large"},
		Response: StubResponse{
			Status: http.StatusOK,
			Body:   responseBody,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addr, err := server.Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer server.Stop(ctx)

	resp, err := http.Post("http://"+addr+"/large", "application/octet-stream", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != responseBody {
		t.Fatalf("response body = %q", got)
	}

	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("exchange count = %d", len(exchanges))
	}
	exchange := exchanges[0]
	if exchange.Request.Body != string(requestBody[:5]) ||
		exchange.Request.BodyBytes != int64(len(requestBody)) ||
		!exchange.Request.BodyTruncated {
		t.Fatalf("request capture = %#v", exchange.Request)
	}
	if exchange.Response.Body != responseBody[:5] ||
		exchange.Response.BodyBytes != int64(len(responseBody)) ||
		!exchange.Response.BodyTruncated {
		t.Fatalf("response capture = %#v", exchange.Response)
	}
}

func TestFallbackUpstreamCaptureLimitForwardsCompleteBodies(t *testing.T) {
	requestBody := []byte("complete-fallback-request")
	responseBody := []byte("complete-fallback-response")
	received := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(responseBody)
	}))
	defer upstream.Close()

	recorder := NewRecorder()
	server := NewServer(nil, recorder, 7)
	if err := server.SetFallbackUpstream(upstream.URL); err != nil {
		t.Fatalf("set fallback: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addr, err := server.Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer server.Stop(ctx)

	resp, err := http.Post("http://"+addr+"/fallback", "application/octet-stream", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	gotResponse, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || !bytes.Equal(gotResponse, responseBody) {
		t.Fatalf("forwarded response = status %d body %q", resp.StatusCode, gotResponse)
	}
	if gotRequest := <-received; !bytes.Equal(gotRequest, requestBody) {
		t.Fatalf("forwarded request = %q", gotRequest)
	}

	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("exchange count = %d", len(exchanges))
	}
	exchange := exchanges[0]
	if exchange.Request.Body != string(requestBody[:7]) ||
		exchange.Request.BodyBytes != int64(len(requestBody)) ||
		!exchange.Request.BodyTruncated {
		t.Fatalf("request capture = %#v", exchange.Request)
	}
	if exchange.Response.Body != string(responseBody[:7]) ||
		exchange.Response.BodyBytes != int64(len(responseBody)) ||
		!exchange.Response.BodyTruncated {
		t.Fatalf("response capture = %#v", exchange.Response)
	}
}
