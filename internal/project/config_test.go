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
		{"misspelled option", "version: 1\nsut: {schema: api.yaml}\noutputz: bad", "field outputz"},
		{"missing variable", "version: 1\nsut: {schema: '${WIRETAP_TEST_MISSING_VARIABLE}'}", "is not set"},
		{"extra document", "version: 1\nsut: {schema: api.yaml}\n---\nversion: 1", "one YAML document"},
		{"redaction removed", "version: 1\nsut: {schema_required: false}\nredactions: []", "field redactions"},
		{"publication removed", "version: 1\nsut: {schema: api.yaml}\npublication: {config: publication.json}", "field publication"},
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

func TestHTTPDependencySchemasAreOptionalWithoutWeakeningProvidedAPI(t *testing.T) {
	root := t.TempDir()
	cfg, err := Decode([]byte(`version: 1
sut: {url: http://orders.test, schema: orders.yaml}
dependencies:
  inventory: {url: http://inventory.test, kind: mock}
  sut: {url: http://another.test, kind: service}
  supplied: {url: http://supplied.test, schema: supplied.yaml}
  strict: {url: http://strict.test, schema: strict.yaml, schema_required: true}
`), filepath.Join(root, ".wiretap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SUT.RequiresSchema() || cfg.SUT.SchemaRequired != nil {
		t.Fatal("main SUT schema default changed")
	}
	for _, id := range []string{"inventory", "sut", "supplied"} {
		api := cfg.Dependencies[id]
		if api.RequiresSchema() || api.SchemaRequired == nil {
			t.Fatalf("HTTP dependency %q did not resolve to optional: %+v", id, api)
		}
	}
	if cfg.Dependencies["supplied"].Schema != filepath.Join(root, "supplied.yaml") || len(cfg.Dependencies["supplied"].Schemas("supplied", "dependency")) != 1 {
		t.Fatal("optional mode discarded a supplied specification")
	}
	if !cfg.Dependencies["strict"].RequiresSchema() {
		t.Fatal("explicit dependency requirement was ignored")
	}
	for _, document := range []string{
		"version: 1\nsut: {url: http://orders.test}\ndependencies:\n  inventory: {url: http://inventory.test}\n",
		"version: 1\nsut: {schema: orders.yaml}\ndependencies:\n  inventory: {url: http://inventory.test, schema_required: true}\n",
	} {
		if _, err := Decode([]byte(document), filepath.Join(root, ".wiretap.yaml")); err == nil {
			t.Fatalf("accepted missing required contract: %s", document)
		}
	}
}

func TestApplicationConfiguration(t *testing.T) {
	for _, identity := range []string{
		`{name: Orders, id: orders-app, description: "Order processing"}`,
		`{name: Orders}`,
		`{id: orders-app}`,
	} {
		cfg, err := Decode([]byte("version: 1\napplication: "+identity+"\nsut: {id: orders-api, schema_required: false}\n"), filepath.Join(t.TempDir(), ".wiretap.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Application == nil || cfg.SUT.ID != "orders-api" {
			t.Fatalf("application or independent API identity lost: %+v", cfg)
		}
		if strings.Contains(identity, "name:") && cfg.Application.Name != "Orders" {
			t.Fatalf("application name = %q", cfg.Application.Name)
		}
		if strings.Contains(identity, "id:") && cfg.Application.ID != "orders-app" {
			t.Fatalf("application ID = %q", cfg.Application.ID)
		}
		if strings.Contains(identity, "description:") && cfg.Application.Description != "Order processing" {
			t.Fatalf("application description = %q", cfg.Application.Description)
		}
	}
	for _, identity := range []string{`{}`, `{name: " ", id: " "}`, `{description: Orders}`, `{name: Orders, app_id: orders-app}`} {
		if _, err := Decode([]byte("version: 1\napplication: "+identity+"\nsut: {schema_required: false}\n"), ".wiretap.yaml"); err == nil {
			t.Fatalf("accepted invalid application: %s", identity)
		}
	}
}

func TestAPINameConfiguration(t *testing.T) {
	data := `version: 1
sut: {id: orders-api, name: Orders API, schema_required: false}
dependencies:
  inventory: {name: Inventory Service, kind: mock, url: http://inventory.test, schema_required: false}
`
	cfg, err := Decode([]byte(data), filepath.Join(t.TempDir(), ".wiretap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SUT.Name != "Orders API" || cfg.SUT.ID != "orders-api" || cfg.Dependencies["inventory"].Name != "Inventory Service" {
		t.Fatalf("API display names or independent IDs lost: %+v", cfg)
	}
	for _, dependency := range []string{
		`{display_name: Inventory Service, schema_required: false}`,
		`{name: {label: Inventory Service}, schema_required: false}`,
	} {
		_, err := Decode([]byte("version: 1\nsut: {schema_required: false}\ndependencies:\n  inventory: "+dependency+"\n"), ".wiretap.yaml")
		if err == nil {
			t.Fatalf("accepted invalid API name configuration: %s", dependency)
		}
	}
}

func TestRemovedRuntimeAndExportOptionsAreRejected(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"grpc SUT", "sut: {protocol: grpc, schema_required: false}", "field protocol"},
		{"grpc dependency", "sut: {schema_required: false}\ndependencies:\n  inventory: {url: http://inventory.test, protocol: grpc}", "field protocol"},
		{"services", "sut: {schema_required: false, services: [inventory.Inventory]}", "field services"},
		{"capture listener", "sut: {schema_required: false, capture_listen: ':50051'}", "field capture_listen"},
		{"capture TLS", "sut: {schema_required: false, capture_tls: {cert_file: server.crt, key_file: server.key}}", "field capture_tls"},
		{"upstream TLS", "sut: {schema_required: false, upstream_tls: {ca_file: server.crt}}", "field upstream_tls"},
		{"schema source", "sut: {schema: {source: reflection}}", "cannot unmarshal"},
		{"null schema", "sut: {schema: null}", "explicit null"},
		{"Postman output", "sut: {schema_required: false}\noutputs: {postman: true}", "field outputs"},
		{"output selection", "sut: {schema_required: false}\noutputs: {coverage: [json]}", "field outputs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte("version: 1\n"+tc.body+"\n"), filepath.Join(t.TempDir(), ".wiretap.yaml"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildSourcesAndProviderMappings(t *testing.T) {
	t.Setenv("APP_COMMIT", "commit-123")
	cfg, err := Decode([]byte("version: 1\nsut: {schema_required: false}\ntest_source: {revision: '${APP_COMMIT}'}\nsut_source: {build_id: ci-12}\ndependencies:\n  stock: {url: http://stock.test, schema_required: false}\nprovider_mappings:\n  stock: {application_id: inventory, api_id: stock-http, version: v2}\n"), filepath.Join(t.TempDir(), ".wiretap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TestSource["revision"] != "commit-123" || cfg.SUTSource["build_id"] != "ci-12" || cfg.ProviderMappings["stock"].APIID != "stock-http" {
		t.Fatalf("missing provenance: %+v", cfg)
	}
	if _, err := Decode([]byte("version: 1\nsut: {schema_required: false}\nprovider_mappings:\n  missing: {application_id: inventory, api_id: stock-http}\n"), filepath.Join(t.TempDir(), ".wiretap.yaml")); err == nil {
		t.Fatal("unknown dependency mapping accepted")

	}
}
