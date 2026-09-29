package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesPathsAndEnvironmentFromProject(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "tests")
	os.Mkdir(child, 0755)
	t.Chdir(child)
	t.Setenv("TEST_SUT_URL", "http://localhost:8080")
	path := filepath.Join(root, ".wiretap.yaml")
	os.WriteFile(path, []byte("version: 1\nsut:\n  url: ${TEST_SUT_URL}\n  schema: schemas/orders.yaml\ndependencies:\n  catalog:\n    url: http://localhost:9090\n    schema_required: false\n"), 0600)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SUT.URL != "http://localhost:8080" || cfg.SUT.Schema != filepath.Join(root, "schemas/orders.yaml") || cfg.OutputDir != filepath.Join(root, "artifacts") || cfg.Dependencies["catalog"].RequiresSchema() {
		t.Fatalf("wrong configuration: %#v", cfg)
	}
}
func TestLoadRejectsMistakesAndKeepsSpecsRequired(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"missing schema", "version: 1\nsut: {}", "schema is required"},
		{"misspelled option", "version: 1\nsut: {schema: api.yaml}\noutputs: bad", "field outputs"},
		{"missing variable", "version: 1\nsut: {schema: '${WIRETAP_TEST_MISSING_VARIABLE}'}", "is not set"},
		{"extra document", "version: 1\nsut: {schema: api.yaml}\n---\nversion: 1", "one YAML document"},
		{"SUT generation", "version: 1\nsut: {generate_schema: true}", "field generate_schema"},
		{"dependency generation", "version: 1\nsut: {schema: api.yaml}\ndependencies:\n  dep: {generate_schema: true}", "field generate_schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".wiretap.yaml")
			os.WriteFile(path, []byte(tc.body), 0600)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
