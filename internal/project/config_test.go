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

func TestGRPCConfigDefaultsAndLocalSourcePaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".wiretap.yaml")
	data := `version: 1
sut:
  id: orders
  protocol: grpc
  url: http://127.0.0.1:50051
  services: [orders.v1.Orders]
dependencies:
  inventory:
    protocol: grpc
    url: http://127.0.0.1:50052
    services: [inventory.v1.Inventory]
    schema:
      source: proto
      files: [inventory/v1/inventory.proto]
      import_paths: [./proto]
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SUT.GRPCSchema != nil || cfg.SUT.Protocol != "grpc" || cfg.Dependencies["inventory"].GRPCSchema.Source != "proto" || cfg.Dependencies["inventory"].GRPCSchema.ImportPaths[0] != filepath.Join(root, "proto") {
		t.Fatalf("unexpected gRPC project defaults: %#v", cfg)
	}
}

func TestGRPCConfigRejectsMissingContractsAndInvalidSources(t *testing.T) {
	for _, tc := range []struct{ name, sut, want string }{
		{"schema opt-out", "schema_required: false", "gRPC requires a schema"},
		{"explicit null", "schema: null", "explicit null"},
		{"empty source", "schema: {}", "unsupported gRPC schema source"},
		{"bare schema", "schema: api.proto", "source object"},
		{"empty string schema", "schema: ''", "source object"},
		{"zero timeout", "schema: {source: reflection, timeout: 0s}", "reflection timeout"},
		{"unbounded timeout", "schema: {source: reflection, timeout: 2m}", "reflection timeout"},
		{"invalid timeout", "schema: {source: reflection, timeout: soon}", "reflection timeout"},
		{"pathful URL", "url: http://127.0.0.1:50051/api", "origin without path"},
		{"local without services", "schema:\n    source: descriptor_set\n    path: api.protoset", "require services"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".wiretap.yaml")
			body := "version: 1\nsut:\n  protocol: grpc\n"
			if tc.name != "pathful URL" {
				body += "  url: http://127.0.0.1:50051\n"
			}
			body += "  " + strings.ReplaceAll(tc.sut, "\n", "\n  ") + "\n"
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
