package coverage

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/version"
)

type TargetSnapshot struct {
	APIID        string `json:"api_id"`
	Origin       string `json:"origin"`
	SchemaID     string `json:"schema_id"`
	Revision     string `json:"contract_revision"`
	RulesVersion string `json:"rules_version"`
	TargetKey    string `json:"target_key"`
	Operation    string `json:"operation"`
	Family       string `json:"family"`
	Phase        string `json:"phase"`
	State        string `json:"state"`
}

type CoverageChange struct {
	Before TargetSnapshot `json:"before"`
	After  TargetSnapshot `json:"after"`
}

type SchemaSnapshot struct {
	SchemaID     string `json:"schema_id"`
	Revision     string `json:"contract_revision"`
	RulesVersion string `json:"rules_version"`
	Dialect      string `json:"dialect,omitempty"`
}

type SchemaChange struct {
	APIID  string           `json:"api_id"`
	Origin string           `json:"origin"`
	Before []SchemaSnapshot `json:"before"`
	After  []SchemaSnapshot `json:"after"`
}

type ReportDiff struct {
	ReportVersion  string           `json:"report_version"`
	Producer       version.Producer `json:"producer"`
	RulesVersion   string           `json:"rules_version"`
	BaselineRunID  string           `json:"baseline_run_id"`
	CurrentRunID   string           `json:"current_run_id"`
	LostCoverage   []CoverageChange `json:"lost_coverage"`
	GainedCoverage []CoverageChange `json:"gained_coverage"`
	AddedTargets   []TargetSnapshot `json:"added_targets"`
	RemovedTargets []TargetSnapshot `json:"removed_targets"`
	SchemaChanges  []SchemaChange   `json:"schema_changes"`
	Notes          []string         `json:"notes"`
}

// CompareReports compares identical measurement identities, never measurements
// from changed schemas/rules. Schema replacements remain explicit additions and
// removals; this report does not claim they are breaking compatibility changes.
func CompareReports(baseline, current *Report) (*ReportDiff, error) {
	if err := validateMeasurement(baseline); err != nil {
		return nil, err
	}
	if err := validateMeasurement(current); err != nil {
		return nil, err
	}
	diff := &ReportDiff{ReportVersion: ReportVersion, Producer: version.Current(), RulesVersion: ContractRulesVersion,
		BaselineRunID: baseline.Contracts.RunID, CurrentRunID: current.Contracts.RunID,
		LostCoverage: []CoverageChange{}, GainedCoverage: []CoverageChange{},
		AddedTargets: []TargetSnapshot{}, RemovedTargets: []TargetSnapshot{}, SchemaChanges: []SchemaChange{},
		Notes: []string{"Changes describe recorded coverage observations, not breaking-change or provider-compatibility proof.", "Targets from changed schema revisions are added/removed; their coverage states are not compared across revisions."}}
	before, oldSchemas := reportTargets(baseline)
	after, newSchemas := reportTargets(current)
	for _, key := range mapKeys(before) {
		old := before[key]
		if next, ok := after[key]; !ok {
			diff.RemovedTargets = append(diff.RemovedTargets, old)
		} else if old.State == "covered" && next.State != "covered" {
			diff.LostCoverage = append(diff.LostCoverage, CoverageChange{old, next})
		} else if old.State != "covered" && next.State == "covered" {
			diff.GainedCoverage = append(diff.GainedCoverage, CoverageChange{old, next})
		}
	}
	for _, key := range mapKeys(after) {
		if _, ok := before[key]; !ok {
			diff.AddedTargets = append(diff.AddedTargets, after[key])
		}
	}
	keys := map[string]bool{}
	for key := range oldSchemas {
		keys[key] = true
	}
	for key := range newSchemas {
		keys[key] = true
	}
	for _, key := range mapKeys(keys) {
		old, next := oldSchemas[key], newSchemas[key]
		if old == nil {
			old = []SchemaSnapshot{}
		}
		if next == nil {
			next = []SchemaSnapshot{}
		}
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(next)
		if string(a) != string(b) {
			parts := strings.SplitN(key, "\x00", 2)
			diff.SchemaChanges = append(diff.SchemaChanges, SchemaChange{APIID: parts[1], Origin: parts[0], Before: old, After: next})
		}
	}
	return diff, nil
}

func reportTargets(report *Report) (map[string]TargetSnapshot, map[string][]SchemaSnapshot) {
	targets := map[string]TargetSnapshot{}
	schemas := map[string][]SchemaSnapshot{}
	for _, spec := range report.Contracts.Specs {
		api := spec.Origin + "\x00" + spec.APIID
		schemas[api] = append(schemas[api], SchemaSnapshot{spec.SchemaID, spec.Revision, spec.RulesVersion, spec.Dialect})
		for _, target := range spec.Targets {
			targets[specIdentity(spec)+"\x00"+target.Key] = TargetSnapshot{APIID: spec.APIID, Origin: spec.Origin, SchemaID: spec.SchemaID, Revision: spec.Revision, RulesVersion: spec.RulesVersion, TargetKey: target.Key, Operation: target.Operation, Family: target.Family, Phase: target.Phase, State: target.State}
		}
	}
	for _, values := range schemas {
		sort.Slice(values, func(i, j int) bool {
			a, _ := json.Marshal(values[i])
			b, _ := json.Marshal(values[j])
			return string(a) < string(b)
		})
	}
	return targets, schemas
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
