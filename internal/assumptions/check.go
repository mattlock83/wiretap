// Package assumptions checks consumer mock examples and exercised contract rules
// against provider tests.
package assumptions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
)

const RulesVersion = "wiretap-assumptions-3"

// ErrInput identifies invalid, corrupt or inconsistent comparison inputs.
var ErrInput = errors.New("invalid assumption-check input")

type Options struct {
	Mode, Consumer, Provider, Dependency                    string
	ConsumerCollections, ProviderCollections, IgnoreHeaders []string
}

type Source struct {
	BundlePath       string   `json:"bundle_path"`
	RunID            string   `json:"run_id"`
	SnapshotRevision uint64   `json:"snapshot_revision"`
	RunState         string   `json:"run_state"`
	ApplicationID    string   `json:"application_id,omitempty"`
	APIID            string   `json:"api_id,omitempty"`
	ManifestSHA256   string   `json:"manifest_sha256"`
	Collections      []string `json:"collections"`
}

type Evidence struct {
	RunID        string                 `json:"run_id"`
	AttemptID    string                 `json:"attempt_id"`
	TestName     string                 `json:"test_name"`
	TestIdentity *artifact.TestIdentity `json:"test_identity,omitempty"`
	FlowID       string                 `json:"flow_id"`
	EvidencePath string                 `json:"evidence_path"`
	TestOutcome  string                 `json:"test_outcome"`
}

type Finding struct {
	Kind        string     `json:"kind"`
	Code        string     `json:"code"`
	Operation   string     `json:"operation,omitempty"`
	Message     string     `json:"message"`
	Consumer    *Evidence  `json:"consumer,omitempty"`
	Provider    []Evidence `json:"provider,omitempty"`
	Differences []string   `json:"differences,omitempty"`
}

type Match struct {
	Consumer      Evidence `json:"consumer"`
	Provider      Evidence `json:"provider"`
	ContractCheck string   `json:"contract_check"`
}

type ExcludedEvidence struct {
	Evidence Evidence `json:"evidence"`
	Reason   string   `json:"reason"`
}

type Counts struct {
	Assumptions int `json:"assumptions"`
	Verified    int `json:"verified"`
	Uncovered   int `json:"uncovered"`
	Mismatched  int `json:"mismatched"`
	Unverified  int `json:"unverified"`
}

type Report struct {
	ReportVersion            string             `json:"report_version"`
	RulesVersion             string             `json:"rules_version"`
	Mode                     string             `json:"mode"`
	Scope                    string             `json:"scope"`
	Compatible               bool               `json:"compatible"`
	Consumer                 Source             `json:"consumer"`
	Provider                 Source             `json:"provider"`
	Dependency               string             `json:"dependency"`
	Binding                  string             `json:"binding"`
	IgnoredHeaders           []string           `json:"ignored_headers"`
	Counts                   Counts             `json:"counts"`
	Matches                  []Match            `json:"matches"`
	Findings                 []Finding          `json:"findings"`
	ExcludedProviderEvidence []ExcludedEvidence `json:"excluded_provider_evidence"`
}

type snapshot struct {
	root, digest string
	run          *bundle.Run
	captures     []*artifact.Artifact
}

type sample struct {
	evidence                     Evidence
	flow                         artifact.Flow
	operation, request, response string
	problem                      string
	schema                       *artifact.SchemaRef
	schemaProblem                string
}

