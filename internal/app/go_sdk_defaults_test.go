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
	wiretap "github.com/mattlock83/wiretap/sdk/go"
)

func TestGoSDKEmptyListsClearProjectDefaultsForOneTest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	schemaPath := filepath.Join(t.TempDir(), "api.yaml")
	if err := os.WriteFile(schemaPath, []byte(permissiveDependencySchema().Source.Content), 0o600); err != nil {
		t.Fatal(err)
	}
	control, instance := dependencyCaptureRuntime(t)
	instance.project = &project.Config{
		SUT: project.API{ID: "orders", URL: upstream.URL, Schema: schemaPath, Kind: "service"},
		Dependencies: map[string]project.API{
			"inventory": {URL: upstream.URL, Schema: schemaPath, Kind: "mock"},
		},
	}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	runtime := wiretap.Runtime{BaseURL: control.URL}
	tests := []struct {
		name         string
		options      wiretap.TestOptions
		dependencies int
		schemas      int
		omittedSUT   bool
	}{
		{name: "inherit", dependencies: 1, schemas: 2},
		{name: "clear schemas", options: wiretap.TestOptions{Schemas: []wiretap.Schema{}, SchemaRequired: new(bool)}, dependencies: 1, schemas: 2, omittedSUT: true},
		{name: "clear dependencies", options: wiretap.TestOptions{Dependencies: []wiretap.Dependency{}}, schemas: 1},
		{name: "clear both", options: wiretap.TestOptions{Schemas: []wiretap.Schema{}, Dependencies: []wiretap.Dependency{}, SchemaRequired: new(bool)}, schemas: 1, omittedSUT: true},
		{name: "inherit again", dependencies: 1, schemas: 2},
	}
	expected := make(map[string]int, len(tests))
	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expected[t.Name()] = index
			session := runtime.StartTest(t, tc.options)
			if len(session.Dependencies) != tc.dependencies {
				t.Fatalf("dependency count = %d, want %d: %#v", len(session.Dependencies), tc.dependencies, session.Dependencies)
			}
			if tc.dependencies != 0 && session.Dependencies["inventory"] == "" {
				t.Fatalf("configured dependency was not inherited: %#v", session.Dependencies)
			}
		})
	}
	root := instance.currentBundle().Path()
	if err := runtime.FinishRun(t.Context()); err != nil {
		t.Fatal(err)
	}
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
