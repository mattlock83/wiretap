package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/core"
)

func TestRuntimeReportRefreshesBundle(t *testing.T) {
	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Artifact.OutputDir = outputDir

	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	openapi := `openapi: 3.0.0
info:
  title: Example
  version: "1"
paths:
  /hello:
    get:
      responses:
        "200":
          description: ok
`
	capture := &artifact.Artifact{
		Version: 1,
		TestCase: artifact.TestCase{
			ID:        "runtime-report",
			Name:      "runtime report",
			StartedAt: time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC),
		},
		Schemas: []artifact.SchemaRef{{
			ID:   "example-openapi",
			Role: "sut",
			Type: "openapi",
			Source: artifact.SchemaSource{
				Type:    "inline",
				Content: openapi,
			},
		}, {
			ID: "dependency-openapi", Role: "dependency", Type: "openapi", AssetID: "inventory",
			Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.0
info: {title: Inventory, version: "1"}
paths:
  /items:
    get:
      responses:
        "200": {description: ok}
`},
		}},
		Traffic: []artifact.Flow{{
			ID:       "flow-1",
			Protocol: "http",
			Channel:  "test_to_sut",
			Request: artifact.Message{HTTP: &artifact.HttpMessage{
				Method: "GET",
				URL:    "http://example.test/hello",
			}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: http.StatusOK}},
		}, {
			ID: "flow-2", Protocol: "http", Channel: "sut_to_dependency", AssetID: "inventory",
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://mock.test/items"}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: http.StatusOK}},
		}},
	}
	writer, err := bundle.New(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := writer.Register(capture.TestCase, capture.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	capture.Schemas = frozen
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"bundle_path": writer.Path(),
	})
	if err != nil {
		t.Fatalf("encode report request: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/report", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("report request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read report response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report status = %d: %s", resp.StatusCode, body)
	}

	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode report response: %v", err)
	}
	for _, key := range []string{"index_path", "viewer_path"} {
		path, ok := response[key].(string)
		if !ok {
			t.Fatalf("missing %s: %v", key, response)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}

}

func TestRuntimeReportRejectsWorkflowExport(t *testing.T) {
	instance, err := New(Deps{Config: core.DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/report", strings.NewReader(`{"workflow_path":"output.yaml"}`))
	response := httptest.NewRecorder()
	instance.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("removed report field accepted: %d %s", response.Code, response.Body.String())
	}
}