// Check verifies the same consumer-to-provider obligation from either viewpoint.
// Extra provider examples are not obligations for consumers. No requests run.
func Check(ctx context.Context, options Options) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Mode != "consumer" && options.Mode != "provider" {
		return nil, fmt.Errorf("%w: check requires consumer or provider", ErrInput)
	}
	consumer, err := loadSnapshot(options.Consumer)
	if err != nil {
		return nil, fmt.Errorf("%w: consumer: %v", ErrInput, err)
	}
	provider, err := loadSnapshot(options.Provider)
	if err != nil {
		return nil, fmt.Errorf("%w: provider: %v", ErrInput, err)
	}
	if consumer.root == provider.root || consumer.run.RunID == provider.run.RunID {
		return nil, fmt.Errorf("%w: consumer and provider must be different captures", ErrInput)
	}
	dependency, err := selectDependency(consumer.run, provider.run, options.Dependency)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInput, err)
	}
	ignored := append([]string{}, IgnoredHeaders...)
	for _, name := range options.IgnoreHeaders {
		name = strings.ToLower(strings.TrimSpace(name))
		if !headerName(name) {
			return nil, fmt.Errorf("%w: invalid ignored header %q", ErrInput, name)
		}
		ignored = append(ignored, name)
	}
	sort.Strings(ignored)
	ignored = unique(ignored)
	report := &Report{ReportVersion: "1.0", RulesVersion: RulesVersion, Mode: options.Mode,
		Scope: "observed_mock_examples_and_exercised_contracts", Dependency: dependency, Binding: "explicit_inputs",
		Consumer: consumer.source(options.ConsumerCollections), Provider: provider.source(options.ProviderCollections),
		IgnoredHeaders: ignored, Matches: []Match{}, Findings: []Finding{}, ExcludedProviderEvidence: []ExcludedEvidence{}}
	for _, entry := range []struct {
		role     string
		snapshot *snapshot
	}{{"consumer", consumer}, {"provider", provider}} {
		if entry.snapshot.run.RunState != "complete" || len(entry.snapshot.run.Pending) > 0 {
			report.add(Finding{Kind: "unverified", Code: entry.role + "_run_incomplete", Message: entry.role + " run must be complete with no pending test attempts"})
		}
	}
	if provider.run.SUTTargetKind != "service" {
		report.add(Finding{Kind: "unverified", Code: "provider_not_service", Message: "provider evidence must capture a SUT declared as kind: service"})
	}
	if mapping, exists := consumer.run.ProviderMappings[dependency]; exists {
		report.Binding = "declared_mapping"
		if mapping.ApplicationID != report.Provider.ApplicationID || mapping.APIID != report.Provider.APIID {
			report.add(Finding{Kind: "unverified", Code: "provider_identity_mismatch", Message: fmt.Sprintf("dependency %q maps to application %q API %q, but supplied provider is application %q API %q", dependency, mapping.ApplicationID, mapping.APIID, report.Provider.ApplicationID, report.Provider.APIID)})
		}
		if mapping.Version != "" {
			version, _ := provider.run.SUTSource["version"].(string)
			if version != mapping.Version {
				report.add(Finding{Kind: "unverified", Code: "provider_version_unverified", Message: fmt.Sprintf("mapped provider version %q must match provider sut_source.version (recorded %q)", mapping.Version, version)})
			}
		}
	}
	consumers := consumer.samples("sut_to_dependency", dependency)
	providers := provider.samples("test_to_sut", "")
	report.Counts.Assumptions = len(consumers)
	if len(consumers) == 0 {
		report.add(Finding{Kind: "unverified", Code: "no_consumer_assumptions", Message: "selected mock dependency has no recorded exchanges; an empty comparison cannot establish compatibility"})
	}
	// An interrupted run or wrong provider identity must never be certified by
	// whatever partial examples happen to remain available.
	if len(report.Findings) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return report, nil
	}
	index := map[string][]sample{}
	byOperation := map[string][]Evidence{}
	var wire wireVerifier
	var httpContracts httpContractVerifier
	for _, candidate := range providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		problem := evidenceProblem(provider, candidate)
		if problem == "" {
			candidate.request, candidate.response, err = normalizeFlow(candidate.flow, options.IgnoreHeaders)
			if err != nil {
				problem = err.Error()
			}
		}
		if problem != "" {
			report.ExcludedProviderEvidence = append(report.ExcludedProviderEvidence, ExcludedEvidence{candidate.evidence, problem})
			continue
		}
		index[candidate.request] = append(index[candidate.request], candidate)
		byOperation[candidate.operation] = append(byOperation[candidate.operation], candidate.evidence)
	}
	unavailableConsumers := 0
	for _, assumption := range consumers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		problem := evidenceProblem(consumer, assumption)
		if problem == "" {
			assumption.request, assumption.response, err = normalizeFlow(assumption.flow, options.IgnoreHeaders)
			if err != nil {
				problem = err.Error()
			}
		}
		if problem != "" {
			unavailableConsumers++
			report.add(Finding{Kind: "unverified", Code: "consumer_evidence_unavailable", Operation: assumption.operation, Message: problem, Consumer: &assumption.evidence})
			continue
		}
		candidates := index[assumption.request]
		if len(candidates) == 0 {
			report.add(Finding{Kind: "uncovered", Code: "provider_request_uncovered", Operation: assumption.operation, Message: "no complete passing provider test covers this concrete request scenario", Consumer: &assumption.evidence, Provider: limitedEvidence(byOperation[assumption.operation])})
			continue
		}
		matched := false
		wireProblem := ""
		var contractProblem *contractCheckProblem
		for _, candidate := range candidates {
			if candidate.response == assumption.response {
				contractCheck := "unavailable"
				if assumption.flow.Protocol == "grpc" {
					if err := wire.verify(assumption.flow.Request.GRPC, candidate.flow.Request.GRPC, candidate.schema, false); err != nil {
						wireProblem = "consumer request wire is incompatible with the provider contract: " + err.Error()
						continue
					}
					if err := wire.verify(candidate.flow.Response.GRPC, assumption.flow.Response.GRPC, assumption.schema, true); err != nil {
						wireProblem = "provider response wire is incompatible with the consumer contract: " + err.Error()
						continue
					}
					differences, err := wire.rulesMatch(assumption, candidate)
					if err != nil {
						contractProblem = &contractCheckProblem{"unverified", "protobuf_contract_unavailable", "protobuf declarations cannot be compared reliably: " + err.Error(), []string{"protobuf/contract"}}
						continue
					}
					if len(differences) > 0 {
						contractProblem = &contractCheckProblem{"mismatch", "protobuf_contract_mismatch", "consumer and provider protobuf declarations differ for this exercised method, including its reachable message fields and enum values", differences}
						continue
					}
					contractCheck = "protobuf_rules_and_observed_wire"
				} else if assumption.flow.Protocol == "http" {
					var problem *contractCheckProblem
					contractCheck, problem = verifyHTTPContracts(ctx, &httpContracts, assumption, candidate)
					if problem != nil {
						contractProblem = problem
						continue
					}
				}
				report.Matches = append(report.Matches, Match{assumption.evidence, candidate.evidence, contractCheck})
				report.Counts.Verified++
				matched = true
				break
			}
		}
		if !matched {
			refs := []Evidence{}
			for _, candidate := range candidates {
				refs = append(refs, candidate.evidence)
			}
			finding := Finding{Kind: "mismatch", Code: "provider_response_mismatch", Operation: assumption.operation, Message: "provider tests cover the request, but none returns the mocked response", Consumer: &assumption.evidence, Provider: limitedEvidence(refs), Differences: differencePaths(assumption.response, candidates[0].response)}
			if wireProblem != "" {
				finding.Code = "provider_wire_mismatch"
				finding.Message = wireProblem
				finding.Differences = []string{"protobuf/wire"}
			} else if contractProblem != nil {
				finding.Kind, finding.Code, finding.Message = contractProblem.kind, contractProblem.code, contractProblem.message
				finding.Differences = contractProblem.differences
			}
			report.add(finding)
		}
	}
	// Entirely unavailable consumer evidence may not have an exportable
	// collection at all. Preserve its useful unverified verdict instead of
	// replacing it with a secondary missing-projection error.
	if unavailableConsumers == len(consumers) && len(consumers) > 0 {
		return report, nil
	}
	// Verify the complete selected Postman projections, including examples in
	// failed attempts. They are inputs to inspect, never substitute proof.
	for _, entry := range []struct {
		role     string
		snapshot *snapshot
		inputs   []string
		boundary string
	}{
		{"consumer", consumer, options.ConsumerCollections, dependency}, {"provider", provider, options.ProviderCollections, ""},
	} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := entry.snapshot.verifyCollections(entry.inputs, entry.boundary); err != nil {
			return nil, fmt.Errorf("%w: %s collections: %v", ErrInput, entry.role, err)
		}
	}
	report.Compatible = len(report.Findings) == 0 && report.Counts.Verified > 0
	return report, nil
}

