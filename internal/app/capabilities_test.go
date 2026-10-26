package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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
	if w.Code != 200 || result.APIVersion != "1.0" || len(result.Capabilities) != 7 || result.Producer["name"] != "wiretap" || a.currentBundle() != nil {
		t.Fatalf("invalid capabilities: %s", w.Body.String())
	}
	data, err := os.ReadFile("../../sdk/conformance/scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Probe struct {
			Version      string   `json:"api_version"`
			Capabilities []string `json:"capabilities"`
		} `json:"capability_probe"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if result.APIVersion != contract.Probe.Version || !reflect.DeepEqual(result.Capabilities, contract.Probe.Capabilities) {
		t.Fatalf("SDK capability contract drifted: %+v", result)
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
