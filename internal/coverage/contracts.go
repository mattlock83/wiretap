package coverage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

// ContractRulesVersion changes whenever target identity or measurement semantics change.
const ContractRulesVersion = "wiretap-contract-1"

// ProtobufRulesVersion keeps RPC measurement independent of HTTP identities.
const ProtobufRulesVersion = "wiretap-protobuf-2"

// ContractCoverage is a disposable measurement of one verified bundle snapshot.
// It deliberately makes no all-API, assertion, or provider-compatibility claim.
type ContractCoverage struct {
	Unmeasured       []UnmeasuredInteraction `json:"unmeasured_interactions,omitempty" yaml:"unmeasured_interactions,omitempty"`
	Rejections       *RejectionCoverage      `json:"rejection_coverage,omitempty" yaml:"rejection_coverage,omitempty"`
	RulesVersion     string                  `json:"rules_version" yaml:"rules_version"`
	RunID            string                  `json:"run_id" yaml:"run_id"`
	RunState         string                  `json:"run_state" yaml:"run_state"`
	SnapshotRevision uint64                  `json:"snapshot_revision" yaml:"snapshot_revision"`
	Complete         bool                    `json:"complete" yaml:"complete"`
	Scope            string                  `json:"scope" yaml:"scope"`
	Specs            []*ContractSpec         `json:"specs" yaml:"specs"`
}

type ContractSpec struct {
	RulesVersion     string                    `json:"rules_version" yaml:"rules_version"`
	APIID            string                    `json:"api_id" yaml:"api_id"`
	SchemaID         string                    `json:"schema_id" yaml:"schema_id"`
	Revision         string                    `json:"contract_revision" yaml:"contract_revision"`
	Origin           string                    `json:"origin" yaml:"origin"`
	TargetKind       string                    `json:"target_kind" yaml:"target_kind"`
	Dialect          string                    `json:"dialect,omitempty" yaml:"dialect,omitempty"`
	DenominatorKnown bool                      `json:"denominator_known" yaml:"denominator_known"`
	Notes            []string                  `json:"notes,omitempty" yaml:"notes,omitempty"`
	Targets          []*ContractTarget         `json:"targets" yaml:"targets"`
	Counts           map[string]ContractCounts `json:"counts" yaml:"counts"`
	Violations       []ContractViolation       `json:"violations,omitempty" yaml:"violations,omitempty"`
	Combinations     []ContractCombination     `json:"combinations,omitempty" yaml:"combinations,omitempty"`
}

type ContractCounts struct {
	Covered     int `json:"covered" yaml:"covered"`
	NotCovered  int `json:"not_covered" yaml:"not_covered"`
	Unknown     int `json:"unknown" yaml:"unknown"`
	Unsupported int `json:"unsupported" yaml:"unsupported"`
	Excluded    int `json:"excluded" yaml:"excluded"`
}

type ContractTarget struct {
	FieldPath    string             `json:"field_path,omitempty" yaml:"field_path,omitempty"`
	Key          string             `json:"key" yaml:"key"`
	OperationKey string             `json:"operation_key" yaml:"operation_key"`
	Operation    string             `json:"operation" yaml:"operation"`
	Phase        string             `json:"phase" yaml:"phase"`
	Location     string             `json:"schema_location" yaml:"schema_location"`
	Family       string             `json:"family" yaml:"family"`
	Condition    string             `json:"condition" yaml:"condition"`
	AppliesWhen  string             `json:"applies_when" yaml:"applies_when"`
	State        string             `json:"state" yaml:"state"`
	Feasibility  string             `json:"feasibility" yaml:"feasibility"`
	Reason       string             `json:"reason,omitempty" yaml:"reason,omitempty"`
	Evidence     []ContractEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	match        func(any, bool) bool
}

