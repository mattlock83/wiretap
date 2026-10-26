package coverage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

// ContractRulesVersion changes whenever target identity or measurement semantics change.
const ContractRulesVersion = "wiretap-contract-1"

// ContractCoverage is a disposable measurement of one verified bundle snapshot.
// It deliberately makes no all-API, assertion, or provider-compatibility claim.
type ContractCoverage struct {
	RulesVersion     string          `json:"rules_version"`
	RunID            string          `json:"run_id"`
	RunState         string          `json:"run_state"`
	SnapshotRevision uint64          `json:"snapshot_revision"`
	Complete         bool            `json:"complete"`
	Scope            string          `json:"scope"`
	Specs            []*ContractSpec `json:"specs"`
}

type ContractSpec struct {
	RulesVersion     string                    `json:"rules_version"`
	APIID            string                    `json:"api_id"`
	SchemaID         string                    `json:"schema_id"`
	Revision         string                    `json:"contract_revision"`
	Origin           string                    `json:"origin"`
	TargetKind       string                    `json:"target_kind"`
	Dialect          string                    `json:"dialect,omitempty"`
	DenominatorKnown bool                      `json:"denominator_known"`
	Notes            []string                  `json:"notes,omitempty"`
	Targets          []*ContractTarget         `json:"targets"`
	Counts           map[string]ContractCounts `json:"counts"`
	Violations       []ContractViolation       `json:"violations,omitempty"`
}

type ContractCounts struct {
	Covered     int `json:"covered"`
	NotCovered  int `json:"not_covered"`
	Unknown     int `json:"unknown"`
	Unsupported int `json:"unsupported"`
	Excluded    int `json:"excluded"`
}

type ContractTarget struct {
	FieldPath    string             `json:"field_path,omitempty"`
	Key          string             `json:"key"`
	OperationKey string             `json:"operation_key"`
	Operation    string             `json:"operation"`
	Phase        string             `json:"phase"`
	Location     string             `json:"schema_location"`
	Family       string             `json:"family"`
	Condition    string             `json:"condition"`
	AppliesWhen  string             `json:"applies_when"`
	State        string             `json:"state"`
	Feasibility  string             `json:"feasibility"`
	Reason       string             `json:"reason,omitempty"`
	Evidence     []ContractEvidence `json:"evidence,omitempty"`
	match        func(any, bool) bool
}

type ContractEvidence struct {
	CaptureIncomplete bool   `json:"capture_incomplete"`
	TransportError    string `json:"transport_error,omitempty"`
	Validation        string `json:"validation"`
	RunID             string `json:"run_id"`
	AttemptID         string `json:"attempt_id"`
	FlowID            string `json:"flow_id"`
	Path              string `json:"evidence_path"`
	TestName          string `json:"test_name"`
	Outcome           string `json:"outcome"`
	Phase             string `json:"phase"`
	InstanceLocation  string `json:"instance_location"`
	Result            string `json:"result"`
	Reason            string `json:"reason,omitempty"`
}

type ContractViolation struct {
	Constraint     string           `json:"constraint,omitempty"`
	SchemaLocation string           `json:"schema_location,omitempty"`
	Evidence       ContractEvidence `json:"evidence"`
	Operation      string           `json:"operation"`
	Reason         string           `json:"reason"`
}

type contractBuilder struct {
	spec             *ContractSpec
	targets          map[string]*ContractTarget
	operation, phase string
}

func contractDigest(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	} // All callers use internal, JSON-safe descriptors.
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (b *contractBuilder) add(location, family, condition string, match func(any, bool) bool) *ContractTarget {
	opKey := contractDigest([]string{ContractRulesVersion, "http", b.spec.APIID, b.operation})
	key := contractDigest([]string{ContractRulesVersion, opKey, b.spec.Revision, b.phase, location, family, condition})
	if t := b.targets[key]; t != nil {
		return t
	}
	t := &ContractTarget{Key: key, OperationKey: opKey, Operation: b.operation, Phase: b.phase, Location: location, Family: family, Condition: condition, State: "not_covered", Feasibility: "known", AppliesWhen: "selected message alternative; parent present and non-null; matching schema branches", match: match}
	b.targets[key] = t
	b.spec.Targets = append(b.spec.Targets, t)
	return t
}

func (b *contractBuilder) unsupported(location, reason string) *ContractTarget {
	t := b.add(location, "unsupported", reason, nil)
	t.State = "unsupported"
	t.Feasibility = "unknown"
	t.Reason = reason
	return t
}

func (t *ContractTarget) observe(e ContractEvidence) {
	t.Evidence = append(t.Evidence, e)
	if t.State == "unsupported" || t.State == "excluded" {
		return
	}
	if e.Result == "covered" {
		t.State = "covered"
		t.Feasibility = "known"
		t.Reason = ""
	} else if e.Result == "unknown" && t.State != "covered" {
		t.State = "unknown"
		t.Reason = e.Reason
	}
}

