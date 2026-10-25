package coverage

import (
	"encoding/json"
	"strings"
	"testing"
)

func policyReport() *Report {
	return &Report{ReportVersion: ReportVersion, Contracts: &ContractCoverage{RulesVersion: ContractRulesVersion, RunID: "run", Complete: true, Specs: []*ContractSpec{{
		RulesVersion: ContractRulesVersion, APIID: "orders", SchemaID: "orders", Origin: "provided", Revision: "revision-a", DenominatorKnown: true,
		Targets: []*ContractTarget{
			{Key: "get", Family: "operation", Operation: "GET /orders", State: "covered"},
			{Key: "post", Family: "operation", Operation: "POST /orders", State: "not_covered"},
			{Key: "enum", Family: "enum", Operation: "GET /orders", State: "unknown"},
		},
	}}}}
}

func TestPolicyGatesOnlyExplicitAPIsOperationsAndFamilies(t *testing.T) {
	report := policyReport()
	before, _ := json.Marshal(report)
	policy := Policy{PolicyVersion: PolicyVersion, APIs: []APIPolicy{{APIID: "orders", Origin: "provided", RequiredOperations: []string{"GET /orders"}}}}
	result, err := EvaluatePolicy(report, policy)
	if err != nil || !result.Passed {
		t.Fatalf("unselected uncovered targets must not fail an explicit gate: %+v, %v", result, err)
	}
	policy.APIs[0].RequiredFamilies = []string{"operation"}
	result, err = EvaluatePolicy(report, policy)
	if err != nil || result.Passed || len(result.Findings) != 1 || result.Findings[0].TargetKey != "post" || result.Findings[0].Code != "required_family_uncovered" {
		t.Fatalf("operation family gate: %+v, %v", result, err)
	}
	policy.APIs[0].FailUnknown = true
	result, err = EvaluatePolicy(report, policy)
	if err != nil || len(result.Findings) != 1 {
		t.Fatalf("unknown enum is outside selected families: %+v, %v", result, err)
	}
	policy.APIs[0].RequiredFamilies = nil
	result, err = EvaluatePolicy(report, policy)
	if err != nil || len(result.Findings) != 1 || result.Findings[0].Code != "measurement_unknown" {
		t.Fatalf("explicit unknown gate: %+v, %v", result, err)
	}
	after, _ := json.Marshal(report)
	if string(before) != string(after) {
		t.Fatal("policy evaluation modified coverage evidence")
	}
}

func TestPolicyDoesNotPoolCoverageAcrossSchemaRevisions(t *testing.T) {
	report := policyReport()
	other := *report.Contracts.Specs[0]
	other.Revision = "revision-b"
	other.Targets = []*ContractTarget{{Key: "get-b", Family: "operation", Operation: "GET /orders", State: "not_covered"}}
	report.Contracts.Specs = append(report.Contracts.Specs, &other)
	result, err := EvaluatePolicy(report, Policy{PolicyVersion: PolicyVersion, APIs: []APIPolicy{{APIID: "orders", Origin: "provided", RequiredOperations: []string{"GET /orders"}}}})
	if err != nil || result.Passed || len(result.Findings) != 1 || result.Findings[0].Revision != "revision-b" {
		t.Fatalf("coverage was pooled across revisions: %+v, %v", result, err)
	}
}

func TestPolicyReportsIncompleteUnknownAndMissingSelections(t *testing.T) {
	report := policyReport()
	report.Contracts.Complete = false
	report.Contracts.Specs[0].DenominatorKnown = false
	result, err := EvaluatePolicy(report, Policy{PolicyVersion: PolicyVersion, RequireComplete: true, APIs: []APIPolicy{
		{APIID: "orders", Origin: "provided", FailUnknown: true, RequiredOperations: []string{"DELETE /orders"}},
		{APIID: "missing", Origin: "dependency"},
	}})
	if err != nil || result.Passed {
		t.Fatalf("incomplete/unknown policy passed: %+v, %v", result, err)
	}
	codes := map[string]bool{}
	for _, finding := range result.Findings {
		codes[finding.Code] = true
	}
	for _, code := range []string{"capture_incomplete", "denominator_unknown", "measurement_unknown", "required_operation_missing", "api_missing"} {
		if !codes[code] {
			t.Fatalf("missing %s diagnostic: %+v", code, result.Findings)
		}
	}
}

func TestPolicyRejectsUnknownVersionsAndMalformedMeasurement(t *testing.T) {
	for _, mutation := range []func(*Report){
		func(r *Report) { r.ReportVersion = "2.0" },
		func(r *Report) { r.Contracts.RulesVersion = "future-rules" },
		func(r *Report) { r.Contracts.Specs[0].RulesVersion = "wiretap-protobuf-1" },
		func(r *Report) { r.Contracts.Specs[0].Targets[0].State = "maybe" },
		func(r *Report) {
			r.Contracts.Specs[0].Targets = append(r.Contracts.Specs[0].Targets, r.Contracts.Specs[0].Targets[0])
		},
	} {
		report := policyReport()
		mutation(report)
		if _, err := EvaluatePolicy(report, Policy{PolicyVersion: PolicyVersion}); err == nil {
			t.Fatal("accepted an incompatible measurement vocabulary")
		}
	}
	if _, err := EvaluatePolicy(policyReport(), Policy{PolicyVersion: "future"}); err == nil || !strings.Contains(err.Error(), "policy_version") {
		t.Fatalf("accepted unknown policy version: %v", err)
	}
}
