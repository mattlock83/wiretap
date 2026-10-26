package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/project"
)

const negativeRequestSpec = `openapi: 3.0.3
info: {title: Orders, version: '1'}
paths:
  /orders:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [quantity], properties: {quantity: {type: integer, minimum: 1}}}
      responses:
        '200': {description: accepted}
        '400':
          description: invalid input
          content:
            application/json:
              schema: {type: object, required: [error], properties: {error: {type: string, enum: [invalid quantity]}}}
  /health:
    get:
      responses:
        '200': {description: healthy}
`

func TestNegativeRequestPreservesPassingOutcomeAndResponseCoverage(t *testing.T) {
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"quantity":0}` {
			t.Errorf("unexpected live request: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid quantity"}`))
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "rejects invalid quantity", "sut_base_url": sut.URL,
		"schemas": []artifact.SchemaRef{{ID: "orders", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: negativeRequestSpec}}},
	})
	response, err := http.Post(session["sut_url"].(string)+"/orders", "application/json", strings.NewReader(`{"quantity":0}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("test expected documented rejection, got %s", response.Status)
	}
	result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
	capture := dependencyArtifact(t, result)
	if capture.TestCase.Status != "pass" || capture.SchemaValidation == nil || capture.SchemaValidation.Status != "fail" {
		t.Fatalf("framework outcome and schema evidence were conflated: %#v", capture)
	}
	if len(capture.SchemaValidation.Errors) != 1 || capture.SchemaValidation.Errors[0].Phase != "request" {
		t.Fatalf("request violation evidence was lost: %#v", capture.SchemaValidation)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	report, err := coverage.Generate(t.Context(), finished["bundle_path"].(string), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Contracts == nil || len(report.Contracts.Specs) != 1 {
		t.Fatalf("missing contract coverage: %#v", report.Contracts)
	}
	spec := report.Contracts.Specs[0]
	responseCovered := false
	for _, target := range spec.Targets {
		if target.Phase == "request" && target.State == "covered" {
			t.Fatalf("invalid request earned valid-request coverage: %#v", target)
		}
		if target.Phase == "response" && target.Family == "response_status" && target.Condition == "400" {
			responseCovered = target.State == "covered"
		}
	}
	if !responseCovered || len(spec.Violations) == 0 {
		t.Fatalf("passing negative test lost documented response coverage or its violation: %#v", spec)
	}
}

func TestInvalidRequestDoesNotHideInvalidResponse(t *testing.T) {
	capture := &artifact.Artifact{
		TestCase: artifact.TestCase{Status: "pass"},
		Schemas:  []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: negativeRequestSpec}}},
		Traffic: []artifact.Flow{{ID: "exchange", Protocol: "http", Channel: "test_to_sut",
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://orders/orders", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"quantity":0}`}}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 400, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"error":"unexpected rejection"}`}}},
		}},
	}
	applySchemaValidation(capture)
	if capture.TestCase.Status != "pass" || len(capture.SchemaValidation.Errors) != 2 {
		t.Fatalf("both validation phases must remain visible independently of the framework outcome: %#v", capture)
	}
	if capture.SchemaValidation.Errors[0].Phase != "request" || capture.SchemaValidation.Errors[1].Phase != "response" {
		t.Fatalf("missing independent validation results: %#v", capture.SchemaValidation)
	}
}

func TestEmptyHTTPRunFreezesConfiguredInventoryAtStart(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	schemaPath := filepath.Join(t.TempDir(), "orders.yaml")
	if err := os.WriteFile(schemaPath, []byte(negativeRequestSpec), 0600); err != nil {
		t.Fatal(err)
	}
	instance.project = &project.Config{
		SUT:          project.API{ID: "orders", URL: "http://127.0.0.1:1", Schema: schemaPath},
		Dependencies: map[string]project.API{"inventory": {URL: "http://127.0.0.1:2", Schema: schemaPath, Kind: "mock", Name: "Inventory"}},
	}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	if err := os.Remove(schemaPath); err != nil {
		t.Fatal(err)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	root := start["bundle_path"].(string)
	run, attempts, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "complete" || len(run.Schemas) != 2 || len(attempts) != 0 {
		t.Fatalf("empty run lost its declared inventory: %#v", run)
	}
	report, err := coverage.Generate(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []coverage.SpecCoverageGroup{report.Provided, report.Dependent} {
		if len(group.Specs) != 1 || !group.Specs[0].DenominatorKnown || len(group.Specs[0].NotCovered) != 2 || len(group.Specs[0].Exercised) != 0 {
			t.Fatalf("empty run must report every configured operation uncovered: %#v", group)
		}
	}
	if report.Contracts == nil || len(report.Contracts.Specs) != 2 {
		t.Fatalf("empty run lost its contract condition inventory: %#v", report.Contracts)
	}
	for _, spec := range report.Contracts.Specs {
		for _, target := range spec.Targets {
			if target.State != "not_covered" && target.State != "excluded" || len(target.Evidence) != 0 {
				t.Fatalf("empty run assigned evidence to an untested target: %#v", target)
			}
		}
	}
}

func TestHTTPConfiguredSchemaInventoryDoesNotDuplicateOnFirstAttempt(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	schemaPath := filepath.Join(t.TempDir(), "orders.yaml")
	if err := os.WriteFile(schemaPath, []byte(negativeRequestSpec), 0600); err != nil {
		t.Fatal(err)
	}
	instance.project = &project.Config{SUT: project.API{ID: "orders", URL: "http://127.0.0.1:1", Schema: schemaPath}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "no traffic"})
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
	finished := postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	run, _, err := bundle.Load(finished["bundle_path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Schemas) != 1 {
		t.Fatalf("first attempt duplicated the configured schema: %#v", run.Schemas)
	}
}
