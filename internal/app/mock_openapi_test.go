package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestMockCapturePreservesContractEnforcement(t *testing.T) {
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
			response, err := http.Get(session["sut_url"].(string) + "/__wiretap/dep/inventory/items")
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
				t.Fatalf("capture changed enforcement: %s / %s", capture.TestCase.Status, capture.SchemaValidation.Status)
			}
			if len(capture.Traffic) != 1 || capture.Traffic[0].Response.HTTP == nil || capture.Traffic[0].Response.HTTP.Body == nil || capture.Traffic[0].Response.HTTP.Body.Text != `{"value":42}` {
				t.Fatalf("archive lost actual mock response: %+v", capture.Traffic)
			}

			postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
		})
	}
}
