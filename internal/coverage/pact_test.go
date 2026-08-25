package coverage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
)

func TestPactExportProjectsAssertionsDeduplicatesAndFiltersSensitiveTraffic(t *testing.T) {
	directory := t.TempDir()
	first := pactArtifact("first-test", "places an order", "inventory", "GET", "/items/widget?include=stock&access_token=secret", http.StatusOK,
		`{"id":"123e4567-e89b-12d3-a456-426614174000","available":true,"warehouse":"melbourne","items":[{"sku":"widget","secret":"hidden"}]}`)
	first.Assertions = []artifact.Assertion{
		{ID: "assert-1", Channel: "sut_to_dependency", Phase: "response", FlowID: "flow-1", Field: "$.available", PathLang: "jsonpath", Operator: "equals", Expected: true, Result: "pass"},
		{ID: "assert-2", Channel: "sut_to_dependency", Phase: "response", FlowID: "flow-1", Field: "$.items[0].sku", PathLang: "jsonpath", Operator: "equals", Expected: "widget", Result: "pass"},
	}
	first.Traffic[0].AssertionIDs = []string{"assert-1", "assert-2"}
	first.Traffic[0].ProviderStates = []artifact.ProviderState{{Name: "widget is in stock", Params: map[string]any{"sku": "widget"}}}
	first.Traffic[0].Request.HTTP.Headers["Authorization"] = []string{"Bearer secret"}
	first.Traffic[0].Request.HTTP.Headers["X-Phrond-Trace-Id"] = []string{"random-trace"}
	first.Traffic[0].Request.HTTP.Headers["X-Consumer"] = []string{"orders"}
	first.Traffic[0].Response.HTTP.Headers["Set-Cookie"] = []string{"session=secret"}
	first.Traffic = append(first.Traffic, artifact.Flow{
		ID: "flow-inbound", Protocol: "http", Channel: "test_to_sut",
		Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://orders.test/orders"}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: http.StatusCreated}},
	})

	duplicate := clonePactArtifact(t, first, "duplicate-test", "another order test")
	failed := pactArtifact("failed-test", "failed order", "inventory", "GET", "/items/failed", http.StatusOK, `{"available":false}`)
	failed.TestCase.Status = "fail"
	transport := pactArtifact("transport-test", "dependency unavailable", "inventory", "GET", "/items/unavailable", http.StatusBadGateway, `{"available":false}`)
	transport.Traffic[0].TransportError = "connection refused"
	payments := pactArtifact("payments-test", "takes payment", "payments", "POST", "/payments", http.StatusCreated, `{"approved":true}`)
	for _, doc := range []*artifact.Artifact{first, duplicate, failed, transport, payments} {
		writePactArtifact(t, directory, doc)
	}

	report, err := Generate(t.Context(), directory, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Producer == nil || report.Producer.CommitSHA != "commit-123" {
		t.Fatalf("producer metadata was not retained in the report: %#v", report.Producer)
	}
	export, err := PactsFromReport(report, PactOptions{
		ConsumerName: "orders-service", ProviderNames: map[string]string{"inventory": "inventory-service"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if export.ConsumerVersion != "commit-123" || export.Branch != "main" || len(export.Contracts) != 2 {
		t.Fatalf("unexpected Pact export identity or grouping: %#v", export)
	}
	if export.Contracts[0].Provider.Name != "inventory-service" || export.Contracts[1].Provider.Name != "payments" {
		t.Fatalf("provider participant names were not sorted or mapped: %#v", export.Contracts)
	}
	inventory := export.Contracts[0]
	if inventory.Consumer.Name != "orders-service" || inventory.Metadata.PactSpecification.Version != "3.0.0" {
		t.Fatalf("invalid Pact V3 participant or metadata: %#v", inventory)
	}
	if len(inventory.Interactions) != 1 {
		t.Fatalf("duplicate, failed, inbound, or errored flows leaked into Pact: %#v", inventory.Interactions)
	}
	interaction := inventory.Interactions[0]
	if interaction.Description != "another order test - GET /items/widget (200)" {
		t.Fatalf("duplicate interaction description was not selected deterministically: %q", interaction.Description)
	}
	if interaction.Request.Path != "/items/widget" || !reflect.DeepEqual(interaction.Request.Query, map[string][]string{"include": {"stock"}}) {
		t.Fatalf("request path or sanitized query is invalid: %#v", interaction.Request)
	}
	if interaction.Request.Headers["X-Consumer"] != "orders" || interaction.Request.Headers["Authorization"] != "" ||
		interaction.Request.Headers["X-Phrond-Trace-Id"] != "" || interaction.Response.Headers["Set-Cookie"] != "" {
		t.Fatalf("sensitive or proxy-specific headers leaked into Pact: %#v / %#v", interaction.Request.Headers, interaction.Response.Headers)
	}
	wantResponse := map[string]any{"available": true, "items": []any{map[string]any{"sku": "widget"}}}
	if !reflect.DeepEqual(interaction.Response.Body, wantResponse) {
		t.Fatalf("Pact response was not projected to consumed fields: got %#v want %#v", interaction.Response.Body, wantResponse)
	}
	if interaction.Response.MatchingRules.Body["$.available"].Matchers[0].Match != "equality" ||
		interaction.Response.MatchingRules.Body["$.items[0].sku"].Matchers[0].Match != "equality" {
		t.Fatalf("explicit assertions lost their equality semantics: %#v", interaction.Response.MatchingRules)
	}
	if len(interaction.ProviderStates) != 1 || interaction.ProviderStates[0].Name != "widget is in stock" {
		t.Fatalf("provider state metadata was not exported: %#v", interaction.ProviderStates)
	}
	if strings.Contains(first.Traffic[0].Response.HTTP.Body.Text, "warehouse") == false {
		t.Fatal("the original traffic artifact was mutated by Pact projection")
	}
}

func TestPactExportUsesAllResponseFieldsAndSchemaMatchersWhenUnasserted(t *testing.T) {
	directory := t.TempDir()
	doc := pactArtifact("schema-test", "reads inventory", "inventory", "GET", "/items/widget", http.StatusOK,
		`{"id":"123e4567-e89b-12d3-a456-426614174000","available":true,"quantity":3,"updated":"2026-08-25T01:02:03Z"}`)
	writePactArtifact(t, directory, doc)
	report, err := Generate(t.Context(), directory, "", false)
	if err != nil {
		t.Fatal(err)
	}
	export, err := PactsFromReport(report, PactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if export.ConsumerName != "orders-service" || len(export.Contracts) != 1 {
		t.Fatalf("producer-derived consumer identity failed: %#v", export)
	}
	interaction := export.Contracts[0].Interactions[0]
	body, ok := interaction.Response.Body.(map[string]any)
	if !ok || len(body) != 4 || body["quantity"] != float64(3) {
		t.Fatalf("unasserted response fields were not all preserved: %#v", interaction.Response.Body)
	}
	rules := interaction.Response.MatchingRules.Body
	if rules["$.id"].Matchers[0].Match != "regex" || !strings.Contains(rules["$.id"].Matchers[0].Regex, "[0-9a-fA-F]") {
		t.Fatalf("UUID schema did not become a Pact regex matcher: %#v", rules["$.id"])
	}
	if rules["$.available"].Matchers[0].Match != "boolean" || rules["$.quantity"].Matchers[0].Match != "integer" ||
		rules["$.updated"].Matchers[0].Match != "regex" {
		t.Fatalf("OpenAPI types/formats did not become Pact matchers: %#v", rules)
	}

	var output bytes.Buffer
	if err := WriteReport(&output, report, "pact"); err != nil {
		t.Fatal(err)
	}
	var standard struct {
		Consumer     struct{ Name string } `json:"consumer"`
		Provider     struct{ Name string } `json:"provider"`
		Interactions []struct {
			Response struct {
				MatchingRules struct {
					Body map[string]struct {
						Matchers []struct {
							Match string `json:"match"`
						} `json:"matchers"`
					} `json:"body"`
				} `json:"matchingRules"`
			} `json:"response"`
		} `json:"interactions"`
		Metadata struct {
			PactSpecification struct{ Version string } `json:"pactSpecification"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(output.Bytes(), &standard); err != nil {
		t.Fatalf("Pact output is not standard JSON: %v", err)
	}
	if standard.Consumer.Name != "orders-service" || standard.Provider.Name != "inventory" ||
		standard.Metadata.PactSpecification.Version != "3.0.0" ||
		standard.Interactions[0].Response.MatchingRules.Body["$.quantity"].Matchers[0].Match != "integer" {
		t.Fatalf("Pact JSON shape is invalid: %s", output.String())
	}
}

func TestPactExportSeparatesGenuinelyDifferentCollidingScenarios(t *testing.T) {
	directory := t.TempDir()
	first := pactArtifact("first", "reads inventory", "inventory", "GET", "/items/widget?view=summary", http.StatusOK, `{"available":true}`)
	second := pactArtifact("second", "reads inventory", "inventory", "GET", "/items/widget?view=full", http.StatusOK, `{"available":true}`)
	third := clonePactArtifact(t, first, "third", "reads inventory")
	third.Traffic[0].ProviderStates = []artifact.ProviderState{{Name: "inventory exists"}}
	for _, doc := range []*artifact.Artifact{first, second, third} {
		writePactArtifact(t, directory, doc)
	}
	report, err := Generate(t.Context(), directory, "Orders Service", false)
	if err != nil {
		t.Fatal(err)
	}
	export, err := PactsFromReport(report, PactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if export.ConsumerName != "Orders Service" || len(export.Contracts[0].Interactions) != 3 {
		t.Fatalf("distinct queries or provider states were deduplicated incorrectly: %#v", export)
	}
	seen := map[string]bool{}
	for _, interaction := range export.Contracts[0].Interactions {
		key, err := pactDescriptionKey(interaction)
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatalf("Pact interaction description/provider-state pair collided: %#v", interaction)
		}
		seen[key] = true
	}
}

func TestWritePactDirectoryIsDeterministicAndRejectsMixedConsumerVersions(t *testing.T) {
	directory := t.TempDir()
	writePactArtifact(t, directory, pactArtifact("inventory", "reads inventory", "inventory", "GET", "/items/widget", http.StatusOK, `{"available":true}`))
	writePactArtifact(t, directory, pactArtifact("payments", "takes payment", "payments", "POST", "/payments", http.StatusCreated, `{"approved":true}`))
	report, err := Generate(t.Context(), directory, "", false)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "pacts")
	export, paths, err := WritePactDirectory(report, output, PactOptions{})
	if err != nil || len(export.Contracts) != 2 || len(paths) != 2 {
		t.Fatalf("failed to write one Pact per provider: export=%#v paths=%#v err=%v", export, paths, err)
	}
	if filepath.Base(paths[0]) != "orders-service-inventory.json" || filepath.Base(paths[1]) != "orders-service-payments.json" {
		t.Fatalf("Pact output filenames are not stable: %#v", paths)
	}
	first, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	_, rewritten, err := WritePactDirectory(report, output, PactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(rewritten[0])
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("Pact output changed across equivalent exports: err=%v", err)
	}

	other := pactArtifact("mixed", "different build", "inventory", "GET", "/items/other", http.StatusOK, `{"available":true}`)
	other.Producer.CommitSHA = "another-commit"
	writePactArtifact(t, directory, other)
	report, err = Generate(t.Context(), directory, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PactsFromReport(report, PactOptions{}); err == nil || !strings.Contains(err.Error(), "multiple consumer commit versions") {
		t.Fatalf("mixed application versions were not rejected: %v", err)
	}
}

func TestPactExportRejectsTruncatedBodies(t *testing.T) {
	directory := t.TempDir()
	doc := pactArtifact("truncated", "truncated inventory", "inventory", "GET", "/items/widget", http.StatusOK, `{"avail`)
	doc.Traffic[0].Response.HTTP.Body.Truncated = true
	writePactArtifact(t, directory, doc)
	report, err := Generate(t.Context(), directory, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PactsFromReport(report, PactOptions{}); err == nil || !strings.Contains(err.Error(), "PHROND_MAX_CAPTURE_BYTES") {
		t.Fatalf("truncated response produced a misleading Pact: %v", err)
	}
}

func pactArtifact(id, name, assetID, method, requestPath string, status int, body string) *artifact.Artifact {
	doc := &artifact.Artifact{
		Version:  1,
		Producer: &artifact.Producer{Repo: "acme/orders-service", CommitSHA: "commit-123", Branch: "main"},
		TestCase: artifact.TestCase{ID: id, Name: name, Status: "pass", StartedAt: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)},
		Traffic: []artifact.Flow{{
			ID: "flow-1", Protocol: "http", Channel: "sut_to_dependency", AssetID: assetID,
			Request: artifact.Message{HTTP: &artifact.HttpMessage{
				Method: method, URL: "http://dependency.test" + requestPath,
				Headers: artifact.Headers{"Content-Type": {"application/json"}},
			}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{
				Status: status, Headers: artifact.Headers{"Content-Type": {"application/json"}},
				Body: &artifact.Body{Encoding: "utf8", Text: body},
			}},
		}},
	}
	if assetID == "inventory" {
		doc.Schemas = []artifact.SchemaRef{{
			ID: "inventory-api", Role: "dependency", Type: "openapi", AssetID: "inventory",
			Source: artifact.SchemaSource{Type: "inline", Content: pactInventoryOpenAPI},
		}}
	}
	return doc
}

func clonePactArtifact(t *testing.T, original *artifact.Artifact, id, name string) *artifact.Artifact {
	t.Helper()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var duplicate artifact.Artifact
	if err := json.Unmarshal(data, &duplicate); err != nil {
		t.Fatal(err)
	}
	duplicate.TestCase.ID = id
	duplicate.TestCase.Name = name
	return &duplicate
}

func writePactArtifact(t *testing.T, directory string, doc *artifact.Artifact) {
	t.Helper()
	if _, err := artifact.WriteFile(directory, doc); err != nil {
		t.Fatal(err)
	}
}

const pactInventoryOpenAPI = `openapi: 3.0.3
info: {title: Inventory, version: 1.0.0}
paths:
  /items/{sku}:
    get:
      parameters:
        - {name: sku, in: path, required: true, schema: {type: string}}
        - {name: include, in: query, schema: {type: string}}
        - {name: view, in: query, schema: {type: string}}
      responses:
        '200':
          description: inventory
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string, format: uuid}
                  available: {type: boolean}
                  warehouse: {type: string}
                  quantity: {type: integer}
                  updated: {type: string, format: date-time}
                  items:
                    type: array
                    items:
                      type: object
                      properties:
                        sku: {type: string}
                        secret: {type: string}
`
