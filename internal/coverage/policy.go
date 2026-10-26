package coverage

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const PolicyVersion = "1.0"

// Policy opts into explicit gates. Families and operations are evaluated for
// each selected contract revision; coverage from different revisions is not pooled.
type Policy struct {
	PolicyVersion   string      `json:"policy_version"`
	RequireComplete bool        `json:"require_complete"`
	APIs            []APIPolicy `json:"apis"`
}

type APIPolicy struct {
	APIID              string   `json:"api_id"`
	Origin             string   `json:"origin"`
	RequiredOperations []string `json:"required_operations,omitempty"`
	RequiredFamilies   []string `json:"required_families,omitempty"`
	FailUnknown        bool     `json:"fail_unknown"`
}

type PolicyResult struct {
	PolicyVersion string          `json:"policy_version"`
	Passed        bool            `json:"passed"`
	Findings      []PolicyFinding `json:"findings"`
}

type PolicyFinding struct {
	APIID     string `json:"api_id,omitempty"`
	Origin    string `json:"origin,omitempty"`
	SchemaID  string `json:"schema_id,omitempty"`
	Revision  string `json:"contract_revision,omitempty"`
	Family    string `json:"family,omitempty"`
	Operation string `json:"operation,omitempty"`
	TargetKey string `json:"target_key,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

func EvaluatePolicy(report *Report, policy Policy) (*PolicyResult, error) {
	if err := validateMeasurement(report); err != nil {
		return nil, err
	}
	if policy.PolicyVersion != PolicyVersion {
		return nil, fmt.Errorf("unsupported policy_version %q; require %s", policy.PolicyVersion, PolicyVersion)
	}
	result := &PolicyResult{PolicyVersion: PolicyVersion, Passed: true, Findings: []PolicyFinding{}}
	if policy.RequireComplete && !report.Contracts.Complete {
		result.Findings = append(result.Findings, PolicyFinding{Code: "capture_incomplete", Message: "The recorded contract measurement is incomplete."})
	}
	for _, rule := range policy.APIs {
		if strings.TrimSpace(rule.APIID) == "" || rule.Origin != "provided" && rule.Origin != "dependency" {
			return nil, errors.New("policy APIs require api_id and origin provided or dependency")
		}
		for _, selector := range append(append([]string{}, rule.RequiredOperations...), rule.RequiredFamilies...) {
			if strings.TrimSpace(selector) == "" {
				return nil, errors.New("policy operation and family selectors must not be empty")
			}
		}
		matched := false
		for _, spec := range report.Contracts.Specs {
			if spec.APIID != rule.APIID || spec.Origin != rule.Origin {
				continue
			}
			matched = true
			add := func(code, message string, target *ContractTarget) {
				finding := PolicyFinding{APIID: spec.APIID, Origin: spec.Origin, SchemaID: spec.SchemaID, Revision: spec.Revision, Code: code, Message: message}
				if target != nil {
					finding.Family, finding.Operation, finding.TargetKey = target.Family, target.Operation, target.Key
				}
				result.Findings = append(result.Findings, finding)
			}
			if rule.FailUnknown && !spec.DenominatorKnown {
				add("denominator_unknown", "The selected API's contract denominator is unknown.", nil)
			}
			for _, operation := range uniqueSorted(rule.RequiredOperations) {
				var target *ContractTarget
				for _, candidate := range spec.Targets {
					if candidate.Family == "operation" && candidate.Operation == operation {
						target = candidate
						break
					}
				}
				if target == nil {
					add("required_operation_missing", "Required operation is absent from this contract revision: "+operation, nil)
					result.Findings[len(result.Findings)-1].Operation = operation
				} else if target.State != "covered" {
					add("required_operation_untested", "Required operation has no eligible coverage evidence.", target)
				}
			}
			families := map[string]bool{}
			for _, family := range rule.RequiredFamilies {
				families[family] = true
			}
			found := map[string]bool{}
			for _, target := range spec.Targets {
				selected := len(families) == 0 || families[target.Family]
				found[target.Family] = true
				if families[target.Family] && target.State == "not_covered" {
					add("required_family_uncovered", "Required contract condition has no eligible coverage evidence.", target)
				}
				if rule.FailUnknown && selected && (target.State == "unknown" || target.State == "unsupported") {
					add("measurement_unknown", "Selected contract condition is "+target.State+".", target)
				}
			}
			for _, family := range uniqueSorted(rule.RequiredFamilies) {
				if !found[family] {
					add("required_family_missing", "Required condition family is absent from this contract revision: "+family, nil)
					result.Findings[len(result.Findings)-1].Family = family
				}
			}
		}
		if !matched {
			result.Findings = append(result.Findings, PolicyFinding{APIID: rule.APIID, Origin: rule.Origin, Code: "api_missing", Message: "No measured contract matches the selected API and origin."})
		}
	}
	sort.SliceStable(result.Findings, func(i, j int) bool {
		a, b := result.Findings[i], result.Findings[j]
		return strings.Join([]string{a.Origin, a.APIID, a.SchemaID, a.Revision, a.Code, a.Family, a.Operation, a.TargetKey}, "\x00") < strings.Join([]string{b.Origin, b.APIID, b.SchemaID, b.Revision, b.Code, b.Family, b.Operation, b.TargetKey}, "\x00")
	})
	result.Passed = len(result.Findings) == 0
	return result, nil
}

// Validate only the identity and measurement vocabulary used by gates/diffs.
// Reports are derived inputs; this does not certify their original raw evidence.
func validateMeasurement(report *Report) error {
	if report == nil || report.ReportVersion != ReportVersion {
		return fmt.Errorf("coverage report_version must be %s", ReportVersion)
	}
	if report.Contracts == nil || report.Contracts.RulesVersion != ContractRulesVersion {
		return fmt.Errorf("coverage contract rules_version must be %s", ContractRulesVersion)
	}
	seen := map[string]bool{}
	for _, spec := range report.Contracts.Specs {
		if spec == nil || spec.APIID == "" || spec.Origin != "provided" && spec.Origin != "dependency" {
			return errors.New("contract specs require an API identity and provided/dependency origin")
		}
		if spec.RulesVersion != ContractRulesVersion {
			return fmt.Errorf("unsupported contract spec rules_version %q", spec.RulesVersion)
		}
		key := specIdentity(spec)
		if seen[key] {
			return errors.New("duplicate contract spec identity")
		}
		seen[key] = true
		targets := map[string]bool{}
		for _, target := range spec.Targets {
			if target == nil || target.Key == "" || target.Family == "" || targets[target.Key] {
				return errors.New("contract targets require distinct keys and nonempty families")
			}
			targets[target.Key] = true
			switch target.State {
			case "covered", "not_covered", "unknown", "unsupported", "excluded":
			default:
				return fmt.Errorf("unsupported contract target state %q", target.State)
			}
		}
	}
	return nil
}

func specIdentity(spec *ContractSpec) string {
	return strings.Join([]string{spec.Origin, spec.APIID, spec.SchemaID, spec.Revision, spec.RulesVersion}, "\x00")
}