func (r *Report) add(f Finding) {
	r.Findings = append(r.Findings, f)
	switch f.Kind {
	case "uncovered":
		r.Counts.Uncovered++
	case "mismatch":
		r.Counts.Mismatched++
	default:
		r.Counts.Unverified++
	}
}

func loadSnapshot(input string) (*snapshot, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("capture path is required")
	}
	root, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if filepath.Base(root) != "bundle.json" {
			return nil, errors.New("use the run directory or its bundle.json manifest; retain the complete archive")
		}
		root = filepath.Dir(root)
	}
	digest, err := manifestDigest(root)
	if err != nil {
		return nil, err
	}
	run, captures, err := bundle.Load(root)
	if err != nil {
		return nil, err
	}
	s := &snapshot{root: root, digest: digest, run: run, captures: captures}
	if err := s.unchanged(); err != nil {
		return nil, err
	}
	return s, nil
}

func manifestDigest(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "bundle.json"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (64<<20)+1))
	if err != nil {
		return "", err
	}
	if n > 64<<20 {
		return "", errors.New("run manifest exceeds 64 MiB")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *snapshot) unchanged() error {
	digest, err := manifestDigest(s.root)
	if err != nil {
		return err
	}
	if digest != s.digest {
		return errors.New("capture manifest changed during comparison; use a stable completed archive")
	}
	return nil
}

