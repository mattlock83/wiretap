package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phrond/evermock/core"
)

const fakeAccountInlineOpenAPI = "openapi: 3.0.0\ninfo:\n  title: fake\n  version: 1\npaths:\n  /alpha:\n    get:\n      responses:\n        \"200\":\n          description: ok\n          content:\n            text/plain:\n              schema:\n                type: string\n"

func TestRuntimeDefaultResponseFallbackAndPersistence(t *testing.T) {
	appURL := startRuntimeTestServer(t)

	startPayload := map[string]any{"stack_size": 4}
	postJSONExpect(t, appURL+"/v1/startServer", startPayload, http.StatusOK)

	setup := postJSONExpect(t, appURL+"/v1/setupTest", setupRuntimePayload("default-fallback"), http.StatusOK)
	sessionID := stringValue(t, setup, "session_id")
	mockBaseURL := stringValue(t, setup, "mock_base_url")

	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "default-alpha",
		"default":    true,
		"match": map[string]any{
			"method": "GET",
			"path":   "/alpha",
		},
		"response": map[string]any{
			"status": 200,
			"body":   "default",
		},
	}, http.StatusOK)
	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "override-alpha",
		"match": map[string]any{
			"method": "GET",
			"path":   "/alpha",
		},
		"response": map[string]any{
			"status": 200,
			"body":   "override",
		},
	}, http.StatusOK)

	status, body := httpGetText(t, mockBaseURL+"/alpha")
	if status != http.StatusOK || body != "override" {
		t.Fatalf("first call expected override 200, got status=%d body=%q", status, body)
	}
	status, body = httpGetText(t, mockBaseURL+"/alpha")
	if status != http.StatusOK || body != "default" {
		t.Fatalf("second call expected default 200, got status=%d body=%q", status, body)
	}
	status, body = httpGetText(t, mockBaseURL+"/alpha")
	if status != http.StatusOK || body != "default" {
		t.Fatalf("third call expected default 200, got status=%d body=%q", status, body)
	}

	queued := getJSONExpect(t, appURL+"/v1/listQueued?session_id="+sessionID, http.StatusOK)
	queuedItems := sliceValue(t, queued, "items")
	if len(queuedItems) != 1 {
		t.Fatalf("expected one queued item after matches, got %d", len(queuedItems))
	}
	queuedDefault := mapItemByID(t, queuedItems, "default-alpha")
	if boolValue(queuedDefault, "default") != true {
		t.Fatalf("expected queued default item to remain in queue")
	}

	responded := getJSONExpect(t, appURL+"/v1/listResponded?session_id="+sessionID, http.StatusOK)
	respondedItems := sliceValue(t, responded, "items")
	if len(respondedItems) != 2 {
		t.Fatalf("expected two responded items, got %d", len(respondedItems))
	}
	defaultResponded := mapItemByID(t, respondedItems, "default-alpha")
	if boolValue(defaultResponded, "default") != true {
		t.Fatalf("expected default responded entry")
	}
	if intValue(defaultResponded, "responded_count") < 2 {
		t.Fatalf("expected default responded count >= 2, got %d", intValue(defaultResponded, "responded_count"))
	}

	inspected := getJSONExpect(t, appURL+"/v1/getResponse?session_id="+sessionID+"&id=default-alpha", http.StatusOK)
	if stringValue(t, inspected, "state") != "responded" {
		t.Fatalf("expected getResponse state responded after default was matched")
	}
	if boolValue(inspected, "default") != true {
		t.Fatalf("expected getResponse default=true")
	}
	if intValue(inspected, "responded_count") < 2 {
		t.Fatalf("expected getResponse responded_count >= 2, got %d", intValue(inspected, "responded_count"))
	}

	postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{
		"session_id": sessionID,
		"status":     "pass",
	}, http.StatusOK)
}

func TestRuntimeDefaultResponseCountsTowardsStackAfterMatch(t *testing.T) {
	appURL := startRuntimeTestServer(t)

	startPayload := map[string]any{"stack_size": 1}
	postJSONExpect(t, appURL+"/v1/startServer", startPayload, http.StatusOK)

	setup := postJSONExpect(t, appURL+"/v1/setupTest", setupRuntimePayload("default-stack"), http.StatusOK)
	sessionID := stringValue(t, setup, "session_id")
	mockBaseURL := stringValue(t, setup, "mock_base_url")

	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "default-alpha",
		"default":    true,
		"match": map[string]any{
			"method": "GET",
			"path":   "/alpha",
		},
		"response": map[string]any{
			"status": 200,
			"body":   "default",
		},
	}, http.StatusOK)

	status, body := httpGetText(t, mockBaseURL+"/alpha")
	if status != http.StatusOK || body != "default" {
		t.Fatalf("expected default response 200 after first call, got status=%d body=%q", status, body)
	}

	status, resp := postJSONWithStatus(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "overflow-alpha",
		"match": map[string]any{
			"method": "GET",
			"path":   "/alpha",
		},
		"response": map[string]any{
			"status": 200,
			"body":   "overflow",
		},
	})
	if status != http.StatusConflict {
		t.Fatalf("expected stack conflict after default match, got status=%d", status)
	}
	if !strings.Contains(stringValue(t, resp, "error"), "stack is full") {
		t.Fatalf("expected stack full error, got %v", resp["error"])
	}

	postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{
		"session_id": sessionID,
		"status":     "pass",
	}, http.StatusOK)
}

