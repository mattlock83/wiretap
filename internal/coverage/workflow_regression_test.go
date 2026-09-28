package coverage

import "testing"

func TestReviewWorkflowMatchesAttempt(t *testing.T) {
	first := FlowSample{TestCase: "same name", TestCaseID: "attempt-one", FlowID: "flow-1", Request: InteractionMessage{Method: "GET", URL: "http://sut/first"}}
	second := FlowSample{TestCase: "same name", TestCaseID: "attempt-two", FlowID: "flow-1", Request: InteractionMessage{Method: "POST", URL: "http://sut/second"}}
	testcase := TestCaseCoverage{Name: "same name", Provided: []SpecOperationSamples{{SchemaID: "first-schema", Operations: []OperationSamples{{Operation: "GET /first", Samples: []FlowSample{first}}}}, {SchemaID: "second-schema", Operations: []OperationSamples{{Operation: "POST /second", Samples: []FlowSample{second}}}}}}
	operation, schema := workflowSampleMatch(testcase, second, "sut")
	if operation != "POST /second" || schema != "second-schema" {
		t.Fatalf("wrong attempt matched: operation=%q schema=%q", operation, schema)
	}
}
