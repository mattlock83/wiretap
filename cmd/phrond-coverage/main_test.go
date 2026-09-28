package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/coverage"
)

func TestLegacyImportMarksMissingSchemaUnknown(t *testing.T) {
	source := t.TempDir()
	old := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "historical", Name: "historical test", StartedAt: time.Now().UTC(), Status: "pass"}, Schemas: []artifact.SchemaRef{{ID: "sut", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: filepath.Join(source, "missing.yaml"), SHA256: "original-hash"}}}}
	if _, err := artifact.WriteFile(source, old); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "converted-bundle")
	if _, err := importLegacy(source, output); err != nil {
		t.Fatal(err)
	}
	run, captures, err := bundle.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "complete" || len(captures) != 1 {
		t.Fatalf("wrong imported run: %+v, captures %d", run, len(captures))
	}
	if captures[0].Schemas[0].Source.Type != "unavailable" || len(captures[0].CaptureDiagnostics) == 0 {
		t.Fatal("missing schema was treated as available")
	}
	report, err := coverage.Generate(t.Context(), output, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Provided.Specs) != 1 || report.Provided.Specs[0].DenominatorKnown {
		t.Fatalf("fabricated coverage denominator: %+v", report.Provided)
	}
}
