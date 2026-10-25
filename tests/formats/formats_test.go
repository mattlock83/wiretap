package formats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/assumptions"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/runoutput"
	"github.com/mattlock83/wiretap/internal/runset"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func validate(t *testing.T, name string, value any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", name+".schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source any
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name+".schema.json", source); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(name + ".schema.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("%s output violates schema: %v", name, err)
	}
	object := document.(map[string]any)
	key := "report_version"
	if name == "run-set" {
		key = "run_set_version"
	}
	object[key] = "999.0"
	if err := schema.Validate(object); err == nil {
		t.Fatalf("%s accepts unsupported version", name)
	}
}

func TestPublicReportSchemas(t *testing.T) {
	archive := func(provider bool) string {
		writer, err := bundle.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		identity := bundle.Identity{Application: &bundle.Application{ID: "consumer"}, APIID: "consumer-api", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock"}}
		channel, asset := "sut_to_dependency", "stock"
		if provider {
			identity.Application.ID = "provider"
			identity.APIID = "provider-api"
			identity.DependencyKinds = nil
			channel = "test_to_sut"
			asset = ""
		}
		if err := writer.Configure(identity); err != nil {
			t.Fatal(err)
		}
		capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "attempt", Name: "test", StartedAt: time.Now(), Status: "pass"}, Traffic: []artifact.Flow{{ID: "flow", Protocol: "http", Channel: channel, AssetID: asset, Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://example/items"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}}}}
		schemas, err := writer.Register(capture.TestCase, nil)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = schemas
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
		if err := writer.Finalize("complete"); err != nil {
			t.Fatal(err)
		}
		result, err := runoutput.Generate(t.Context(), writer.Path(), runoutput.Defaults())
		if err != nil {
			t.Fatal(err)
		}
		validate(t, "control-api", result)
		validate(t, "control-api", map[string]any{"report_version": "1.0", "producer": result.Producer, "status": "complete", "run_id": writer.Snapshot().RunID, "bundle_path": writer.Path(), "outputs": result})
		return writer.Path()
	}
	consumer, provider := archive(false), archive(true)
	pair, err := assumptions.Check(t.Context(), assumptions.Options{Mode: "consumer", Consumer: consumer, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	validate(t, "check-report", pair)
	validate(t, "check-report", &assumptions.StoreReport{Producer: pair.Producer, ReportVersion: pair.ReportVersion, RulesVersion: pair.RulesVersion, Mode: pair.Mode, Scope: "discovered_consumer_provider_relationships", ArtifactsPath: t.TempDir(), Compatible: pair.Compatible, Counts: pair.Counts, Comparisons: []*assumptions.Report{pair}})
	create := func(path string) string {
		target := filepath.Join(t.TempDir(), "set.json")
		manifest, err := runset.Create(t.Context(), []string{path}, target)
		if err != nil {
			t.Fatal(err)
		}
		validate(t, "run-set", manifest)
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
		return target
	}
	set, err := assumptions.CheckRunSets(t.Context(), create(consumer), create(provider), "consumer", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, "check-report", set)
}
