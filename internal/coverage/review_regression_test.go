package coverage

import (
	"github.com/phrond/evermock/internal/artifact"
	"testing"
)

func TestReviewPostmanAttemptMetadata(t *testing.T) {
	report := sampleCaptureReport()
	first := report.TestCases[0].Provided[0].Operations[0].Samples[0]
	first.TestCaseID, first.TestTraceID, first.RootSpanID = "attempt-one", "trace-one", "root-one"
	second := first
	second.TestCaseID, second.TestTraceID, second.RootSpanID = "attempt-two", "trace-two", "root-two"
	report.TestCases[0].Provided[0].Operations[0].Samples = []FlowSample{first, second}
	collection, err := CollectionFromReport(report)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := ReportFromPostmanCollection(collection)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt.TestCases[0].Attempts) != 2 {
		t.Errorf("expected two attempts, got %#v", rebuilt.TestCases[0].Attempts)
	}
	for _, sample := range rebuilt.TestCases[0].Traffic {
		if sample.TestTraceID == "" || sample.RootSpanID == "" {
			t.Errorf("missing metadata: %#v", sample)
		}
	}
}

func TestReviewWorkflowMatchesAttempt(t *testing.T) {
	first := FlowSample{TestCase: "same name", TestCaseID: "attempt-one", FlowID: "flow-1", Request: InteractionMessage{Method: "GET", URL: "http://sut/first"}}
	second := FlowSample{TestCase: "same name", TestCaseID: "attempt-two", FlowID: "flow-1", Request: InteractionMessage{Method: "POST", URL: "http://sut/second"}}
	testcase := TestCaseCoverage{Name: "same name", Provided: []SpecOperationSamples{{SchemaID: "first-schema", Operations: []OperationSamples{{Operation: "GET /first", Samples: []FlowSample{first}}}}, {SchemaID: "second-schema", Operations: []OperationSamples{{Operation: "POST /second", Samples: []FlowSample{second}}}}}}
	operation, schema := workflowSampleMatch(testcase, second, "sut")
	if operation != "POST /second" || schema != "second-schema" {
		t.Fatalf("wrong attempt matched: operation=%q schema=%q", operation, schema)
	}
}

func TestPostmanPreservesAttemptsWithoutTraffic(t *testing.T) {
	report := sampleCaptureReport()
	report.TestCases = []TestCaseCoverage{{Name: "same name", Attempts: []artifact.TestCase{
		{ID: "attempt-a", Name: "same name", TraceID: "trace-a", RootSpanID: "root-a", Status: "fail"},
		{ID: "attempt-b", Name: "same name", TraceID: "trace-b", RootSpanID: "root-b", Status: "skip"},
	}}}
	report.Notes = []string{"capture drain timed out"}
	collection, err := CollectionFromReport(report)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := ReportFromPostmanCollection(collection)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt.TestCases) != 1 || len(rebuilt.TestCases[0].Attempts) != 2 {
		t.Fatalf("empty attempts lost: %#v", rebuilt.TestCases)
	}
	if len(rebuilt.Notes) == 0 || rebuilt.Notes[0] != "capture drain timed out" {
		t.Fatalf("capture diagnostic lost: %v", rebuilt.Notes)
	}
	for _, attempt := range rebuilt.TestCases[0].Attempts {
		if attempt.TraceID == "" || attempt.RootSpanID == "" || (attempt.Status != "fail" && attempt.Status != "skip") {
			t.Fatalf("attempt metadata lost: %#v", attempt)
		}
	}
}
