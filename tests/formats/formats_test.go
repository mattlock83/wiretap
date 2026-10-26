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
	} else if name == "bundle-evidence" {
		key = "format_version"
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
		capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "attempt", Name: "test", StartedAt: time.Now(), Status: "pass"}, Traffic: []artifact.Flow{{ID: "flow", Protocol: "http", Channel: channel, AssetID: asset, Request: &artifact.HttpMessage{Method: "GET", URL: "http://example/items"}, Response: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{}`}}}}}
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
		run := writer.Snapshot()
		validate(t, "bundle-evidence", run)
		raw, err := os.ReadFile(filepath.Join(writer.Path(), run.Attempts[0].Path))
		if err != nil {
			t.Fatal(err)
		}
		var attempt bundle.Attempt
		if err := json.Unmarshal(raw, &attempt); err != nil {
			t.Fatal(err)
		}
		validate(t, "bundle-evidence", attempt)
		// Transport failures still belong to the HTTP archive, even without a reply.
		attempt.Capture.Traffic[0].Response = nil
		attempt.Capture.Traffic[0].TransportError = "connection reset"
		validate(t, "bundle-evidence", attempt)
		result, err := runoutput.Generate(t.Context(), writer.Path())
		if err != nil {
			t.Fatal(err)
		}
		validate(t, "control-api", result)
		raw, err = os.ReadFile(result.Coverage["json"])
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		validate(t, "coverage-report", report)
		validate(t, "control-api", map[string]any{"report_version": "1.0", "producer": result.Producer, "status": "complete", "run_id": writer.Snapshot().RunID, "bundle_path": writer.Path(), "outputs": result})
		return writer.Path()
	}
	consumer, provider := archive(false), archive(true)
	pair, err := assumptions.Check(t.Context(), assumptions.Options{Consumer: consumer, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	validate(t, "check-report", pair)
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
	consumerSet, providerSet := create(consumer), create(provider)
	set, err := assumptions.CheckRunSets(t.Context(), consumerSet, providerSet, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, "check-report", set)
}
