package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
)

func TestDependencyAssertionsRecordSelectedResponseFieldsAndFlowLinks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available":true,"items":[{"sku":"widget"}],"warehouse":"melbourne"}`))
	}))
	defer upstream.Close()

	control, _ := dependencyCaptureRuntime(t)
	postDependencyJSON(t, control.URL, "/v1/startServer", map[string]any{})
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "asserts only consumed inventory response fields", "generate_schema": true,
		"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: upstream.URL, Schema: assertionDependencySchema()}},
	})
	response, err := http.Get(session["dependencies"].(map[string]any)["inventory"].(string) + "/items")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	result := postDependencyJSON(t, control.URL, "/v1/assertDependency", map[string]any{
		"session_id": session["session_id"], "asset_id": "inventory",
		"method": "get", "path": "/items",
		"response": map[string]any{"$.available": true, "$.items[0].sku": "widget"},
	})
	if result["status"] != "pass" || result["flow_id"] != "flow-1" {
		t.Fatalf("unexpected assertion result: %#v", result)
	}

	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{
		"session_id": session["session_id"], "status": "pass",
	}))
	if art.TestCase.Status != "pass" || len(art.Assertions) != 2 {
		t.Fatalf("successful dependency assertions were not persisted: %#v", art)
	}
	if got := art.Traffic[0].AssertionIDs; len(got) != 2 || got[0] != "assertion-1" || got[1] != "assertion-2" {
		t.Fatalf("assertions were not linked to their dependency flow: %#v", got)
	}
	if art.Assertions[0].Field != "$.available" || art.Assertions[1].Field != "$.items[0].sku" {
		t.Fatalf("assertions were not recorded deterministically: %#v", art.Assertions)
	}
	for _, assertion := range art.Assertions {
		if assertion.FlowID != "flow-1" || assertion.Channel != "sut_to_dependency" ||
			assertion.Phase != "response" || assertion.PathLang != "jsonpath" ||
			assertion.Operator != "equals" || assertion.Result != "pass" {
			t.Fatalf("assertion metadata was incomplete: %#v", assertion)
		}
	}
	if !strings.Contains(art.Traffic[0].Response.HTTP.Body.Text, `"warehouse":"melbourne"`) {
		t.Fatalf("field assertions removed full captured response evidence: %#v", art.Traffic[0])
	}
}

func TestFailedDependencyAssertionFailsTheOwningArtifact(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available":false}`))
	}))
	defer upstream.Close()

	control, _ := dependencyCaptureRuntime(t)
	postDependencyJSON(t, control.URL, "/v1/startServer", map[string]any{})
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "fails an incorrect consumed inventory response", "generate_schema": true,
		"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: upstream.URL, Schema: assertionDependencySchema()}},
	})
	response, err := http.Get(session["dependencies"].(map[string]any)["inventory"].(string) + "/items")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	payload, _ := json.Marshal(map[string]any{
		"session_id": session["session_id"], "asset_id": "inventory",
		"response": map[string]any{"$.available": true},
	})
	response, err = http.Post(control.URL+"/v1/assertDependency", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "expected true, got false") {
		t.Fatalf("failed assertion status = %d, body = %s", response.StatusCode, body)
	}

	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{
		"session_id": session["session_id"], "status": "pass",
	}))
	if art.TestCase.Status != "fail" || len(art.Assertions) != 1 || art.Assertions[0].Result != "fail" {
		t.Fatalf("failed assertion did not fail the owning test: %#v", art)
	}
	if len(art.Traffic[0].AssertionIDs) != 1 || art.Traffic[0].AssertionIDs[0] != art.Assertions[0].ID {
		t.Fatalf("failed assertion lost its dependency flow link: %#v", art.Traffic[0])
	}
}

func TestDependencyAssertionRejectsInvalidResponsePaths(t *testing.T) {
	for _, path := range []string{"available", "$", "$.", "$..available", "$.items[-1]", "$.items[x]", "$.items[0"} {
		if _, err := parseAssertionPath(path); err == nil {
			t.Errorf("parseAssertionPath(%q) accepted an invalid JSONPath", path)
		}
	}
}

func assertionDependencySchema() *artifact.SchemaRef {
	return &artifact.SchemaRef{ID: "inventory-api", Type: "openapi", Source: artifact.SchemaSource{
		Type: "inline",
		Content: `openapi: 3.0.3
info: {title: Inventory, version: 1.0.0}
paths:
  /items:
    get:
      responses:
        '200':
          description: inventory
          content:
            application/json:
              schema:
                type: object
                required: [available]
                properties:
                  available: {type: boolean}
                  warehouse: {type: string}
                  items:
                    type: array
                    items:
                      type: object
                      properties:
                        sku: {type: string}
`,
	}}
}
