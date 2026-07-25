package mock

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExternalMockGatewayPreservesHTTPExchange(t *testing.T) {
	requestBody := []byte{0x00, 0x01, 'p', 'h', 'r', 'o', 'n', 'd'}
	responseBody := []byte{0xff, 0x00, 'o', 'k'}
	var upstreamHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.EscapedPath() != "/wiremock/items/a%2Fb" {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		if r.URL.RawQuery != "tag=a&tag=b&empty=" {
			t.Errorf("raw query = %q", r.URL.RawQuery)
		}
		if r.Host != upstreamHost {
			t.Errorf("host = %q, want %q", r.Host, upstreamHost)
		}
		if values := r.Header.Values("X-Multi"); len(values) != 2 || values[0] != "one" || values[1] != "two" {
			t.Errorf("X-Multi = %#v", values)
		}
		if r.Header.Get("X-Phrond-Trace-Id") != "phtr-123" {
			t.Errorf("trace header = %q", r.Header.Get("X-Phrond-Trace-Id"))
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, requestBody) {
			t.Errorf("request body = %v", body)
		}

		w.Header().Add("X-Upstream", "one")
		w.Header().Add("X-Upstream", "two")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Trailer", "X-Upstream-Trailer")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write(responseBody)
		w.Header().Set("X-Upstream-Trailer", "finished")
	}))
	defer upstream.Close()
	upstreamHost = strings.TrimPrefix(upstream.URL, "http://")

	server, addr := startMockServer(t)
	server.SetDefaultSession("session-a")
	if err := server.SetExternalUpstream("session-a", "inventory", upstream.URL+"/wiremock", false); err != nil {
		t.Fatalf("set external upstream: %v", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		"http://"+addr+"/__phrond/dep/inventory/items/a%2Fb?tag=a&tag=b&empty=",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Add("X-Multi", "one")
	req.Header.Add("X-Multi", "two")
	req.Header.Set("X-Phrond-Trace-Id", "phtr-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("gateway request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMultiStatus)
	}
	if !bytes.Equal(body, responseBody) {
		t.Fatalf("response body = %v", body)
	}
	if values := resp.Header.Values("X-Upstream"); len(values) != 2 {
		t.Fatalf("X-Upstream = %#v", values)
	}
	if resp.Trailer.Get("X-Upstream-Trailer") != "finished" {
		t.Fatalf("response trailer = %#v", resp.Trailer)
	}

	exchanges := server.Recorder().Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(exchanges))
	}
	exchange := exchanges[0]
	if exchange.SessionID != "session-a" ||
		exchange.AssetID != "inventory" ||
		exchange.TraceID != "phtr-123" ||
		exchange.MockMode != "external" {
		t.Fatalf("exchange attribution = %#v", exchange)
	}
	if exchange.Matched || exchange.MatchedStub != nil || exchange.MatchedStubID != "" {
		t.Fatalf("external exchange fabricated a stub match: %#v", exchange)
	}
	if exchange.Request.URL != "/items/a%2Fb?tag=a&tag=b&empty=" {
		t.Fatalf("recorded URL = %q", exchange.Request.URL)
	}
	if exchange.Response.Status != http.StatusMultiStatus ||
		exchange.Response.Trailers["X-Upstream-Trailer"][0] != "finished" ||
		!bytes.Equal([]byte(exchange.Response.Body), responseBody) {
		t.Fatalf("recorded response = %#v", exchange.Response)
	}
}

func TestExternalMockGatewayRecordsFailureAndReturns502(t *testing.T) {
	server, addr := startMockServer(t)
	server.SetDefaultSession("session-a")

	unused, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve unused address: %v", err)
	}
	unreachable := "http://" + unused.Addr().String()
	unused.Close()

	if err := server.SetExternalUpstream("session-a", "inventory", unreachable, false); err != nil {
		t.Fatalf("set external upstream: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/__phrond/dep/inventory/health")
	if err != nil {
		t.Fatalf("gateway request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.HasPrefix(string(body), "proxy error: ") {
		t.Fatalf("body = %q", body)
	}

	exchanges := server.Recorder().Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(exchanges))
	}
	if exchanges[0].Error == "" ||
		exchanges[0].Response.Status != http.StatusBadGateway ||
		exchanges[0].MockMode != "external" {
		t.Fatalf("failed exchange = %#v", exchanges[0])
	}
}

