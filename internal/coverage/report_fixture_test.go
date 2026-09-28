package coverage

import (
	"context"
	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateReadsOnlyBundleInventory(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "tc-1", Name: "hello", StartedAt: time.Now().UTC(), Status: "pass"}}
	if _, err := writer.Register(capture.TestCase, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writer.Path(), "coverage.workflow.yaml"), []byte("document: {dsl: 1.0.3}"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Generate(context.Background(), writer.Path(), "hello", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactCount != 1 || len(report.Artifacts) != 1 || filepath.Base(report.Artifacts[0]) != "tc-1.json" {
		t.Fatalf("unexpected evidence: %#v", report)
	}
	loose := t.TempDir()
	if err := os.WriteFile(filepath.Join(loose, "tc-1.json"), []byte(`{"version":1,"testcase":{"id":"tc-1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(context.Background(), loose, "", false); err == nil {
		t.Fatal("accepted a loose capture without a bundle")
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
