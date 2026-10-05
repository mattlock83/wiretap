package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/hooks"
)

// Selection identifies HTTP operations by METHOD and the spec's path template,
// or gRPC methods by their exact /package.Service/Method procedure.
// FlowID disambiguates repeated calls to the same operation in the seed.
type Selection struct {
	Operations []string `json:"operations,omitempty"`
	FlowID     string   `json:"flow_id,omitempty"`
}
type HookScope struct {
	ID         string   `json:"id"`
	Event      string   `json:"event"`
	Operations []string `json:"operations,omitempty"`
}
type Result struct {
	Bundle     string   `json:"bundle_path"`
	Generated  int      `json:"generated"`
	Executed   int      `json:"executed"`
	Passed     int      `json:"passed"`
	Failed     int      `json:"failed"`
	Baselines  int      `json:"baselines"`
	Skipped    int      `json:"skipped"`
	Complete   bool     `json:"complete"`
	Notes      []string `json:"notes,omitempty"`
	AttemptIDs []string `json:"attempt_ids,omitempty"`
}

// Plan owns an immutable snapshot of its source, contract and generated cases.
// Execution never reads from or finalizes an earlier bundle.
type Plan struct {
	seed       *artifact.Artifact
	cfg        Config
	spec       *spec
	steps      []step
	mutations  []mutation
	selected   []int
	operations []string
	notes      []string
	grpc       bool
}

func (p *Plan) Generated() int       { return len(p.mutations) }
func (p *Plan) Operations() []string { return append([]string(nil), p.operations...) }

// GRPCRequest is a finite native RPC conversation. ResponseGates preserves the
// observed number of responses preceding each request message in a stream.
type GRPCRequest struct {
	ID            string
	Procedure     string
	Metadata      http.Header
	Messages      [][]byte
	ResponseGates []int
	SchemaID      string
}
type GRPCHookRequest struct {
	Procedure      string            `json:"procedure"`
	Metadata       http.Header       `json:"metadata"`
	Messages       []json.RawMessage `json:"messages"`
	MessagesBase64 []string          `json:"messages_base64"`
	ResponseGates  []int             `json:"response_gates,omitempty"`
}

// Execution connects the protocol-independent lifecycle to the active run.
// BeginCase installs capture ownership before fixture hooks. SnapshotCase drains
// dependency traffic for checks; EndCase releases capture after cleanup hooks.
type Execution struct {
	InvokeGRPC      func(context.Context, GRPCRequest) (artifact.Flow, error)
	Writer          *bundle.Writer
	Target          string
	InvocationID    string
	ParentAttemptID string
	CallHook        func(context.Context, HookInput) (hooks.Output, error)
	BeginCase       func(context.Context, artifact.TestCase) (context.Context, error)
	SnapshotCase    func(context.Context, *artifact.Artifact) error
	EndCase         func(context.Context, *artifact.Artifact) error
}

