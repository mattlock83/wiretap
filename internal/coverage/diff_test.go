package coverage

import "testing"

func TestCompareReportsDistinguishesLossGainAndInventoryChanges(t *testing.T) {
	before, after := policyReport(), policyReport()
	after.Contracts.RunID = "current"
	after.Contracts.Specs[0].Targets[0].State = "unknown"
	after.Contracts.Specs[0].Targets[1].State = "covered"
	after.Contracts.Specs[0].Targets[2] = &ContractTarget{Key: "added", Family: "presence", Operation: "GET /orders", State: "not_covered"}
	diff, err := CompareReports(before, after)
	if err != nil || len(diff.LostCoverage) != 1 || len(diff.GainedCoverage) != 1 || len(diff.AddedTargets) != 1 || len(diff.RemovedTargets) != 1 || len(diff.SchemaChanges) != 0 {
		t.Fatalf("wrong coverage diff: %+v, %v", diff, err)
	}
	if diff.LostCoverage[0].Before.TargetKey != "get" || diff.LostCoverage[0].After.State != "unknown" || diff.AddedTargets[0].TargetKey != "added" || diff.RemovedTargets[0].TargetKey != "enum" {
		t.Fatalf("wrong target identities in diff: %+v", diff)
	}
	if diff.Producer.Name != "wiretap" || diff.LostCoverage[0].After.RulesVersion != ContractRulesVersion {
		t.Fatalf("diff omitted producer or per-target rules provenance: %+v", diff)
	}
}

func TestCompareReportsDoesNotInferLossAcrossSchemaChanges(t *testing.T) {
	before, after := policyReport(), policyReport()
	after.Contracts.Specs[0].Revision = "revision-b"
	after.Contracts.Specs[0].Targets[0].State = "not_covered"
	diff, err := CompareReports(before, after)
	if err != nil || len(diff.LostCoverage) != 0 || len(diff.SchemaChanges) != 1 || len(diff.AddedTargets) != 3 || len(diff.RemovedTargets) != 3 {
		t.Fatalf("schema revisions were treated as comparable coverage: %+v, %v", diff, err)
	}
	change := diff.SchemaChanges[0]
	if change.Before[0].Revision != "revision-a" || change.After[0].Revision != "revision-b" || len(diff.Notes) == 0 {
		t.Fatalf("schema context or limits missing: %+v", diff)
	}
}

func TestCompareReportsRejectsIncompatibleRules(t *testing.T) {
	for _, mutate := range []func(*Report){
		func(r *Report) { r.ReportVersion = "2" },
		func(r *Report) { r.Contracts = nil },
		func(r *Report) { r.Contracts.RulesVersion = "next" },
		func(r *Report) { r.Contracts.Specs[0].RulesVersion = "old" },
	} {
		current := policyReport()
		mutate(current)
		if _, err := CompareReports(policyReport(), current); err == nil {
			t.Fatal("compared incompatible report/rule versions")
		}
	}
}
