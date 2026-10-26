package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/project"
)

func TestEmptyListsClearProjectDefaultsForOneTest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	schemaPath := filepath.Join(t.TempDir(), "api.yaml")
	if err := os.WriteFile(schemaPath, []byte(permissiveDependencySchema().Source.Content), 0o600); err != nil {
		t.Fatal(err)
	}
	control, instance := dependencyCaptureRuntime(t)
	required := true
	instance.project = &project.Config{
		SUT:        project.API{ID: "orders", URL: upstream.URL, Schema: schemaPath, Kind: "service", SchemaRequired: &required},
		TestSource: map[string]any{"commit": "configured", "repo": "tests"},
		Dependencies: map[string]project.API{
			"inventory": {URL: upstream.URL, Schema: schemaPath, Kind: "mock"},
		},
	}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	apiStatus(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "conflicting source", "test_source": map[string]any{"commit": "override"}, "schema_required": false,
	}, http.StatusBadRequest)
	if !required || instance.project.TestSource["commit"] != "configured" {
		t.Fatalf("rejected override mutated project defaults: required=%v source=%v", required, instance.project.TestSource)
	}
	tests := []struct {
		name         string
		options      map[string]any
		dependencies int
		schemas      int
		omittedSUT   bool
	}{
		{name: "inherit", dependencies: 1, schemas: 2},
		{name: "clear schemas", options: map[string]any{"schemas": []any{}, "schema_required": false}, dependencies: 1, schemas: 2, omittedSUT: true},
		{name: "clear dependencies", options: map[string]any{"dependencies": []any{}}, schemas: 1},
		{name: "clear both", options: map[string]any{"schemas": []any{}, "dependencies": []any{}, "schema_required": false}, schemas: 1, omittedSUT: true},
		{name: "inherit again", dependencies: 1, schemas: 2},
	}
	expected := make(map[string]int, len(tests))
	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expected[t.Name()] = index
			payload := map[string]any{"name": t.Name()}
			for key, value := range tc.options {
				payload[key] = value
			}
			session := postDependencyJSON(t, control.URL, "/v1/setupTest", payload)
			t.Cleanup(func() {
				postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
			})
			dependencies := session["dependencies"].(map[string]any)
			if len(dependencies) != tc.dependencies {
				t.Fatalf("dependency count = %d, want %d: %#v", len(dependencies), tc.dependencies, dependencies)
			}
			if tc.dependencies != 0 && dependencies["inventory"] == "" {
				t.Fatalf("configured dependency was not inherited: %#v", dependencies)
			}
		})
	}
	if !required || instance.project.TestSource["commit"] != "configured" {
		t.Fatalf("test overrides mutated project defaults: required=%v source=%v", required, instance.project.TestSource)
	}
	root := instance.currentBundle().Path()
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	_, captures, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != len(tests) {
		t.Fatalf("capture count = %d, want %d", len(captures), len(tests))
	}
	for _, capture := range captures {
		index, ok := expected[capture.TestCase.Name]
		if !ok {
			t.Fatalf("unexpected capture: %s", capture.TestCase.Name)
		}
		tc := tests[index]
		if capture.TestCase.Status != "pass" || len(capture.Schemas) != tc.schemas {
			t.Fatalf("%s: unexpected capture status or schemas: %s %#v", tc.name, capture.TestCase.Status, capture.Schemas)
		}
		sutSchemas, dependencies := 0, 0
		for _, schema := range capture.Schemas {
			switch schema.Role {
			case "sut":
				sutSchemas++
				if schema.AssetID != "orders" || (schema.Source.Type == "omitted") != tc.omittedSUT {
					t.Fatalf("%s: wrong SUT schema: %#v", tc.name, schema)
				}
			case "dependency":
				dependencies++
				if schema.AssetID != "inventory" || schema.Source.Type == "omitted" {
					t.Fatalf("%s: wrong dependency schema: %#v", tc.name, schema)
				}
			}
		}
		if sutSchemas != 1 || dependencies != tc.dependencies {
			t.Fatalf("%s: schema roles = %d SUT, %d dependencies", tc.name, sutSchemas, dependencies)
		}
	}
}