type ContractEvidence struct {
	MessageIndex      *int   `json:"message_index,omitempty" yaml:"message_index,omitempty"`
	CaptureIncomplete bool   `json:"capture_incomplete" yaml:"capture_incomplete"`
	TransportError    string `json:"transport_error,omitempty" yaml:"transport_error,omitempty"`
	Validation        string `json:"validation" yaml:"validation"`
	RunID             string `json:"run_id" yaml:"run_id"`
	AttemptID         string `json:"attempt_id" yaml:"attempt_id"`
	FlowID            string `json:"flow_id" yaml:"flow_id"`
	Path              string `json:"evidence_path" yaml:"evidence_path"`
	TestName          string `json:"test_name" yaml:"test_name"`
	Outcome           string `json:"outcome" yaml:"outcome"`
	Phase             string `json:"phase" yaml:"phase"`
	InstanceLocation  string `json:"instance_location" yaml:"instance_location"`
	Result            string `json:"result" yaml:"result"`
	Reason            string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

type ContractViolation struct {
	Expected       bool             `json:"expected,omitempty" yaml:"expected,omitempty"`
	Constraint     string           `json:"constraint,omitempty" yaml:"constraint,omitempty"`
	SchemaLocation string           `json:"schema_location,omitempty" yaml:"schema_location,omitempty"`
	Evidence       ContractEvidence `json:"evidence" yaml:"evidence"`
	Operation      string           `json:"operation" yaml:"operation"`
	Reason         string           `json:"reason" yaml:"reason"`
}

type ConditionHit struct {
	Key              string `json:"key" yaml:"key"`
	InstanceLocation string `json:"instance_location" yaml:"instance_location"`
}

type ContractCombination struct {
	Fingerprint  string           `json:"fingerprint" yaml:"fingerprint"`
	OperationKey string           `json:"operation_key" yaml:"operation_key"`
	Phase        string           `json:"phase" yaml:"phase"`
	Hits         []ConditionHit   `json:"hits" yaml:"hits"`
	Evidence     ContractEvidence `json:"evidence" yaml:"evidence"`
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

func buildContractCoverage(run *bundle.Run, captures []*artifact.Artifact) *ContractCoverage {
	out := &ContractCoverage{RulesVersion: ContractRulesVersion, RunID: run.RunID, RunState: run.RunState, SnapshotRevision: run.Revision, Complete: run.RunState == "complete" && len(run.Pending) == 0, Scope: "registered supplied contracts only; condition combinations are observations, not compatibility or assertion proof", Specs: []*ContractSpec{}}
	out.Rejections = buildRejectionCoverage(run, captures)
	out.Unmeasured = unmeasuredInteractions(captures)
	if len(out.Unmeasured) > 0 {
		out.Complete = false
	}
	refs := append([]artifact.SchemaRef{}, run.Schemas...)
	for _, boundary := range run.Contracts {
		if boundary.State == "resolved" {
			continue
		}
		out.Complete = false
		origin := "provided"
		kind := run.SUTTargetKind
		if boundary.Role == "dependency" {
			origin = "dependency"
			kind = run.DependencyKinds[boundary.APIID]
		}
		out.Specs = append(out.Specs, &ContractSpec{RulesVersion: ProtobufRulesVersion, APIID: boundary.APIID, SchemaID: boundary.APIID, Origin: origin, TargetKind: kind, Dialect: "protobuf", Notes: []string{"Contract resolution is " + boundary.State + "; declared API inventory and coverage denominator are unknown."}, Targets: []*ContractTarget{}, Counts: map[string]ContractCounts{}})
	}
	for _, a := range captures {
		refs = append(refs, a.Schemas...)
		if a.CaptureIncomplete {
			out.Complete = false
		}
	}
	type identityResult struct {
		revision string
		precise  bool
		err      error
	}
	identities := map[string]identityResult{}
	for _, ref := range refs {
		key := schemaKey(ref)
		if _, ok := identities[key]; ok || ref.Source.Type == "omitted" || ref.Type != "openapi" {
			continue
		}
		revision, precise, err := contractRevision(ref)
		identities[key] = identityResult{revision, precise, err}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.Role != "sut" && ref.Role != "provided" && ref.Role != "dependency" {
			continue
		}
		id, origin, kind := run.APIID, "provided", run.SUTTargetKind
		if ref.Role == "provided" || id == "" {
			id = ref.AssetID
		}
		if ref.Role == "dependency" {
			id, origin, kind = ref.AssetID, "dependency", run.DependencyKinds[ref.AssetID]
		}
		if kind == "" {
			kind = "unknown"
		}
		revision := schemaRevision(ref)
		canonical := identities[schemaKey(ref)]
		if canonical.err == nil && canonical.revision != "" {
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
		if ref.Type == "protobuf" {
			measureGRPCContract(spec, ref, run, captures, origin, id)
			finishContractSpec(spec)
			continue
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
		loaded, err := loadOpenAPISpec(ref)
		if err != nil {
			spec.Notes = append(spec.Notes, "Contract could not be evaluated: "+err.Error())
			continue
		}
		if canonical.err != nil {
			spec.Notes = append(spec.Notes, "Contract identity unavailable: "+canonical.err.Error())
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