func Prepare(seed *artifact.Artifact, apiID string, selection Selection, cfg Config) (*Plan, error) {
	cfg.Defaults()
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var frozenConfig Config
	if err = json.Unmarshal(configJSON, &frozenConfig); err != nil {
		return nil, err
	}
	cfg = frozenConfig
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if seed == nil || seed.TestCase.ID == "" || seed.CaptureIncomplete || seed.TestCase.Status == "fail" || seed.Exploration != nil {
		return nil, fmt.Errorf("exploration requires a complete, non-failing authored capture")
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		return nil, err
	}
	snapshot := new(artifact.Artifact)
	if err = json.Unmarshal(raw, snapshot); err != nil {
		return nil, err
	}
	for _, flow := range snapshot.Traffic {
		if flow.Interaction != nil {
			return nil, fmt.Errorf("exploration does not support event interaction %q", flow.ID)
		}

	}
	var ref artifact.SchemaRef
	for _, r := range snapshot.Schemas {
		if r.Role == "sut" || r.Role == "provided" && r.AssetID == apiID {
			if ref.ID != "" {
				return nil, fmt.Errorf("exploration requires one provider schema per seed")
			}
			ref = r
		}
	}
	if ref.Type == "protobuf" && ref.Source.Type == "file" {
		return prepareGRPC(snapshot, ref, apiID, selection, cfg)
	}
	if ref.Type != "openapi" || ref.Source.Type != "file" {
		return nil, fmt.Errorf("seed requires a frozen provider OpenAPI or Protobuf contract")
	}
	revision, err := coverage.ReplayContractRevision(ref)
	if err != nil {
		return nil, err
	}
	s, err := loadSpec(ref)
	if err != nil {
		return nil, err
	}
	steps, err := seedSteps(snapshot)
	if err != nil {
		return nil, err
	}
	for index := range steps {
		in, e := s.input(steps[index].Request)
		if e != nil {
			return nil, e
		}
		steps[index].Operation = in.Route.Method + " " + in.Route.Path
	}
	selected, operations, err := selectOperations(steps, selection, &cfg)
	if err != nil {
		return nil, err
	}
	generationSteps := make([]step, 0, len(selected))
	for _, index := range selected {
		generationSteps = append(generationSteps, steps[index])
	}
	mutations, notes, err := generate(s, generationSteps, apiID, revision, cfg.ExpectedStatuses)
	if err != nil {
		return nil, err
	}
	if len(mutations) == 0 {
		return nil, fmt.Errorf("no supported isolated invalid-input cases can be generated from the selected requests")
	}
	for i := range mutations {
		mutations[i].Step = selected[mutations[i].Step]
	}
	for _, b := range cfg.Bindings {
		if b.In == "metadata" || b.MessageIndex != nil {
			return nil, fmt.Errorf("HTTP exploration does not support gRPC metadata or message_index bindings")
		}
		if b.FlowID != "" && !hasStep(steps, b.FlowID) {
			return nil, fmt.Errorf("binding refers to unknown provider flow %q", b.FlowID)
		}
	}
	for _, x := range cfg.Extract {
		if x.In == "metadata" || x.In == "trailers" || x.MessageIndex != nil {
			return nil, fmt.Errorf("HTTP exploration does not support gRPC metadata/message extraction")
		}
		if !hasStep(steps, x.FlowID) {
			return nil, fmt.Errorf("extraction refers to unknown provider flow %q", x.FlowID)
		}
	}
	cfg.Redactions = append(append([]artifact.Redaction(nil), snapshot.Redactions...), cfg.Redactions...)
	return &Plan{seed: snapshot, cfg: cfg, spec: s, steps: steps, mutations: mutations, selected: selected, operations: operations, notes: notes}, nil
}

func normalizeOperation(value string) (string, error) {
	if strings.HasPrefix(value, "/") {
		parts := strings.Split(value, "/")
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" || strings.ContainsAny(value, " ?#*\t\n") {
			return "", fmt.Errorf("gRPC operation must be /package.Service/Method")
		}
		return value, nil
	}
	fields := strings.Fields(value)
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "/") || strings.ContainsAny(fields[1], "?#") {
		return "", fmt.Errorf("operation %q must be METHOD /spec/path", value)
	}
	method := strings.ToUpper(fields[0])
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
	default:
		return "", fmt.Errorf("unsupported HTTP method in operation %q", value)
	}
	return method + " " + fields[1], nil
}
func validHookEvent(event string) bool {
	switch event {
	case "before_explore", "before_case", "before_request", "check_case", "after_case", "after_explore":
		return true
	}
	return false
}
func hookMatches(scopes []HookScope, event string, in HookInput) bool {
	for _, scope := range scopes {
		if scope.Event != event {
			continue
		}
		if len(scope.Operations) == 0 {
			return true
		}
		for _, op := range scope.Operations {
			if in.Operation == op {
				return true
			}
			if event == "before_explore" || event == "after_explore" {
				for _, selected := range in.Operations {
					if op == selected {
						return true
					}
				}
			}
		}
	}
	return false
}

