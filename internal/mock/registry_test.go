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
	target := MatchTarget{Path: "/alpha"}

	first, matched := registry.Match(req, nil, target)
	if !matched {
		t.Fatalf("expected first match")
	}
	if first.ID != "override-alpha" {
		t.Fatalf("expected non-default override first, got %q", first.ID)
	}

	second, matched := registry.Match(req, nil, target)
	if !matched {
		t.Fatalf("expected second match")
	}
	if second.ID != "default-alpha" {
		t.Fatalf("expected default fallback second, got %q", second.ID)
	}

	third, matched := registry.Match(req, nil, target)
	if !matched {
		t.Fatalf("expected third match")
	}
	if third.ID != "default-alpha" {
		t.Fatalf("expected default to persist, got %q", third.ID)
	}
}

func TestRegistryMatch_AssetScoping(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Stub{
		ID:       "accounts-b",
		AssetID:  "service-b",
		Default:  true,
		Match:    StubMatch{Method: "GET", Path: "/health"},
		Response: StubResponse{Status: 200, Body: "b"},
	})
	registry.Register(Stub{
		ID:       "accounts-c",
		AssetID:  "service-c",
		Default:  true,
		Match:    StubMatch{Method: "GET", Path: "/health"},
		Response: StubResponse{Status: 200, Body: "c"},
	})

	req := httptest.NewRequest("GET", "http://mock.local/health", nil)

	// Overlapping paths resolve by dependency identity, not registration order.
	got, matched := registry.Match(req, nil, MatchTarget{AssetID: "service-b", Path: "/health"})
	if !matched || got.ID != "accounts-b" {
		t.Fatalf("expected asset-scoped match for service-b, got matched=%v id=%q", matched, got.ID)
	}
	got, matched = registry.Match(req, nil, MatchTarget{AssetID: "service-c", Path: "/health"})
	if !matched || got.ID != "accounts-c" {
		t.Fatalf("expected asset-scoped match for service-c, got matched=%v id=%q", matched, got.ID)
	}

	// A request routed to no dependency matches neither asset-bound stub.
	if _, matched = registry.Match(req, nil, MatchTarget{Path: "/health"}); matched {
		t.Fatalf("expected no match for unrouted request against asset-bound stubs")
	}
}

func TestRegistryMatch_SessionScoping(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Stub{
		ID:        "session-a-stub",
		SessionID: "session-a",
		Default:   true,
		Match:     StubMatch{Method: "GET", Path: "/alpha"},
		Response:  StubResponse{Status: 200, Body: "a"},
	})

	req := httptest.NewRequest("GET", "http://mock.local/alpha", nil)

	if _, matched := registry.Match(req, nil, MatchTarget{SessionID: "session-b", Path: "/alpha"}); matched {
		t.Fatalf("expected session-bound stub not to match another session")
	}
	got, matched := registry.Match(req, nil, MatchTarget{SessionID: "session-a", Path: "/alpha"})
	if !matched || got.ID != "session-a-stub" {
		t.Fatalf("expected session match, got matched=%v id=%q", matched, got.ID)
	}
}
