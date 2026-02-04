package mock

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRegistryMatch(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Stub{
		ID: "first",
		Match: StubMatch{
			Method: "POST",
			Path:   "/widgets",
			Headers: map[string]string{
				"X-Env": "test",
			},
			Query: map[string]string{
				"mode": "fast",
			},
			Body: `{"ok":true}`,
		},
		Response: StubResponse{
			Status: 201,
			Body:   "created",
		},
	})

	req := &http.Request{
		Method: "POST",
		URL: &url.URL{
			Path:     "/widgets",
			RawQuery: "mode=fast",
		},
		Header: http.Header{
			"X-Env": []string{"test"},
		},
		Body: io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	bodyBytes, _ := io.ReadAll(req.Body)

	stub, ok := registry.Match(req, bodyBytes)
	if !ok {
		t.Fatalf("expected match")
	}
	if stub.ID != "first" {
		t.Fatalf("expected stub first, got %q", stub.ID)
	}
}

func TestRegistryOrder(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Stub{ID: "a", Match: StubMatch{Path: "/order"}})
	registry.Register(Stub{ID: "b", Match: StubMatch{Path: "/order"}})

	req := &http.Request{
		Method: "GET",
		URL: &url.URL{
			Path: "/order",
		},
		Header: http.Header{},
	}

	stub, ok := registry.Match(req, nil)
	if !ok {
		t.Fatalf("expected match")
	}
	if stub.ID != "a" {
		t.Fatalf("expected first stub to match, got %q", stub.ID)
	}
}
