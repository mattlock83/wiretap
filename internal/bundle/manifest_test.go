package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeManifestPreservesSourceNumbersWithoutOpeningArchiveFiles(t *testing.T) {
	writer, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(Identity{
		Application: &Application{ID: "shop"}, APIID: "shop-api", SUTTargetKind: "service",
		DependencyKinds: map[string]string{"stock": "mock"},
		SUTSource:       map[string]any{"build": json.Number("9007199254740993")},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(writer.Path(), "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if run.SUTSource["build"] != json.Number("9007199254740993") {
		t.Fatalf("source metadata was rounded: %#v", run.SUTSource)
	}
	mutations := []struct {
		name, message string
		edit          func(*Run)
	}{
		{"format", "unsupported evidence version", func(run *Run) { run.EvidenceVersion = "4.0" }},
		{"capability", "unsupported required capability", func(run *Run) { run.RequiredCapabilities = []string{"future-mappings.v1"} }},
		{"kind", "invalid dependency identity or target kind", func(run *Run) { run.DependencyKinds["stock"] = "moc" }},
		{"changed kind", "inventory disagrees", func(run *Run) { run.DependencyKinds["stock"] = "service" }},
		{"missing kind", "inventory disagrees", func(run *Run) { delete(run.DependencyKinds, "stock") }},
		{"changed API", "inventory disagrees", func(run *Run) { run.APIID = "different" }},
		{"missing inventory", "missing or unsupported API inventory", func(run *Run) { run.Dependencies = nil }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			run, err := DecodeManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			mutation.edit(run)
			changed, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeManifest(changed); err == nil || !strings.Contains(err.Error(), mutation.message) {
				t.Fatalf("wanted %q, got %v", mutation.message, err)
			}
		})
	}
}
