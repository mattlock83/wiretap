package coverage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostmanCollectionContainsOnlyTestTraffic(t *testing.T) {
	report := sampleCaptureReport()
	collection, err := CollectionFromReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Item) != 1 || len(collection.Item[0].Item) != 1 || collection.Item[0].Item[0].Name != "test traffic" {
		t.Fatalf("unexpected folders: %#v", collection.Item)
	}
	var out bytes.Buffer
	if err := WritePostmanCollection(&out, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "mock traffic") || strings.Contains(out.String(), "sut_to_mock") {
		t.Fatalf("collection contains removed mock concepts: %s", out.String())
	}
}

func TestPostmanRoundTripRetainsProvidedCoverage(t *testing.T) {
	collection, err := CollectionFromReport(sampleCaptureReport())
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := ReportFromPostmanCollection(collection)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt.Provided.Specs) != 1 || len(rebuilt.TestCases) != 1 {
		t.Fatalf("unexpected rebuilt report: %#v", rebuilt)
	}
}

func TestGenerateIgnoresReportsBesideArtifacts(t *testing.T) {
	dir := t.TempDir()
	artifact := `version: 1
testcase:
  id: tc-1
  name: hello
  started_at: 2026-08-08T00:00:00Z
traffic: []
`
	if err := os.WriteFile(filepath.Join(dir, "tc-1.yaml"), []byte(artifact), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "coverage.workflow.yaml"), []byte("document:\n  dsl: 1.0.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "coverage.postman.json"), []byte(`{"info":{"name":"report"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Generate(context.Background(), dir, "hello", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactCount != 1 || len(report.Artifacts) != 1 || filepath.Base(report.Artifacts[0]) != "tc-1.yaml" {
		t.Fatalf("unexpected artifact discovery: %#v", report)
	}
}

func sampleCaptureReport() *Report {
	sample := FlowSample{
		TestCase: "hello world happy flow", FlowID: "flow-1", Protocol: "http", Channel: "test_to_sut",
		Request:  InteractionMessage{Method: "GET", URL: "http://sut.test/hello"},
		Response: InteractionMessage{Status: 200, Body: "hello"},
	}
	operation := OperationSamples{Operation: "GET /hello", Samples: []FlowSample{sample}}
	return &Report{
		GeneratedAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC), SUTName: "hello-sut", ArtifactCount: 1,
		Provided:  SpecCoverageGroup{Specs: []SpecCoverage{{SchemaID: "hello-openapi", Role: "sut", Type: "openapi", Source: "hello.openapi.yaml", Exercised: []OperationSamples{operation}}}},
		TestCases: []TestCaseCoverage{{Name: "hello world happy flow", Traffic: []FlowSample{sample}, Provided: []SpecOperationSamples{{SchemaID: "hello-openapi", Type: "openapi", Source: "hello.openapi.yaml", Operations: []OperationSamples{operation}}}}},
	}
}
