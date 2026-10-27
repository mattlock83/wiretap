package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCapabilitiesDoNotStartRunOrWaitForControlLock(t *testing.T) {
	a := &App{}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil))
	var result struct {
		APIVersion   string         `json:"api_version"`
		Capabilities []string       `json:"capabilities"`
		Producer     map[string]any `json:"producer"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	validateControlResponse(t, "capabilities", document)
	wantCapabilities := []string{"capture.http", "outputs.coverage", "outputs.arazzo", "test.identity", "run.finalize"}
	if w.Code != 200 || result.APIVersion != "1.0" || !slices.Equal(result.Capabilities, wantCapabilities) || result.Producer["name"] != "wiretap" || a.currentBundle() != nil {
		t.Fatalf("invalid capabilities: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/capabilities", nil))
	if w.Code != 405 {
		t.Fatalf("POST status %d", w.Code)
	}
}

func validateControlResponse(t *testing.T, definition string, document any) {
	t.Helper()
	data, err := os.ReadFile("../../docs/control-api.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var source any
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("control-api.schema.json", source); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("control-api.schema.json#/$defs/" + definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("actual %s response violates public schema: %v", definition, err)
	}
}