func TestExternalMockGatewayRecordsTLSFailureAndReturns502(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "should not be reached")
	}))
	defer upstream.Close()

	server, addr := startMockServer(t)
	server.SetDefaultSession("session-a")
	if err := server.SetExternalUpstream("session-a", "secure-inventory", upstream.URL, false); err != nil {
		t.Fatalf("set TLS upstream: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/__phrond/dep/secure-inventory/health")
	if err != nil {
		t.Fatalf("gateway request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.HasPrefix(string(body), "proxy error: ") {
		t.Fatalf("TLS failure response = status %d body %q", resp.StatusCode, body)
	}

	exchanges := server.Recorder().Exchanges()
	if len(exchanges) != 1 || exchanges[0].Error == "" || exchanges[0].MockMode != "external" {
		t.Fatalf("TLS failure exchange = %#v", exchanges)
	}
}

func TestExternalMockGatewayRoutesAreSessionIsolated(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "from-a")
	}))
	defer upstreamA.Close()
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "from-b")
	}))
	defer upstreamB.Close()

	server, addr := startMockServer(t)
	if err := server.SetExternalUpstream("session-a", "inventory", upstreamA.URL, false); err != nil {
		t.Fatalf("set session-a upstream: %v", err)
	}
	if err := server.SetExternalUpstream("session-b", "inventory", upstreamB.URL, false); err != nil {
		t.Fatalf("set session-b upstream: %v", err)
	}
	if err := server.SetExternalUpstream("session-a", "pricing", upstreamB.URL, false); err != nil {
		t.Fatalf("set session-a pricing upstream: %v", err)
	}

	assertHTTPBody(t, "http://"+addr+"/__phrond/session/session-a/dep/inventory/health", "from-a")
	assertHTTPBody(t, "http://"+addr+"/__phrond/session/session-a/dep/pricing/health", "from-b")
	assertHTTPBody(t, "http://"+addr+"/__phrond/session/session-b/dep/inventory/health", "from-b")

	exchanges := server.Recorder().Exchanges()
	if len(exchanges) != 3 ||
		exchanges[0].SessionID != "session-a" || exchanges[0].AssetID != "inventory" ||
		exchanges[1].SessionID != "session-a" || exchanges[1].AssetID != "pricing" ||
		exchanges[2].SessionID != "session-b" || exchanges[2].AssetID != "inventory" {
		t.Fatalf("session/asset attribution = %#v", exchanges)
	}

	server.RemoveExternalUpstreams("session-a")
	resp, err := http.Get("http://" + addr + "/__phrond/session/session-a/dep/inventory/health")
	if err != nil {
		t.Fatalf("request after route removal: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status after route removal = %d, want 501", resp.StatusCode)
	}
}

func TestExternalMockGatewayPreservesHostAndDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != "wiremock.test" {
			t.Errorf("preserved host = %q, want wiremock.test", r.Host)
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer upstream.Close()

	server, addr := startMockServer(t)
	server.SetDefaultSession("session-a")
	if err := server.SetExternalUpstream("session-a", "inventory", upstream.URL, true); err != nil {
		t.Fatalf("set external upstream: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/__phrond/dep/inventory/start", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "wiremock.test"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("gateway request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/final" {
		t.Fatalf("redirect response = status %d headers %#v", resp.StatusCode, resp.Header)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hit count = %d, want 1", hits.Load())
	}
}

func TestExternalMockGatewayRejectsRecursiveTarget(t *testing.T) {
	server, addr := startMockServer(t)
	err := server.SetExternalUpstream("session-a", "inventory", "http://"+addr, false)
	if err == nil || !strings.Contains(err.Error(), "must not point") {
		t.Fatalf("recursive target error = %v", err)
	}
}

func startMockServer(t *testing.T) (*Server, string) {
	t.Helper()
	server := NewServer(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	addr, err := server.Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Stop(context.Background())
	})
	return server, addr
}

func assertHTTPBody(t *testing.T, target, want string) {
	t.Helper()
	resp, err := http.Get(target)
	if err != nil {
		t.Fatalf("get %s: %v", target, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != want {
		t.Fatalf("get %s = status %d body %q", target, resp.StatusCode, body)
	}
}