// Execute adds generated attempts to the caller's open bundle. A fresh baseline
// uses the same fixtures and request preparation as each mutation.
func Execute(ctx context.Context, p *Plan, execution Execution) (result Result, err error) {
	if p == nil || execution.Writer == nil || execution.InvocationID == "" || execution.ParentAttemptID == "" {
		return result, fmt.Errorf("plan, writer, invocation_id and parent_attempt_id are required")
	}
	if strings.ContainsAny(execution.InvocationID, "/\\") {
		return result, fmt.Errorf("invocation_id cannot contain path separators")
	}
	if p.grpc && execution.InvokeGRPC == nil {
		return result, fmt.Errorf("gRPC exploration requires a native transport adapter")
	}
	target, e := url.Parse(execution.Target)
	if e != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return result, fmt.Errorf("target must be an http(s) origin")
	}
	if len(p.cfg.HookScopes) > 0 && execution.CallHook == nil {
		return result, fmt.Errorf("declared lifecycle hooks require a hook dispatcher")
	}
	result.Bundle = execution.Writer.Path()
	result.Generated = len(p.mutations)
	result.Notes = append([]string(nil), p.notes...)
	if p.cfg.MaxCases > 0 && len(p.mutations) > p.cfg.MaxCases {
		result.Skipped = len(p.mutations) - p.cfg.MaxCases
		result.Notes = append(result.Notes, fmt.Sprintf("Case budget %d leaves %d generated targets unexecuted", p.cfg.MaxCases, result.Skipped))
	}
	snapshot := execution.Writer.Snapshot()
	provenance := artifact.Exploration{Version: Version, SourceRunID: snapshot.RunID, SourceAttemptID: execution.ParentAttemptID, InvocationID: execution.InvocationID, SourceKind: "active_test"}
	for _, index := range p.selected {
		provenance.SourceFlowIDs = append(provenance.SourceFlowIDs, p.steps[index].ID)
	}
	base := HookInput{Version: hooks.Version, RunID: snapshot.RunID, Scenario: p.seed.TestCase.Name, InvocationID: execution.InvocationID, ParentAttemptID: execution.ParentAttemptID, Operations: p.Operations(), Bindings: map[string]any{}, State: map[string]any{}, Headers: map[string]string{}}
	call := func(callCtx context.Context, event string, in *HookInput) (hooks.Output, error) {
		in.Event = event
		if !hookMatches(p.cfg.HookScopes, event, *in) {
			return hooks.Output{}, nil
		}
		combined := hooks.Output{Bindings: map[string]any{}, State: map[string]any{}, Headers: map[string]string{}}
		local := copyContext(*in)
		for _, scope := range p.cfg.HookScopes {
			if !hookMatches([]HookScope{scope}, event, local) {
				continue
			}
			local.HookID = scope.ID
			duration, _ := time.ParseDuration(p.cfg.HookTimeout)
			bounded, cancel := context.WithTimeout(callCtx, duration)
			output, e := execution.CallHook(bounded, local)
			cancel()
			if e != nil {
				return combined, e
			}
			if event != "check_case" && len(output.Checks) > 0 {
				return hooks.Output{}, fmt.Errorf("hook %s: checks are only accepted from check_case", scope.ID)
			}
			seen := map[string]bool{}
			for _, check := range output.Checks {
				if check.Name == "" || seen[check.Name] {
					return hooks.Output{}, fmt.Errorf("hook checks require unique nonempty names")
				}
				seen[check.Name] = true
			}
			if event == "check_case" && len(output.Checks) == 0 {
				return hooks.Output{}, fmt.Errorf("check_case hook %s must return at least one named check", scope.ID)
			}
			merge(&local, output)
			mergeValues(combined.Bindings, output.Bindings)
			mergeValues(combined.State, output.State)
			for k, v := range output.Headers {
				combined.Headers[k] = v
			}
			for _, check := range output.Checks {
				check.Name = scope.ID + ": " + check.Name
				combined.Checks = append(combined.Checks, check)
			}
		}
		return combined, nil
	}
	defer func() {
		base.Outcome = "pass"
		if err != nil || result.Failed > 0 {
			base.Outcome = "fail"
		}
		if err != nil {
			base.Error = err.Error()
		}
		_, e := call(context.WithoutCancel(ctx), "after_explore", &base)
		err = errors.Join(err, e)
		result.Complete = err == nil && result.Skipped == 0
	}()
	initial, e := call(ctx, "before_explore", &base)
	if e != nil {
		return result, fmt.Errorf("before_explore: %w", e)
	}
	merge(&base, initial)
	catalogue := make([]artifact.RejectionTarget, 0, len(p.mutations))
	for _, m := range p.mutations {
		catalogue = append(catalogue, m.Target)
	}
	executed := 0
	catalogueWritten := false
	for _, selected := range p.selected {
		if p.cfg.MaxCases > 0 && executed >= p.cfg.MaxCases {
			break
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		caseBase := copyContext(base)
		caseBase.Operation = p.steps[selected].Operation
		runtime := caseRuntime{execution: execution, schemas: p.seed.Schemas, baselineID: fmt.Sprintf("%s-baseline-%d", execution.InvocationID, selected+1)}
		var baselineCatalogue []artifact.RejectionTarget
		var baselineNotes []string
		if !catalogueWritten {
			baselineCatalogue = catalogue
			baselineNotes = result.Notes
		}
		baseline, e := runCase(ctx, execution.Writer, p.spec, p.steps[:selected+1], nil, caseBase, p.cfg, target, provenance, baselineCatalogue, baselineNotes, call, &runtime)
		if baseline != nil {
			catalogueWritten = true
			result.Baselines++
			result.AttemptIDs = append(result.AttemptIDs, baseline.TestCase.ID)
		}
		if e != nil {
			return result, e
		}
		if baseline.TestCase.Status != "pass" {
			return result, fmt.Errorf("fresh baseline failed for %s; no mutations executed for it", caseBase.Operation)
		}
		for i := range p.mutations {
			if p.mutations[i].Step != selected {
				continue
			}
			if p.cfg.MaxCases > 0 && executed >= p.cfg.MaxCases {
				break
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			art, e := runCase(ctx, execution.Writer, p.spec, p.steps, &p.mutations[i], caseBase, p.cfg, target, provenance, nil, nil, call, &runtime)
			if art != nil {
				executed++
				result.Executed++
				result.AttemptIDs = append(result.AttemptIDs, art.TestCase.ID)
				if art.TestCase.Status == "pass" {
					result.Passed++
				} else {
					result.Failed++
				}
			}
			if e != nil {
				return result, e
			}
			if art.CaptureIncomplete {
				return result, fmt.Errorf("generated capture is incomplete; remaining cases were not executed because fixture state is uncertain")
			}
		}
	}
	return result, nil
}

type caseRuntime struct {
	schemas    []artifact.SchemaRef
	execution  Execution
	baselineID string
}

func selectOperations(steps []step, selection Selection, cfg *Config) ([]int, []string, error) {
	wanted := map[string]bool{}
	for _, op := range selection.Operations {
		normalized, e := normalizeOperation(op)
		if e != nil {
			return nil, nil, e
		}
		wanted[normalized] = false
	}
	var selected []int
	available := map[string]bool{}
	selectedByOperation := map[string]int{}
	for index := range steps {
		operation := steps[index].Operation
		available[operation] = true
		_, match := wanted[operation]
		if (len(wanted) == 0 || match) && (selection.FlowID == "" || selection.FlowID == steps[index].ID) {
			if prior, found := selectedByOperation[operation]; found {
				return nil, nil, fmt.Errorf("operation %s has multiple captured requests (%s, %s); select flow_id", operation, steps[prior].ID, steps[index].ID)
			}
			selectedByOperation[operation] = index
			selected = append(selected, index)
			if match {
				wanted[operation] = true
			}
		}
	}
	for operation, found := range wanted {
		if !found {
			return nil, nil, fmt.Errorf("operation %s has no selected successful seed request", operation)
		}
	}
	if len(selected) == 0 {
		return nil, nil, fmt.Errorf("no provider request matches exploration selection")
	}
	for i := range cfg.HookScopes {
		for j, op := range cfg.HookScopes[i].Operations {
			normalized, _ := normalizeOperation(op)
			cfg.HookScopes[i].Operations[j] = normalized
			if !available[normalized] {
				return nil, nil, fmt.Errorf("hook %s operation %s has no request in the seed", cfg.HookScopes[i].Event, normalized)
			}
		}
	}

	operations := make([]string, 0, len(selected))
	for _, index := range selected {
		operations = append(operations, steps[index].Operation)
	}
	sort.Strings(operations)
	return selected, operations, nil
}