func (s *snapshot) source(inputs []string) Source {
	if len(inputs) == 0 {
		inputs = []string{filepath.Join(s.root, "exports", "postman")}
	}
	paths := make([]string, 0, len(inputs))
	for _, path := range inputs {
		absolute, err := filepath.Abs(path)
		if err == nil {
			path = absolute
		}
		paths = append(paths, path)
	}
	appID := ""
	if s.run.Application != nil {
		appID = s.run.Application.ID
	}
	return Source{BundlePath: s.root, RunID: s.run.RunID, SnapshotRevision: s.run.Revision, RunState: s.run.RunState, ApplicationID: appID, APIID: providerAPI(s.run), ManifestSHA256: s.digest, Collections: paths}
}

func providerAPI(run *bundle.Run) string {
	if run.APIID != "" {
		return run.APIID
	}
	if len(run.ProvidedAPIs) == 1 {
		return run.ProvidedAPIs[0].ID
	}
	return ""
}

func selectDependency(consumer, provider *bundle.Run, explicit string) (string, error) {
	if explicit != "" {
		if consumer.DependencyKinds[explicit] != "mock" {
			return "", fmt.Errorf("dependency %q must be a declared mock in the consumer capture", explicit)
		}
		return explicit, nil
	}
	var all, mapped []string
	for id, kind := range consumer.DependencyKinds {
		if kind != "mock" {
			continue
		}
		all = append(all, id)
		if m, ok := consumer.ProviderMappings[id]; ok && provider.Application != nil && m.ApplicationID == provider.Application.ID && m.APIID == providerAPI(provider) {
			mapped = append(mapped, id)
		}
	}
	if len(mapped) == 1 {
		return mapped[0], nil
	}
	if len(all) == 1 {
		return all[0], nil
	}
	return "", errors.New("--dependency is required unless exactly one mock or provider mapping identifies the boundary")
}

