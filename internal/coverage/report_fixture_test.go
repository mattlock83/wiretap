package coverage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	report, err := Generate(context.Background(), dir, "hello", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactCount != 1 || len(report.Artifacts) != 1 || filepath.Base(report.Artifacts[0]) != "tc-1.yaml" {
		t.Fatalf("unexpected artifact discovery: %#v", report)
	}
}

func sampleCaptureReport() *Report {
	sample := FlowSample{TestCase: "hello world happy flow", FlowID: "flow-1", Protocol: "http", Channel: "test_to_sut", Request: InteractionMessage{Method: "GET", URL: "http://sut.test/hello"}, Response: InteractionMessage{Status: 200, Body: "hello"}}
	operation := OperationSamples{Operation: "GET /hello", Samples: []FlowSample{sample}}
	return &Report{
		GeneratedAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC), SUTName: "hello-sut", ArtifactCount: 1,
		Provided:  SpecCoverageGroup{Specs: []SpecCoverage{{SchemaID: "hello-openapi", Role: "sut", Type: "openapi", Source: "hello.openapi.yaml", Exercised: []OperationSamples{operation}}}},
		TestCases: []TestCaseCoverage{{Name: "hello world happy flow", Traffic: []FlowSample{sample}, Provided: []SpecOperationSamples{{SchemaID: "hello-openapi", Type: "openapi", Source: "hello.openapi.yaml", Operations: []OperationSamples{operation}}}}},
	}
}

func sampleDualCaptureReport() *Report {
	report := sampleCaptureReport()
	dependency := FlowSample{TestCase: report.TestCases[0].Name, FlowID: "flow-2", Protocol: "http", Channel: "sut_to_dependency", AssetID: "inventory-service", ParentFlowID: "flow-1", TraceID: "trace-1", Request: InteractionMessage{Method: "POST", URL: "http://wiremock.test/inventory", Body: `{"sku":"widget"}`}, Response: InteractionMessage{Status: 200, Body: `{"available":true}`}}
	operation := OperationSamples{Operation: "POST /inventory", Samples: []FlowSample{dependency}}
	report.Dependent = SpecCoverageGroup{Specs: []SpecCoverage{{SchemaID: "inventory-openapi", Role: "dependency", AssetID: "inventory-service", Type: "openapi", Source: "inventory.openapi.yaml", Exercised: []OperationSamples{operation}}}}
	report.TestCases[0].Traffic = append(report.TestCases[0].Traffic, dependency)
	report.TestCases[0].Dependent = []SpecOperationSamples{{SchemaID: "inventory-openapi", AssetID: "inventory-service", Type: "openapi", Source: "inventory.openapi.yaml", Operations: []OperationSamples{operation}}}
	return report
}
