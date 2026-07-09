package app

import (
	"net/http"
	"strings"
	"testing"
)

// Two dependencies with overlapping paths must resolve by asset identity, and
// zero-config sessions must attribute unlabeled traffic without any session
// prefix in the URL.
func TestRuntimeDependencyRoutingZeroConfig(t *testing.T) {
	appURL := startRuntimeTestServer(t)

	postJSONExpect(t, appURL+"/v1/startServer", map[string]any{"stack_size": 8}, http.StatusOK)

	setup := postJSONExpect(t, appURL+"/v1/setupTest", map[string]any{
		"name": "dependency-routing",
		"dependencies": []map[string]any{
			{"asset_id": "service-b", "asset_name": "Service B"},
			{"asset_id": "service-c", "asset_name": "Service C"},
		},
	}, http.StatusOK)
	sessionID := stringValue(t, setup, "session_id")
	mockBaseURL := stringValue(t, setup, "mock_base_url")
	if strings.Contains(mockBaseURL, "/__phrond/session/") {
		t.Fatalf("expected zero-config mock URL without session prefix, got %q", mockBaseURL)
	}
	depURLs, ok := setup["dependency_urls"].(map[string]any)
	if !ok || len(depURLs) != 2 {
		t.Fatalf("expected two dependency urls, got %v", setup["dependency_urls"])
	}
	urlB, _ := depURLs["service-b"].(string)
	urlC, _ := depURLs["service-c"].(string)
	if !strings.HasSuffix(urlB, "/__phrond/dep/service-b") || !strings.HasSuffix(urlC, "/__phrond/dep/service-c") {
		t.Fatalf("unexpected dependency urls: %q %q", urlB, urlC)
	}

	// Same method+path on both dependencies — only asset identity can
	// disambiguate them.
	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "health-b",
		"asset_id":   "service-b",
		"default":    true,
		"match":      map[string]any{"method": "GET", "path": "/health"},
		"response":   map[string]any{"status": 200, "body": "from-b"},
	}, http.StatusOK)
	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "health-c",
		"asset_id":   "service-c",
		"default":    true,
		"match":      map[string]any{"method": "GET", "path": "/health"},
		"response":   map[string]any{"status": 200, "body": "from-c"},
	}, http.StatusOK)

	if status, body := httpGetText(t, urlB+"/health"); status != http.StatusOK || body != "from-b" {
		t.Fatalf("expected service-b response, got status=%d body=%q", status, body)
	}
	if status, body := httpGetText(t, urlC+"/health"); status != http.StatusOK || body != "from-c" {
		t.Fatalf("expected service-c response, got status=%d body=%q", status, body)
	}

	// Record consumed fields for one response.
	postJSONExpect(t, appURL+"/v1/verify", map[string]any{
		"session_id": sessionID,
		"id":         "health-b",
		"fields":     []string{"body.status"},
	}, http.StatusOK)

	teardown := postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{
		"session_id": sessionID,
		"status":     "pass",
	}, http.StatusOK)

	artifactDoc, ok := teardown["artifact"].(map[string]any)
	if !ok {
		t.Fatalf("expected artifact in teardown response")
	}
	traffic, _ := artifactDoc["traffic"].([]any)
	assetSeen := map[string]string{}
	consumedSeen := false
	for _, raw := range traffic {
		flow, _ := raw.(map[string]any)
		if flow == nil {
			continue
		}
		if assetID, _ := flow["asset_id"].(string); assetID != "" {
			stubID, _ := flow["matched_stub_id"].(string)
			assetSeen[assetID] = stubID
		}
		if fields, ok := flow["consumed_fields"].([]any); ok && len(fields) == 1 {
			consumedSeen = true
		}
		if fp, _ := flow["fingerprint"].(string); fp == "" {
			t.Fatalf("expected fingerprint on flow %v", flow["id"])
		}
	}
	if assetSeen["service-b"] != "health-b" || assetSeen["service-c"] != "health-c" {
		t.Fatalf("expected asset attribution on flows, got %v", assetSeen)
	}
	if !consumedSeen {
		t.Fatalf("expected consumed_fields on the verified flow")
	}

	// Schema validation is skipped (not failed) without schemas.
	validation, _ := artifactDoc["schema_validation"].(map[string]any)
	if validation == nil || validation["status"] != "skipped" {
		t.Fatalf("expected schema validation skipped, got %v", validation)
	}
}

// A second zero-config session must be rejected while one is active; explicit
// sessions may coexist.
func TestRuntimeZeroConfigSessionGuard(t *testing.T) {
	appURL := startRuntimeTestServer(t)
	postJSONExpect(t, appURL+"/v1/startServer", map[string]any{}, http.StatusOK)

	first := postJSONExpect(t, appURL+"/v1/setupTest", map[string]any{"name": "first"}, http.StatusOK)
	firstID := stringValue(t, first, "session_id")

	status, resp := postJSONWithStatus(t, appURL+"/v1/setupTest", map[string]any{"name": "second"})
	if status != http.StatusConflict {
		t.Fatalf("expected conflict for second zero-config session, got %d (%v)", status, resp)
	}

	explicit := postJSONExpect(t, appURL+"/v1/setupTest", map[string]any{
		"name":             "parallel",
		"explicit_session": true,
	}, http.StatusOK)
	explicitMock := stringValue(t, explicit, "mock_base_url")
	if !strings.Contains(explicitMock, "/__phrond/session/") {
		t.Fatalf("expected explicit session URL to carry session prefix, got %q", explicitMock)
	}

	postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{"session_id": firstID, "status": "pass"}, http.StatusOK)
	postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{"session_id": stringValue(t, explicit, "session_id"), "status": "pass"}, http.StatusOK)
}
