package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/project"
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

func TestRemovedGRPCControlOptionsAreRejectedBeforeRunStarts(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	for _, tc := range []struct {
		name, path string
		payload    map[string]any
	}{
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

func TestAppRejectsUnsupportedConfiguredProtocols(t *testing.T) {
	for _, config := range []*project.Config{
		{SUT: project.API{Protocol: "grpc"}},
		{Dependencies: map[string]project.API{"inventory": {Protocol: "grpc"}}},
	} {
		if _, err := New(Deps{Project: config}); err == nil || !strings.Contains(err.Error(), "only HTTP JSON/REST") {
			t.Fatalf("unsupported protocol accepted: %v", err)
		}
	}
}
