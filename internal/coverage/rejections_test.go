package coverage

import (
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func TestRejectionCoverageUsesEachInvocationsFlowAndExpectedStatus(t *testing.T) {
	first := artifact.RejectionTarget{Key: "required-sku", FlowID: "first-seed", Operation: "POST /orders", ExpectedStatuses: []int{400}}
	second := first
	second.FlowID, second.ExpectedStatuses = "second-seed", []int{422}
	makeCapture := func(id string, target artifact.RejectionTarget, passed bool) *artifact.Artifact {
		status := "fail"
		if passed {
			status = "pass"
		}
		return &artifact.Artifact{
			TestCase:    artifact.TestCase{ID: id, Status: status},
			Exploration: &artifact.Exploration{Mode: "rejection", Catalogue: []artifact.RejectionTarget{target}, Target: &target, RequestVerified: passed},
			Traffic:     []artifact.Flow{{ID: target.FlowID, Channel: "test_to_sut", Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: target.ExpectedStatuses[0]}}}},
			Assertions: []artifact.Assertion{
				{ID: "expected-rejection", FlowID: target.FlowID, Operator: "status_in", Result: status},
				{ID: "response-schema-" + target.FlowID, FlowID: target.FlowID, Operator: "schema", Result: status},
			},
		}
	}
	run := &bundle.Run{RunID: "run", Attempts: []bundle.AttemptRef{{AttemptID: "first"}, {AttemptID: "second"}}}
	report := buildRejectionCoverage(run, []*artifact.Artifact{makeCapture("first", first, false), makeCapture("second", second, true)})
	if report.Covered != 1 || report.Failed != 0 || len(report.Targets[0].Evidence) != 2 {
		t.Fatalf("coverage = %+v", report)
	}
	if evidence := report.Targets[0].Evidence[1]; evidence.FlowID != second.FlowID || evidence.Result != "covered" {
		t.Fatalf("second invocation evidence = %+v", evidence)
	}
}
