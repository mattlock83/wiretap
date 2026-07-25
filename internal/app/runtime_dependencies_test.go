package app

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestRuntimeMixedBuiltinAndExternalDependenciesCaptureAndCorrelate(t *testing.T) {
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" || r.URL.RawQuery != "from=sut" {
			t.Errorf("external request URL = %q", r.URL.String())
		}
		w.Header().Set("X-Mock-Provider", "external")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "from-external")
	}))
	defer external.Close()

	var externalDependencyURL atomic.Value
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, _ := externalDependencyURL.Load().(string)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target+"/health?from=sut", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("X-Phrond-Trace-Id", r.Header.Get("X-Phrond-Trace-Id"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer sut.Close()

	appURL := startRuntimeTestServer(t)
	start := postJSONExpect(t, appURL+"/v1/startServer", map[string]any{
		"stack_size": 8,
		"proxy":      map[string]any{"target_url": sut.URL},
	}, http.StatusOK)
	_ = start

	setup := postJSONExpect(t, appURL+"/v1/setupTest", map[string]any{
		"name": "mixed-builtin-external",
		"dependencies": []map[string]any{
			{
				"asset_id": "external-inventory",
				"mock": map[string]any{
					"mode":         "external",
					"upstream_url": external.URL,
				},
			},
			{"asset_id": "builtin-inventory"},
		},
	}, http.StatusOK)
	sessionID := stringValue(t, setup, "session_id")
	dependencyURLs, _ := setup["dependency_urls"].(map[string]any)
	externalURL, _ := dependencyURLs["external-inventory"].(string)
	builtinURL, _ := dependencyURLs["builtin-inventory"].(string)
	externalDependencyURL.Store(externalURL)

	status, rejection := postJSONWithStatus(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"asset_id":   "external-inventory",
	})
	if status != http.StatusConflict || !strings.Contains(stringValue(t, rejection, "error"), "uses an external mock") {
		t.Fatalf("external queue-response rejection = status %d body %v", status, rejection)
	}
	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "builtin-health",
		"asset_id":   "builtin-inventory",
		"match":      map[string]any{"method": "GET", "path": "/health"},
		"response":   map[string]any{"status": 200, "body": "from-builtin"},
	}, http.StatusOK)

	if gotStatus, body := httpGetText(t, builtinURL+"/health"); gotStatus != http.StatusOK || body != "from-builtin" {
		t.Fatalf("built-in dependency = status %d body %q", gotStatus, body)
	}
	sutURL := stringValue(t, setup, "sut_url")
	if gotStatus, body := httpGetText(t, sutURL+"/trigger"); gotStatus != http.StatusAccepted || body != "from-external" {
		t.Fatalf("SUT call = status %d body %q", gotStatus, body)
	}

	teardown := postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{
		"session_id": sessionID,
		"status":     "pass",
	}, http.StatusOK)
	artifactDoc, _ := teardown["artifact"].(map[string]any)
	traffic, _ := artifactDoc["traffic"].([]any)

	var parentID string
	var externalFlow map[string]any
	var builtinFlow map[string]any
	for _, raw := range traffic {
		flow, _ := raw.(map[string]any)
		switch flow["channel"] {
		case "test_to_sut":
			parentID, _ = flow["id"].(string)
		case "sut_to_mock":
			switch flow["asset_id"] {
			case "external-inventory":
				externalFlow = flow
			case "builtin-inventory":
				builtinFlow = flow
			}
		}
	}
	if parentID == "" || externalFlow == nil || builtinFlow == nil {
		t.Fatalf("missing captured flows: parent=%q external=%v builtin=%v", parentID, externalFlow, builtinFlow)
	}
	if externalFlow["mock_mode"] != "external" ||
		externalFlow["parent_flow_id"] != parentID ||
		externalFlow["mock_stub"] != nil ||
		boolValue(externalFlow, "unmatched") {
		t.Fatalf("external flow semantics = %v", externalFlow)
	}
	traceID, _ := externalFlow["trace_id"].(string)
	if traceID == "" {
		t.Fatalf("external flow missing trace correlation: %v", externalFlow)
	}
	if builtinFlow["mock_mode"] != "builtin" || builtinFlow["mock_stub"] == nil {
		t.Fatalf("built-in flow semantics = %v", builtinFlow)
	}
}

func TestRuntimeExternalMockFailureIsFinalizedInArtifact(t *testing.T) {
	unused, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve unused address: %v", err)
	}
	upstreamURL := "http://" + unused.Addr().String()
	unused.Close()

	appURL := startRuntimeTestServer(t)
	postJSONExpect(t, appURL+"/v1/startServer", map[string]any{}, http.StatusOK)
	setup := postJSONExpect(t, appURL+"/v1/setupTest", map[string]any{
		"name": "external-failure",
		"dependencies": []map[string]any{{
			"asset_id": "external-inventory",
			"mock": map[string]any{
				"mode":         "external",
				"upstream_url": upstreamURL,
			},
		}},
	}, http.StatusOK)
	sessionID := stringValue(t, setup, "session_id")
	dependencyURLs, _ := setup["dependency_urls"].(map[string]any)
	dependencyURL, _ := dependencyURLs["external-inventory"].(string)

	if status, body := httpGetText(t, dependencyURL+"/health"); status != http.StatusBadGateway || !strings.HasPrefix(body, "proxy error: ") {
		t.Fatalf("external failure = status %d body %q", status, body)
	}
	teardown := postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{
		"session_id": sessionID,
		"status":     "pass",
	}, http.StatusOK)
	artifactDoc, _ := teardown["artifact"].(map[string]any)
	traffic, _ := artifactDoc["traffic"].([]any)
	if len(traffic) != 1 {
		t.Fatalf("traffic count = %d, want 1", len(traffic))
	}
	flow, _ := traffic[0].(map[string]any)
	response, _ := flow["response"].(map[string]any)
	if flow["mock_mode"] != "external" ||
		flow["transport_error"] == "" ||
		response["status"] != float64(http.StatusBadGateway) ||
		boolValue(flow, "unmatched") {
		t.Fatalf("failed external artifact flow = %v", flow)
	}
}

func TestRuntimeExternalMockConfigurationValidation(t *testing.T) {
	appURL := startRuntimeTestServer(t)
	start := postJSONExpect(t, appURL+"/v1/startServer", map[string]any{}, http.StatusOK)
	mockInfo, _ := start["mock"].(map[string]any)
	mockAddr, _ := mockInfo["listen_addr"].(string)

	tests := []struct {
		name     string
		mock     map[string]any
		contains string
	}{
		{
			name:     "missing upstream",
			mock:     map[string]any{"mode": "external"},
			contains: "upstream_url is required",
		},
		{
			name:     "unsupported scheme",
			mock:     map[string]any{"mode": "external", "upstream_url": "ftp://example.test"},
			contains: "scheme must be http or https",
		},
		{
			name:     "recursive target",
			mock:     map[string]any{"mode": "external", "upstream_url": "http://" + mockAddr},
			contains: "must not point",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := postJSONWithStatus(t, appURL+"/v1/setupTest", map[string]any{
				"name": "invalid-" + tt.name,
				"dependencies": []map[string]any{{
					"asset_id": "inventory",
					"mock":     tt.mock,
				}},
			})
			if status != http.StatusBadRequest || !strings.Contains(stringValue(t, body, "error"), tt.contains) {
				t.Fatalf("validation = status %d body %v", status, body)
			}
		})
	}
}