func TestRuntimeRecordTrafficFalseSkipsArtifactAndSchemaRequirement(t *testing.T) {
	appURL := startRuntimeTestServer(t)

	postJSONExpect(t, appURL+"/v1/startServer", map[string]any{}, http.StatusOK)

	setup := postJSONExpect(t, appURL+"/v1/setupTest", map[string]any{
		"name":           "performance-session",
		"record_traffic": false,
	}, http.StatusOK)
	sessionID := stringValue(t, setup, "session_id")
	mockBaseURL := stringValue(t, setup, "mock_base_url")
	if boolValue(setup, "record_traffic") {
		t.Fatalf("expected record_traffic=false in setup response")
	}

	postJSONExpect(t, appURL+"/v1/queue-response", map[string]any{
		"session_id": sessionID,
		"id":         "default-alpha",
		"default":    true,
		"match": map[string]any{
			"method": "GET",
			"path":   "/alpha",
		},
		"response": map[string]any{
			"status": 200,
			"body":   "default",
		},
	}, http.StatusOK)

	status, body := httpGetText(t, mockBaseURL+"/alpha")
	if status != http.StatusOK || body != "default" {
		t.Fatalf("expected default response 200, got status=%d body=%q", status, body)
	}

	responded := getJSONExpect(t, appURL+"/v1/listResponded?session_id="+sessionID, http.StatusOK)
	if items := sliceValue(t, responded, "items"); len(items) != 0 {
		t.Fatalf("expected no recorded responses, got %d", len(items))
	}

	teardown := postJSONExpect(t, appURL+"/v1/teardownTest", map[string]any{
		"session_id": sessionID,
		"status":     "pass",
	}, http.StatusOK)
	if boolValue(teardown, "artifact_recorded") {
		t.Fatalf("expected artifact_recorded=false")
	}
	if _, exists := teardown["artifact"]; exists {
		t.Fatalf("expected artifact to be omitted when record_traffic=false")
	}
	if _, exists := teardown["schema_validation"]; exists {
		t.Fatalf("expected schema_validation to be omitted when record_traffic=false")
	}
}

func startRuntimeTestServer(t *testing.T) string {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()
	cfg.Mock.ListenAddr = "127.0.0.1:0"
	cfg.Artifact.OutputDir = t.TempDir()

	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	server := httptest.NewServer(appInstance.routes())
	t.Cleanup(server.Close)
	return server.URL
}

func setupRuntimePayload(name string) map[string]any {
	return map[string]any{
		"name": name,
		"fake_schemas": []map[string]any{
			{
				"id":   "fake-openapi",
				"type": "openapi",
				"source": map[string]any{
					"type":    "inline",
					"content": fakeAccountInlineOpenAPI,
				},
			},
		},
	}
}

func postJSONExpect(t *testing.T, url string, payload any, expectedStatus int) map[string]any {
	t.Helper()
	status, body := postJSONWithStatus(t, url, payload)
	if status != expectedStatus {
		t.Fatalf("unexpected status %d for %s: %v", status, url, body)
	}
	return body
}

func postJSONWithStatus(t *testing.T, url string, payload any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)

	var body map[string]any
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("decode response for %s: %v (body=%q)", url, err, string(bodyBytes))
		}
	} else {
		body = map[string]any{}
	}
	return resp.StatusCode, body
}

func getJSONExpect(t *testing.T, url string, expectedStatus int) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d for %s: %s", resp.StatusCode, url, string(body))
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("decode response for %s: %v (body=%q)", url, err, string(bodyBytes))
		}
	}
	return body
}

func httpGetText(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func stringValue(t *testing.T, body map[string]any, key string) string {
	t.Helper()
	value, ok := body[key].(string)
	if !ok {
		t.Fatalf("expected string at key %q, got %T", key, body[key])
	}
	return value
}

func boolValue(body map[string]any, key string) bool {
	value, _ := body[key].(bool)
	return value
}

func intValue(body map[string]any, key string) int {
	value, ok := body[key].(float64)
	if !ok {
		return 0
	}
	return int(value)
}

func sliceValue(t *testing.T, body map[string]any, key string) []any {
	t.Helper()
	items, ok := body[key].([]any)
	if !ok {
		t.Fatalf("expected slice at key %q, got %T", key, body[key])
	}
	return items
}

func mapItemByID(t *testing.T, items []any, id string) map[string]any {
	t.Helper()
	for _, raw := range items {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if entryID, _ := entry["id"].(string); entryID == id {
			return entry
		}
	}
	t.Fatalf("expected item with id %q", id)
	return nil
}
