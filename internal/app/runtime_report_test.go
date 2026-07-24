package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/core"
	"github.com/phrond/evermock/internal/artifact"
)

func TestRuntimeReportWritesPostmanWorkflowAndHTML(t *testing.T) {
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
	_, err = artifact.WriteFile(outputDir, &artifact.Artifact{
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
		}},
	})
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	collectionPath := filepath.Join(outputDir, "custom.postman.json")
	workflowPath := filepath.Join(outputDir, "custom.workflow.yaml")
	htmlPath := filepath.Join(outputDir, "custom.html")
	payload, err := json.Marshal(map[string]any{
		"artifacts_dir":   outputDir,
		"sut_name":        "Example SUT",
		"collection_path": collectionPath,
		"workflow_path":   workflowPath,
		"html_path":       htmlPath,
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
	if response["collection_path"] != collectionPath {
		t.Fatalf("collection_path = %v", response["collection_path"])
	}
	if response["workflow_path"] != workflowPath {
		t.Fatalf("workflow_path = %v", response["workflow_path"])
	}

	postman, err := os.ReadFile(collectionPath)
	if err != nil {
		t.Fatalf("read Postman output: %v", err)
	}
	if !bytes.Contains(postman, []byte("schema.getpostman.com")) {
		t.Fatalf("unexpected Postman output: %s", postman)
	}
	workflow, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read workflow output: %v", err)
	}
	if !strings.Contains(string(workflow), "dsl: 1.0.3") ||
		!strings.Contains(string(workflow), "call: http") {
		t.Fatalf("unexpected workflow output:\n%s", workflow)
	}
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatalf("stat HTML output: %v", err)
	}
}
