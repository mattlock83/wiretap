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

func TestRuntimeReportWritesPactContractsFromSuccessfulDependencyFlows(t *testing.T) {
	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Artifact.OutputDir = outputDir
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	_, err = artifact.WriteFile(outputDir, &artifact.Artifact{
		Version:  1,
		Producer: &artifact.Producer{Repo: "acme/orders", CommitSHA: "abc123", Branch: "main"},
		TestCase: artifact.TestCase{ID: "pact-report", Name: "orders check inventory", Status: "pass", StartedAt: time.Now().UTC()},
		Schemas: []artifact.SchemaRef{{
			ID: "inventory-api", Role: "dependency", Type: "openapi", AssetID: "inventory",
			Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Inventory, version: 1.0.0}
paths:
  /inventory:
    get:
      responses:
        '200':
          description: available
          content:
            application/json:
              schema:
                type: object
                properties:
                  available: {type: boolean}
                  ignored: {type: string}
`},
		}},
		Assertions: []artifact.Assertion{{
			ID: "assertion-1", Channel: "sut_to_dependency", Phase: "response", FlowID: "flow-1",
			Field: "$.available", PathLang: "jsonpath", Operator: "equals", Expected: true, Result: "pass",
		}},
		Traffic: []artifact.Flow{{
			ID: "flow-1", Protocol: "http", Channel: "sut_to_dependency", AssetID: "inventory",
			Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://mock.test/inventory"}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{
				Status: http.StatusOK, Headers: artifact.Headers{"Content-Type": {"application/json"}},
				Body: &artifact.Body{Encoding: "utf8", Text: `{"available":true,"ignored":"private"}`},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	pactDir := filepath.Join(outputDir, "contracts")
	payload, _ := json.Marshal(map[string]any{
		"artifacts_dir": outputDir, "consumer_name": "orders-service", "pact_dir": pactDir,
		"provider_names": map[string]string{"inventory": "inventory-service"},
	})
	response, err := http.Post(server.URL+"/v1/report", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("runtime Pact report status = %d: %s", response.StatusCode, body)
	}
	var result struct {
		PactDir         string   `json:"pact_dir"`
		PactPaths       []string `json:"pact_paths"`
		ConsumerName    string   `json:"consumer_name"`
		ConsumerVersion string   `json:"consumer_version"`
		Branch          string   `json:"branch"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.PactDir != pactDir || result.ConsumerName != "orders-service" || result.ConsumerVersion != "abc123" ||
		result.Branch != "main" || len(result.PactPaths) != 1 {
		t.Fatalf("Pact publication identity or paths are incomplete: %#v", result)
	}
	contract, err := os.ReadFile(result.PactPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contract, []byte(`"provider"`)) || !bytes.Contains(contract, []byte(`"inventory-service"`)) ||
		!bytes.Contains(contract, []byte(`"available": true`)) || bytes.Contains(contract, []byte(`"ignored"`)) {
		t.Fatalf("runtime generated the wrong Pact contract: %s", contract)
	}
}

func TestGenerateSchemaEndpointWritesStandardOpenAPIWithoutCapturedValues(t *testing.T) {
	outputDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Artifact.OutputDir = outputDir

	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	server := httptest.NewServer(appInstance.routes())
	defer server.Close()

	_, err = artifact.WriteFile(outputDir, &artifact.Artifact{
		Version: 1,
		TestCase: artifact.TestCase{
			ID:        "schema-generation",
			Name:      "creates an order",
			StartedAt: time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC),
		},
		Traffic: []artifact.Flow{{
			ID:       "flow-1",
			Protocol: "http",
			Channel:  "test_to_sut",
			Request: artifact.Message{HTTP: &artifact.HttpMessage{
				Method:  "POST",
				URL:     "http://orders.test/orders",
				Headers: artifact.Headers{"Content-Type": {"application/json"}},
				Body:    &artifact.Body{Text: `{"product_id":"sensitive-value","quantity":2}`},
			}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{
				Status:  http.StatusCreated,
				Headers: artifact.Headers{"Content-Type": {"application/json"}},
				Body:    &artifact.Body{Text: `{"id":"order-123"}`},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	schemaPath := filepath.Join(outputDir, "orders.generated.openapi.yaml")
	payload, _ := json.Marshal(map[string]any{
		"output_path": schemaPath,
		"title":       "Orders Service",
		"version":     "1.0.0",
	})
	resp, err := http.Post(server.URL+"/v1/generateSchema", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("generate schema request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate schema status = %d: %s", resp.StatusCode, body)
	}
	generated, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read generated schema: %v", err)
	}
	text := string(generated)
	if !strings.Contains(text, "openapi: 3.1.0") || !strings.Contains(text, "/orders:") {
		t.Fatalf("unexpected generated schema:\n%s", text)
	}
	if strings.Contains(text, "sensitive-value") || strings.Contains(text, "order-123") {
		t.Fatalf("generated schema leaked captured values:\n%s", text)
	}

	resp, err = http.Post(server.URL+"/v1/generateSchema", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("repeat generate schema request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("repeat generate schema status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}
