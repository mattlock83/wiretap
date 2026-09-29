package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
	"gopkg.in/yaml.v3"
)

func TestMockDraftGenerationNeverReplacesContractEnforcement(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		name := "omitted spec"
		if supplied {
			name = "supplied spec"
		}
		t.Run(name, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"value":42}`))
			}))
			defer mock.Close()
			control, _ := dependencyCaptureRuntime(t)
			dependency := dependencyRegistration{AssetID: "inventory", UpstreamKind: "mock", UpstreamURL: mock.URL, SchemaRequired: new(bool)}
			if supplied {
				dependency.Schema = &artifact.SchemaRef{ID: "inventory", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.0
info: {title: Inventory, version: "1"}
paths:
  /items:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [value]
                properties:
                  value: {type: string}
`}}
			}
			session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": name, "schema_required": false, "dependencies": []dependencyRegistration{dependency}})
			response, err := http.Get(session["sut_url"].(string) + "/__phrond/dep/inventory/items")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
			capture := dependencyArtifact(t, result)
			wantStatus, wantValidation := "pass", "skipped"
			if supplied {
				wantStatus, wantValidation = "fail", "fail"
			}
			if capture.TestCase.Status != wantStatus || capture.SchemaValidation.Status != wantValidation {
				t.Fatalf("draft changed enforcement: %s / %s", capture.TestCase.Status, capture.SchemaValidation.Status)
			}
			root := result["bundle_path"].(string)
			paths, err := filepath.Glob(filepath.Join(root, "dependencies/mocks/*.openapi.yaml"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("missing automatic mock draft: %v %v", paths, err)
			}
			raw, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			// The actual numeric mock shape is documented even when its supplied
			// string contract rejected it; it is never promoted to a validation spec.
			if !strings.Contains(string(raw), "type: integer") || !strings.Contains(string(raw), "used_for_validation: false") {
				t.Fatalf("draft did not describe actual mock evidence: %s", raw)
			}
			postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
		})
	}
}