func (s *snapshot) samples(channel, dependency string) []sample {
	refs := map[string]string{}
	for _, ref := range s.run.Attempts {
		refs[ref.AttemptID] = ref.Path
	}
	var out []sample
	for _, capture := range s.captures {
		problem := ""
		if capture.TestCase.Status != "pass" {
			problem = "test outcome must be pass, not " + strconv.Quote(capture.TestCase.Status)
		}
		if capture.CaptureIncomplete {
			problem = "test capture is incomplete"
		}
		if len(capture.Redactions) > 0 {
			problem = "legacy redaction rules make original evidence unavailable"
		}
		for index, flow := range capture.Traffic {
			selected := flow.Channel == channel && (channel != "sut_to_dependency" || flow.AssetID == dependency)
			if interaction := flow.Interaction; interaction != nil {
				selected = channel == "sut_to_dependency" && interaction.Boundary.Role == "dependency" && interaction.Boundary.ID == dependency || channel == "test_to_sut" && interaction.Boundary.Role == "provided" && interaction.Boundary.ID == providerAPI(s.run)
			}
			if !selected {
				continue
			}
			var schema *artifact.SchemaRef
			schemaProblem := ""
			if flow.Request.GRPC != nil {
				for i := range capture.Schemas {
					ref := &capture.Schemas[i]
					if ref.Type == "protobuf" && ref.ID == flow.Request.GRPC.SchemaRef && (channel == "test_to_sut" && (ref.Role == "sut" || ref.Role == "provided" && ref.AssetID == flow.AssetID) || channel == "sut_to_dependency" && ref.Role == "dependency" && ref.AssetID == dependency) {
						schema = ref
						break
					}
				}
			} else if flow.Request.HTTP != nil {
				for i := range capture.Schemas {
					ref := &capture.Schemas[i]
					if ref.Type != "openapi" || !(channel == "test_to_sut" && (ref.Role == "sut" || ref.Role == "provided" && ref.AssetID == flow.AssetID) || channel == "sut_to_dependency" && ref.Role == "dependency" && ref.AssetID == dependency) {
						continue
					}
					if schema != nil {
						schemaProblem = "multiple OpenAPI contracts apply to this HTTP boundary"
						break
					}
					schema = ref
				}
			}
			out = append(out, sample{flow: flow, operation: operation(flow), problem: problem, schema: schema, schemaProblem: schemaProblem, evidence: Evidence{
				RunID: s.run.RunID, AttemptID: capture.TestCase.ID, TestName: capture.TestCase.Name, TestIdentity: capture.TestCase.Identity,
				FlowID: flow.ID, EvidencePath: refs[capture.TestCase.ID] + "#/capture/traffic/" + strconv.Itoa(index), TestOutcome: capture.TestCase.Status,
			}})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].evidence.AttemptID != out[j].evidence.AttemptID {
			return out[i].evidence.AttemptID < out[j].evidence.AttemptID
		}
		return out[i].evidence.FlowID < out[j].evidence.FlowID
	})
	return out
}

func evidenceProblem(_ *snapshot, item sample) string {
	return item.problem
}

func (s *snapshot) verifyCollections(inputs []string, boundary string) error {
	if len(inputs) == 0 {
		inputs = []string{filepath.Join(s.root, "exports", "postman")}
	}
	result, err := postman.GenerateAllWithOptions(s.root, "", postman.Options{OmitIncomplete: true})
	if err != nil {
		return err
	}
	var expected []postman.Export
	for _, export := range result.Exports {
		if export.BoundaryID == boundary {
			expected = append(expected, export)
		}
	}
	if len(expected) == 0 {
		return errors.New("selected boundary has no representable Postman projection")
	}
	if err := VerifyCollections(inputs, expected); err != nil {
		return err
	}
	return s.unchanged()
}

func operation(flow artifact.Flow) string {
	if flow.Request.HTTP != nil {
		u, err := url.Parse(flow.Request.HTTP.URL)
		if err == nil {
			return flow.Request.HTTP.Method + " " + u.EscapedPath()
		}
	}
	if flow.Request.GRPC != nil {
		return "gRPC " + flow.Request.GRPC.Procedure
	}
	return flow.Protocol
}

func limitedEvidence(refs []Evidence) []Evidence {
	if len(refs) > 10 {
		return refs[:10]
	}
	return refs
}

func unique(values []string) []string {
	var out []string
	for _, v := range values {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

func headerName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r > 127 || !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyz", r) {
			return false
		}
	}
	return true
}

func differencePaths(expected, actual string) []string {
	return jsonDifferencePaths(expected, actual, "response")
}

func jsonDifferencePaths(expected, actual, root string) []string {
	var left, right any
	decode := func(data string, result *any) error {
		decoder := json.NewDecoder(bytes.NewBufferString(data))
		decoder.UseNumber()
		return decoder.Decode(result)
	}
	if decode(expected, &left) != nil || decode(actual, &right) != nil {
		return []string{root}
	}
	var paths []string
	var walk func(any, any, string)
	walk = func(a, b any, path string) {
		if len(paths) >= 20 || reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := []string{}
			for k := range am {
				keys = append(keys, k)
			}
			for k := range bm {
				if _, ok := am[k]; !ok {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				av, ae := am[key]
				bv, be := bm[key]
				p := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
				if ae != be {
					if len(paths) < 20 {
						paths = append(paths, p)
					}
				} else {
					walk(av, bv, p)
				}
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok && len(aa) == len(ba) {
			for i := range aa {
				walk(aa[i], ba[i], path+"/"+strconv.Itoa(i))
			}
			return
		}
		paths = append(paths, path)
	}
	walk(left, right, root)
	if len(paths) == 0 {
		return []string{root}
	}
	return paths
}
