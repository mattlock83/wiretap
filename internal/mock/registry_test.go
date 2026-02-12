package mock

import (
	"net/http/httptest"
	"testing"
)

func TestRegistryMatch_DefaultIsFallbackAndPersistent(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Stub{
		ID:      "default-alpha",
		Default: true,
		Match: StubMatch{
			Method: "GET",
			Path:   "/alpha",
		},
		Response: StubResponse{Status: 200, Body: "default"},
	})
	registry.Register(Stub{
		ID: "override-alpha",
		Match: StubMatch{
			Method: "GET",
			Path:   "/alpha",
		},
		Response: StubResponse{Status: 200, Body: "override"},
	})

	req := httptest.NewRequest("GET", "http://mock.local/alpha", nil)

	first, matched := registry.Match(req, nil)
	if !matched {
		t.Fatalf("expected first match")
	}
	if first.ID != "override-alpha" {
		t.Fatalf("expected non-default override first, got %q", first.ID)
	}

	second, matched := registry.Match(req, nil)
	if !matched {
		t.Fatalf("expected second match")
	}
	if second.ID != "default-alpha" {
		t.Fatalf("expected default fallback second, got %q", second.ID)
	}

	third, matched := registry.Match(req, nil)
	if !matched {
		t.Fatalf("expected third match")
	}
	if third.ID != "default-alpha" {
		t.Fatalf("expected default to persist, got %q", third.ID)
	}
}
