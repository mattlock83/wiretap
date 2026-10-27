package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestSchemaSetupRequiresSpecsUnlessExplicitlyOmitted(t *testing.T) {
	valid := artifact.SchemaRef{
		ID:   "orders-api",
		Role: "sut",
		Type: "openapi",
		Source: artifact.SchemaSource{
			Type: "inline",
			Content: `openapi: 3.1.0
info:
  title: Orders
  version: 1.0.0
paths: {}
`,
		},
	}
	invalid := valid
	invalid.Source.Content = "not: [valid"

	tests := []struct {
		name         string
		schemas      []artifact.SchemaRef
		allowMissing bool
		want         string
	}{
		{name: "missing schema", want: "OpenAPI schema is required"},
		{name: "explicit omission", allowMissing: true},
		{name: "valid schema", schemas: []artifact.SchemaRef{valid}},
		{name: "optional valid schema", schemas: []artifact.SchemaRef{valid}, allowMissing: true},
		{name: "invalid schema", schemas: []artifact.SchemaRef{invalid}, want: "invalid or unreadable"},
		{name: "optional invalid schema", schemas: []artifact.SchemaRef{invalid}, allowMissing: true, want: "invalid or unreadable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSchemaSetup(test.schemas, test.allowMissing)
			if err == nil {
				_, err = prepareTestSchemas(t, test.schemas)
			}
			if test.want == "" {
				if err != nil {
					t.Fatalf("validate schema setup: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRemovedControlOptionsAreRejectedBeforeRunStarts(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	for _, tc := range []struct {
		name, path string
		payload    map[string]any
	}{
		{"boundary registration", "/v1/startRun", map[string]any{"boundaries": map[string]any{"sut": map[string]any{"schema_required": false}}}},
		{"singular dependency schema", "/v1/setupTest", map[string]any{"schema_required": false, "dependencies": []map[string]any{{"asset_id": "inventory", "upstream_url": "http://inventory.test", "schema": map[string]any{}}}}},
		{"SUT protocol", "/v1/setupTest", map[string]any{"schema_required": false, "sut_protocol": "grpc"}},
		{"dependency protocol", "/v1/setupTest", map[string]any{"schema_required": false, "dependencies": []map[string]any{{"asset_id": "inventory", "upstream_url": "http://inventory.test", "protocol": "grpc"}}}},
		{"protobuf SUT schema", "/v1/setupTest", map[string]any{"schemas": []map[string]any{{"id": "rpc", "type": "protobuf", "source": map[string]any{"type": "inline", "content": "not a JSON/REST schema"}}}}},
		{"SUT boundary", "/v1/startRun", map[string]any{"boundaries": map[string]any{"sut": map[string]any{"protocol": "grpc", "schema_required": false, "url": "http://inventory.test"}}}},
		{"dependency boundary", "/v1/startRun", map[string]any{"boundaries": map[string]any{"sut": map[string]any{"schema_required": false}, "dependencies": map[string]any{"inventory": map[string]any{"protocol": "grpc", "url": "http://inventory.test"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.Post(control.URL+tc.path, "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", response.StatusCode)
			}
			if instance.currentBundle() != nil || instance.hasActiveSessions() {
				t.Fatal("unsupported RPC started capture")
			}
		})
	}
}

func prepareTestSchemas(t *testing.T, refs []artifact.SchemaRef) ([]bundle.PreparedSchema, error) {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return writer.RegisterPrepared(artifact.TestCase{ID: "schema-validation"}, refs)
}

func validateTestCapture(t *testing.T, capture *artifact.Artifact) {
	t.Helper()
	prepared, err := prepareTestSchemas(t, capture.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	capture.Schemas = nil
	for _, schema := range prepared {
		capture.Schemas = append(capture.Schemas, schema.Ref)
	}
	applySchemaValidation(capture, prepared)
}

func TestSetupReadsEachContractOnceAndReusesItForValidation(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		_, _ = w.Write([]byte(negativeRequestSpec))
	}))
	defer server.Close()
	control, instance := dependencyCaptureRuntime(t)
	for attempt := range 2 {
		session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
			"name":    "prepared contract",
			"schemas": []artifact.SchemaRef{{ID: "orders", Type: "openapi", Source: artifact.SchemaSource{Type: "url", URL: server.URL}}},
		})
		if got := reads.Load(); got != int32(attempt+1) {
			t.Fatalf("setup read contract %d times for %d attempts", got, attempt+1)
		}
		instance.sessionsMu.Lock()
		store := instance.sessions[session["session_id"].(string)].Store
		instance.sessionsMu.Unlock()
		store.mu.Lock()
		store.active.flows = []artifact.Flow{{ID: "health", Protocol: "http", Channel: "test_to_sut", Request: &artifact.HttpMessage{Method: "GET", URL: "http://provider/health"}, Response: &artifact.HttpMessage{Status: 200}}}
		store.mu.Unlock()
		finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
		capture := dependencyArtifact(t, finished)
		if capture.SchemaValidation.Status != "pass" || reads.Load() != int32(attempt+1) {
			t.Fatal("finish did not validate against its already prepared contract")
		}
	}
}