func buildContractCoverage(run *bundle.Run, captures []*artifact.Artifact, cache contractCache) *ContractCoverage {
	out := &ContractCoverage{RulesVersion: ContractRulesVersion, RunID: run.RunID, RunState: run.RunState, SnapshotRevision: run.Revision, Complete: run.RunState == "complete" && len(run.Pending) == 0, Scope: "registered supplied contracts only; observed conditions are not compatibility or assertion proof", Specs: []*ContractSpec{}}
	refs := append([]artifact.SchemaRef{}, run.Schemas...)
	for _, a := range captures {
		refs = append(refs, a.Schemas...)
		if a.CaptureIncomplete {
			out.Complete = false
		}
	}
	identities := map[string]contractPreparation{}
	for _, ref := range refs {
		if ref.Source.Type != "omitted" && ref.Type == "openapi" {
			identities[schemaKey(ref)] = cache.load(ref)
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		id, origin, kind, ok := contractBoundary(run, ref)
		if !ok {
			continue
		}
		revision := schemaRevision(ref)
		canonical := identities[schemaKey(ref)]
		if canonical.identityErr == nil && canonical.revision != "" {
			revision = canonical.revision
		}
		identity := contractDigest([]string{id, origin, ref.ID, revision, ref.Type})
		if seen[identity] {
			continue
		}
		seen[identity] = true
		spec := &ContractSpec{RulesVersion: ContractRulesVersion, APIID: id, SchemaID: ref.ID, Revision: revision, Origin: origin, TargetKind: kind, Targets: []*ContractTarget{}, Counts: map[string]ContractCounts{}}
		out.Specs = append(out.Specs, spec)
		if id == "" {
			spec.Notes = append(spec.Notes, "API identity is missing; do not compare these keys across repositories")
		}
		if ref.Source.Type == "omitted" || ref.Type != "openapi" {
			if ref.Source.Type == "omitted" {
				spec.Notes = append(spec.Notes, "No supplied contract; denominator unknown. Inferred drafts are not coverage inputs.")
			} else {
				spec.Notes = append(spec.Notes, fmt.Sprintf("Contract type %q has no coverage adapter; denominator unknown. The frozen contract is retained for inspection.", ref.Type))
				out.Complete = false
			}
			continue
		}
		loaded, err := canonical.spec, canonical.loadErr
		if err != nil {
			spec.Notes = append(spec.Notes, "Contract could not be evaluated: "+err.Error())
			continue
		}
		if canonical.identityErr != nil {
			spec.Notes = append(spec.Notes, "Contract identity unavailable: "+canonical.identityErr.Error())
			continue
		}
		spec.Dialect = loaded.Doc.OpenAPI
		b := &contractBuilder{spec: spec, targets: map[string]*ContractTarget{}}
		if !strings.HasPrefix(spec.Dialect, "3.0.") {
			b.unsupported("/openapi", "Condition measurement currently supports OpenAPI 3.0 only")
			finishContractSpec(spec)
			continue
		}
		if !canonical.precise {
			b.unsupported("/", "Contract numbers exceed the validator's reliable precision")
			// Preserve the declared API inventory without deriving numeric
			// predicates from values that the validator has already rounded.
			for _, operation := range openapiOperations(loaded) {
				method, path, _ := strings.Cut(operation, " ")
				b.operation, b.phase = operation, "operation"
				target := b.add("/paths/"+pointerToken(path)+"/"+strings.ToLower(method), "operation", "exercised", nil)
				uncertain(target, "Contract precision prevents conclusive validation")
			}
			finishContractSpec(spec)
			continue
		}
		plans := b.httpPlans(loaded)
		for i, a := range captures {
			applicable := false
			for _, ar := range a.Schemas {
				if ar.Role == ref.Role && ar.ID == ref.ID && ar.AssetID == ref.AssetID && identities[schemaKey(ar)].revision == revision {
					applicable = true
					break
				}
			}
			if !applicable {
				continue
			}
			for _, f := range a.Traffic {
				if !flowMatchesSchemaBoundary(f, ref) {
					continue
				}
				if (origin == "provided" && f.Channel != "test_to_sut") || (origin == "dependency" && (f.Channel != "sut_to_dependency" || f.AssetID != id)) {
					continue
				}
				e := ContractEvidence{RunID: run.RunID, AttemptID: a.TestCase.ID, FlowID: f.ID, Path: run.Attempts[i].Path, TestName: a.TestCase.Name, Outcome: a.TestCase.Status, CaptureIncomplete: a.CaptureIncomplete, TransportError: f.TransportError}
				b.measureHTTP(loaded, plans, a, f, e)
			}
		}
		finishContractSpec(spec)
	}
	sort.Slice(out.Specs, func(i, j int) bool {
		a, b := out.Specs[i], out.Specs[j]
		return a.Origin+"|"+a.APIID+"|"+a.SchemaID+"|"+a.Revision < b.Origin+"|"+b.APIID+"|"+b.SchemaID+"|"+b.Revision
	})
	return out
}

func finishContractSpec(s *ContractSpec) {
	s.DenominatorKnown = len(s.Targets) > 0
	sort.Slice(s.Targets, func(i, j int) bool {
		a, b := s.Targets[i], s.Targets[j]
		return strings.Join([]string{a.Operation, a.Phase, a.Location, a.Family, a.Condition}, "\x00") < strings.Join([]string{b.Operation, b.Phase, b.Location, b.Family, b.Condition}, "\x00")
	})
	for _, t := range s.Targets {
		if t.Feasibility == "unknown" || t.State == "unsupported" {
			s.DenominatorKnown = false
		}
		for _, group := range []string{"phase:" + t.Phase, "family:" + t.Family} {
			c := s.Counts[group]
			switch t.State {
			case "covered":
				c.Covered++
			case "not_covered":
				c.NotCovered++
			case "unknown":
				c.Unknown++
			case "unsupported":
				c.Unsupported++
			case "excluded":
				c.Excluded++
			}
			s.Counts[group] = c
		}
	}
}
