package coverage

import (
	"fmt"
	"sort"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
)

type RejectionCoverage struct {
	Scope      string                     `json:"scope" yaml:"scope"`
	Covered    int                        `json:"covered" yaml:"covered"`
	Failed     int                        `json:"failed" yaml:"failed"`
	NotCovered int                        `json:"not_covered" yaml:"not_covered"`
	Targets    []*RejectionCoverageTarget `json:"targets" yaml:"targets"`
}
type RejectionCoverageTarget struct {
	artifact.RejectionTarget `yaml:",inline"`
	State                    string             `json:"state" yaml:"state"`
	Evidence                 []ContractEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

func buildRejectionCoverage(run *bundle.Run, captures []*artifact.Artifact) *RejectionCoverage {
	out := &RejectionCoverage{Scope: "supported isolated invalid-input targets generated from the selected provider seed; not all invalid values or combinations"}
	targets := map[string]*RejectionCoverageTarget{}
	for _, a := range captures {
		if a.Exploration == nil {
			continue
		}
		for _, t := range a.Exploration.Catalogue {
			if targets[t.Key] == nil {
				targets[t.Key] = &RejectionCoverageTarget{RejectionTarget: t, State: "not_covered"}
			}
		}
	}
	for i, a := range captures {
		x := a.Exploration
		if x == nil || x.Mode != "rejection" || x.Target == nil {
			continue
		}
		t := targets[x.Target.Key]
		if t == nil {
			continue
		}
		for _, flow := range a.Traffic {
			if flow.ID != t.FlowID || flow.Channel != "test_to_sut" {
				continue
			}
			e := ContractEvidence{RunID: run.RunID, AttemptID: a.TestCase.ID, FlowID: flow.ID, Path: run.Attempts[i].Path, TestName: a.TestCase.Name, Outcome: a.TestCase.Status, Phase: "request", InstanceLocation: t.Location, Validation: "invalid", Result: "observed", CaptureIncomplete: a.CaptureIncomplete, TransportError: flow.TransportError}
			statusOK := false
			if flow.Response.HTTP != nil {
				for _, status := range t.ExpectedStatuses {
					if status == flow.Response.HTTP.Status && status >= 400 && status < 500 && status != 401 && status != 403 && status != 429 {
						statusOK = true
					}
				}
			}
			checked, responseChecked := false, false
			for _, check := range a.Assertions {
				if check.FlowID != flow.ID {
					continue
				}
				if check.ID == "expected-rejection" && check.Operator == "status_in" && check.Result == "pass" {
					checked = true
				}
				if check.ID == "response-schema-"+flow.ID && check.Operator == "schema" && check.Result == "pass" {
					responseChecked = true
				}
			}
			if x.RequestVerified && statusOK && checked && responseChecked && a.TestCase.Status == "pass" && !a.CaptureIncomplete && flow.TransportError == "" {
				t.State = "covered"
				e.Result = "covered"
				e.Reason = "Intended request violation verified before transmission; expected rejection and response contract checked"
			} else {
				if t.State != "covered" {
					t.State = "failed"
				}
				e.Reason = "Generated case did not satisfy rejection checks"
				if !x.RequestVerified {
					e.Validation = "unknown"
				}
				if flow.Response.HTTP != nil {
					e.Reason += fmt.Sprintf(" (HTTP %d)", flow.Response.HTTP.Status)
				}
			}
			t.Evidence = append(t.Evidence, e)
		}
	}
	for _, key := range sortedMapKeys(targets) {
		t := targets[key]
		out.Targets = append(out.Targets, t)
		switch t.State {
		case "covered":
			out.Covered++
		case "failed":
			out.Failed++
		default:
			out.NotCovered++
		}
	}
	sort.Slice(out.Targets, func(i, j int) bool {
		a, b := out.Targets[i], out.Targets[j]
		return a.Operation+a.Location+a.Constraint < b.Operation+b.Location+b.Constraint
	})
	if len(out.Targets) == 0 {
		return nil
	}
	return out
}
