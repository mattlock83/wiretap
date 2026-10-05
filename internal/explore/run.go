package explore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/bundlereport"
	"github.com/phrond/wiretap/internal/capture"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/hooks"
	"github.com/phrond/wiretap/internal/redact"
)

type Options struct {
	Bundle, Attempt, Test, Target, Output string
	Config                                Config
	Logs                                  io.Writer
}
type Result struct {
	Bundle                              string
	Generated, Executed, Passed, Failed int
}
type step struct {
	ID      string
	Request request
}

type hookInput struct {
	Version   int                       `json:"version"`
	Event     string                    `json:"event"`
	RunID     string                    `json:"run_id"`
	CaseID    string                    `json:"case_id,omitempty"`
	Scenario  string                    `json:"scenario"`
	Mode      string                    `json:"mode,omitempty"`
	Mutation  *artifact.RejectionTarget `json:"mutation,omitempty"`
	FlowID    string                    `json:"flow_id,omitempty"`
	Request   *request                  `json:"request,omitempty"`
	Bindings  map[string]any            `json:"bindings"`
	State     map[string]any            `json:"state"`
	Headers   map[string]string         `json:"headers"`
	Exchanges []artifact.Flow           `json:"exchanges,omitempty"`
	Outcome   string                    `json:"outcome,omitempty"`
	Error     string                    `json:"error,omitempty"`
}

// Run never executes bundle content or updates the source bundle. Only the
// explicitly supplied project configuration selects executable hook commands.
func Run(ctx context.Context, opts Options) (result Result, err error) {
	cfg := opts.Config
	cfg.Defaults()
	if err = cfg.Validate(); err != nil {
		return result, err
	}
	if opts.Output == "" {
		opts.Output = "artifacts"
	}
	if opts.Logs == nil {
		opts.Logs = os.Stderr
	}
	target, e := url.Parse(opts.Target)
	if e != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return result, fmt.Errorf("target must be an http(s) origin, without credentials, a base path, query or fragment")
	}
	source, captures, e := bundle.Load(opts.Bundle)
	if e != nil {
		return result, e
	}
	if source.RunState != "complete" || len(source.Pending) > 0 {
		return result, fmt.Errorf("source bundle must be complete")
	}
	seed, e := selectSeed(captures, opts.Attempt, opts.Test)
	if e != nil {
		return result, e
	}
	for _, flow := range seed.Traffic {
		if flow.Interaction != nil {
			return result, fmt.Errorf("exploration does not support event interaction %q (%s %s); replay would omit its recorded events, including any dependency interactions", flow.ID, flow.Protocol, flow.Interaction.Kind)
		}
		if flow.Channel == "test_to_sut" && flow.Protocol == "grpc" {
			return result, fmt.Errorf("gRPC provider exploration is not supported; select a test with only HTTP provider requests")
		}
	}
	cfg.Redactions = append(append([]artifact.Redaction(nil), seed.Redactions...), cfg.Redactions...)
	var ref artifact.SchemaRef
	for _, r := range seed.Schemas {
		if r.Role == "sut" {
			if ref.ID != "" {
				return result, fmt.Errorf("exploration requires one provider schema per seed attempt")
			}
			ref = r
		}
	}
	if ref.Type != "openapi" || ref.Source.Type != "file" {
		return result, fmt.Errorf("seed requires a supplied provider OpenAPI schema")
	}
	revision, e := coverage.ReplayContractRevision(ref)
	if e != nil {
		return result, e
	}
	s, e := loadSpec(ref)
	if e != nil {
		return result, e
	}
	steps, e := seedSteps(seed)
	if e != nil {
		return result, e
	}
	mutations, notes, e := generate(s, steps, source.APIID, revision, cfg.ExpectedStatuses)
	if e != nil {
		return result, e
	}
	if len(mutations) == 0 {
		return result, fmt.Errorf("no supported isolated invalid-input cases can be generated from this seed")
	}
	for _, b := range cfg.Bindings {
		if b.FlowID != "" && !hasStep(steps, b.FlowID) {
			return result, fmt.Errorf("binding refers to unknown provider flow %q", b.FlowID)
		}
	}
	for _, x := range cfg.Extract {
		if !hasStep(steps, x.FlowID) {
			return result, fmt.Errorf("extraction refers to unknown provider flow %q", x.FlowID)
		}
	}
	w, e := bundle.New(opts.Output)
	if e != nil {
		return result, e
	}
	result.Bundle = w.Path()
	result.Generated = len(mutations)
	provenance := artifact.Exploration{Version: Version, SourceRunID: source.RunID, SourceAttemptID: seed.TestCase.ID}
	for _, r := range source.Attempts {
		if r.AttemptID == seed.TestCase.ID {
			provenance.SourceSHA256 = r.SHA256
		}
	}
	if e = w.Configure(bundle.Identity{APIID: source.APIID, SUTTargetKind: "service", TestSource: map[string]any{"generator": Version, "source_run_id": source.RunID, "source_attempt_id": seed.TestCase.ID, "source_sha256": provenance.SourceSHA256}}); e != nil {
		return result, e
	}
	// Always finish a readable output, including failed setup and interrupted runs.
	defer func() {
		state := "complete"
		if err != nil {
			state = "interrupted"
		}
		err = errors.Join(err, w.Finalize(state))
		_, viewErr := bundlereport.Rebuild(w.Path())
		err = errors.Join(err, viewErr)
	}()
	base := hookInput{Version: hooks.Version, RunID: w.Snapshot().RunID, Scenario: seed.TestCase.Name, Bindings: map[string]any{}, State: map[string]any{}, Headers: map[string]string{}}
	call := func(callCtx context.Context, event string, in *hookInput) (hooks.Output, error) {
		in.Event = event
		return hooks.Call(callCtx, cfg.Hooks.Commands()[event], cfg.Dir, *in, opts.Logs)
	}
	defer func() {
		clean := context.WithoutCancel(ctx)
		base.Outcome = "pass"
		if err != nil || result.Failed > 0 {
			base.Outcome = "fail"
		}
		if err != nil {
			base.Error = err.Error()
		}
		_, cleanupErr := call(clean, "after_run", &base)
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("after_run: %w", cleanupErr))
		}
	}()
	initial, e := call(ctx, "before_run", &base)
	if e != nil {
		return result, fmt.Errorf("before_run: %w", e)
	}
	merge(&base, initial)
	catalogue := make([]artifact.RejectionTarget, 0, len(mutations))
	for _, m := range mutations {
		catalogue = append(catalogue, m.Target)
	}
	if len(mutations) > cfg.MaxCases {
		notes = append(notes, fmt.Sprintf("Case budget %d leaves %d generated targets unexecuted", cfg.MaxCases, len(mutations)-cfg.MaxCases))
	}
	baseline, runErr := runCase(ctx, w, s, steps, nil, base, cfg, target, provenance, catalogue, notes, call)
	if runErr != nil {
		return result, runErr
	}
	if baseline.TestCase.Status != "pass" {
		return result, fmt.Errorf("fresh baseline failed; no mutations were executed (see output bundle)")
	}
	for i := range mutations {
		if i >= cfg.MaxCases {
			break
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		art, caseErr := runCase(ctx, w, s, steps, &mutations[i], base, cfg, target, provenance, nil, nil, call)
		if art != nil {
			result.Executed++
			if art.TestCase.Status == "pass" {
				result.Passed++
			} else {
				result.Failed++
			}
		}
		if caseErr != nil {
			return result, caseErr
		}
	}
	return result, nil
}

type hookCaller func(context.Context, string, *hookInput) (hooks.Output, error)

func runCase(ctx context.Context, w *bundle.Writer, s *spec, steps []step, m *mutation, base hookInput, cfg Config, target *url.URL, provenance artifact.Exploration, catalogue []artifact.RejectionTarget, notes []string, call hookCaller) (art *artifact.Artifact, err error) {
	id := "baseline"
	mode := "baseline"
	name := base.Scenario + " [baseline]"
	if m != nil {
		id = "negative-" + m.Target.Key[:20]
		mode = "rejection"
		name = base.Scenario + " [" + m.Target.Constraint + " " + m.Target.Location + "]"
		t := m.Target
		provenance.Target = &t
	}
	provenance.Mode = mode
	provenance.Catalogue = catalogue
	provenance.Notes = notes
	art = &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: id, Name: name, StartedAt: time.Now().UTC(), Status: "pass"}, Exploration: &provenance, SchemaValidation: &artifact.SchemaValidation{Status: "pass"}}
	frozen, e := w.Register(art.TestCase, []artifact.SchemaRef{s.ref})
	if e != nil {
		return nil, e
	}
	art.Schemas = frozen
	input := copyContext(base)
	input.CaseID = id
	input.Mode = mode
	input.Mutation = provenance.Target
	fail := func(message string, incomplete bool) {
		art.TestCase.Status = "fail"
		art.CaptureDiagnostics = append(art.CaptureDiagnostics, message)
		art.CaptureIncomplete = art.CaptureIncomplete || incomplete
		input.Error = message
	}
	defer func() {
		input.Exchanges = art.Traffic
		input.Outcome = art.TestCase.Status
		_, cleanupErr := call(context.WithoutCancel(ctx), "after_case", &input)
		if cleanupErr != nil {
			fail("after_case failed: "+cleanupErr.Error(), true)
			err = errors.Join(err, fmt.Errorf("after_case: %w", cleanupErr))
		}
		art.TestCase.FinishedAt = time.Now().UTC()
		// Runtime context never becomes evidence. Header credentials are always
		// removed; user-selected payload redactions apply before persistence.
		rules := append([]artifact.Redaction(nil), cfg.Redactions...)
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
			if hasHeader(art, "request", name) {
				rules = append(rules, artifact.Redaction{Field: "request.headers." + name, Rule: "remove"})
			}
		}
		if hasHeader(art, "response", "Set-Cookie") {
			rules = append(rules, artifact.Redaction{Field: "response.headers.Set-Cookie", Rule: "remove"})
		}
		art.Redactions = rules
		_ = redact.Apply(art, rules)
		_, commitErr := w.Commit(art)
		err = errors.Join(err, commitErr)
		if commitErr == nil {
			_, refreshErr := bundlereport.Refresh(w.Path())
			err = errors.Join(err, refreshErr)
		}
	}()
	prepared, e := call(ctx, "before_case", &input)
	if e != nil {
		fail("before_case failed: "+e.Error(), true)
		return art, fmt.Errorf("before_case: %w", e)
	}
	merge(&input, prepared)
	timeout, _ := time.ParseDuration(cfg.RequestTimeout)
	jar, _ := cookiejar.New(nil)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for index, st := range steps {
		if m != nil && index > m.Step {
			break
		}
		r := st.Request.clone()
		r.Headers.Del("Authorization")
		r.Headers.Del("Cookie")
		u, _ := url.Parse(r.URL)
		u.Scheme = target.Scheme
		u.Host = target.Host
		u.User = nil
		r.URL = u.String()
		// Apply the cookie jar before validation/capture so evidence describes
		// the actual transmitted request. Each case owns a fresh jar.
		cookieRequest := &http.Request{Header: r.Headers}
		for _, cookie := range jar.Cookies(u) {
			cookieRequest.AddCookie(cookie)
		}
		for k, v := range input.Headers {
			r.Headers.Set(k, v)
		}
		for _, b := range cfg.Bindings {
			if b.FlowID != "" && b.FlowID != st.ID {
				continue
			}
			value, ok := input.Bindings[b.Name]
			if !ok {
				fail("missing runtime binding: "+b.Name, true)
				return art, fmt.Errorf("missing runtime binding %q", b.Name)
			}
			in, e := s.input(r)
			if e != nil {
				fail("binding route unavailable", true)
				return art, e
			}
			if e = setValue(&r, b.In, b.Path, value, false, in); e != nil {
				fail("binding failed: "+e.Error(), true)
				return art, e
			}
		}
		if e = s.validate(r); e != nil {
			fail("unmutated request is invalid after runtime bindings", true)
			return art, fmt.Errorf("flow %s: runtime bindings do not produce a valid request", st.ID)
		}
		isMutation := m != nil && index == m.Step
		if isMutation {
			if e = m.apply(&r, s); e != nil {
				fail("mutation could not be applied", true)
				return art, e
			}
		}
		input.FlowID = st.ID
		// JSON decoding cannot preserve the original body whitespace/ordering.
		// Signing hooks receive the exact bytes separately from the parsed body.
		r.BodyBase64 = base64.StdEncoding.EncodeToString(r.Body)
		input.Request = &r
		input.Exchanges = art.Traffic
		prepared, e = call(ctx, "before_request", &input)
		if e != nil {
			fail("before_request failed: "+e.Error(), true)
			return art, e
		}
		for k, v := range prepared.Headers {
			r.Headers.Set(k, v)
		}
		mergeValues(input.Bindings, prepared.Bindings)
		mergeValues(input.State, prepared.State)
		validation := s.validate(r)
		if isMutation {
			if !exactViolation(validation, m.Target.Location, m.Target.Constraint) {
				fail("final request does not isolate the planned violation", true)
				return art, fmt.Errorf("flow %s: binding or request hook changed the planned violation", st.ID)
			}
			art.Exploration.RequestVerified = true
			art.SchemaValidation.Status = "expected_rejection"
		} else if validation != nil {
			fail("final baseline/prefix request is invalid", true)
			return art, fmt.Errorf("flow %s: request hook produced invalid input", st.ID)
		}
		flow := artifact.Flow{ID: st.ID, Protocol: "http", Channel: "test_to_sut", StartedAt: time.Now().UTC(), Request: artifact.Message{HTTP: r.message()}}
		req, e := r.http(ctx)
		if e != nil {
			fail("request construction failed", true)
			return art, e
		}
		resp, e := client.Do(req)
		if e != nil {
			// Network error strings can contain URLs with runtime credentials.
			flow.TransportError = "HTTP transport failed"
			art.Traffic = append(art.Traffic, flow)
			fail("HTTP transport failed", true)
			break
		}
		body, readErr := capture.ReadAll(resp.Body, capture.DefaultMaxBodyBytes)
		resp.Body.Close()
		jar.SetCookies(req.URL, resp.Cookies())
		flow.DurationMS = time.Since(flow.StartedAt).Milliseconds()
		flow.Response.HTTP = &artifact.HttpMessage{Status: resp.StatusCode, Headers: artifact.Headers(resp.Header.Clone()), Body: &artifact.Body{Encoding: "utf-8", Text: string(body.Bytes), Truncated: body.Truncated, OriginalBytes: body.OriginalBytes}}
		if !utf8.Valid(body.Bytes) {
			flow.Response.HTTP.Body.Encoding = "base64"
			flow.Response.HTTP.Body.Text = ""
			flow.Response.HTTP.Body.Bytes = base64.StdEncoding.EncodeToString(body.Bytes)
		}
		art.Traffic = append(art.Traffic, flow)
		if readErr != nil || body.Truncated {
			fail("response could not be completely captured", true)
			break
		}
		responseErr := s.response(r, flow.Response.HTTP)
		check := artifact.Assertion{ID: "response-schema-" + st.ID, Channel: "test_to_sut", Phase: "response", FlowID: st.ID, Operator: "schema", Result: "pass"}
		if responseErr != nil {
			check.Result = "fail"
			check.Message = "Response violates the frozen provider contract"
			fail(check.Message, false)
			art.SchemaValidation.Status = "fail"
		}
		art.Assertions = append(art.Assertions, check)
		statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300
		expected := []int{200, 201, 202, 203, 204, 205, 206, 207, 208, 226}
		checkID := "baseline-status-" + st.ID
		if isMutation {
			expected = cfg.ExpectedStatuses
			statusOK = containsStatus(expected, resp.StatusCode)
			checkID = "expected-rejection"
		}
		statusCheck := artifact.Assertion{ID: checkID, Channel: "test_to_sut", Phase: "response", FlowID: st.ID, Operator: "status_in", Expected: expected, Actual: resp.StatusCode, Result: "pass"}
		if !statusOK {
			statusCheck.Result = "fail"
			fail(fmt.Sprintf("unexpected HTTP %d for %s flow %s", resp.StatusCode, mode, st.ID), false)
		}
		art.Assertions = append(art.Assertions, statusCheck)
		if !statusOK || responseErr != nil {
			break
		}
		if !isMutation {
			if e = extractValues(cfg.Extract, st.ID, flow.Response.HTTP, input.Bindings); e != nil {
				fail(e.Error(), true)
				return art, e
			}
		}
	}
	input.Request = nil
	input.Exchanges = art.Traffic
	input.Outcome = art.TestCase.Status
	checked, e := call(ctx, "check_case", &input)
	if e != nil {
		fail("check_case failed: "+e.Error(), true)
		return art, e
	}
	merge(&input, checked)
	if cfg.Hooks.CheckCase != nil && len(checked.Checks) == 0 {
		fail("check_case returned no explicit checks", true)
		return art, fmt.Errorf("check_case must return at least one named check")
	}
	for i, c := range checked.Checks {
		a := artifact.Assertion{ID: fmt.Sprintf("property-%d", i+1), Operator: "property", Field: c.Name, Message: c.Message, Result: "pass"}
		if !c.Passed {
			a.Result = "fail"
			fail("property failed: "+c.Name, false)
		}
		art.Assertions = append(art.Assertions, a)
	}
	return art, nil
}

func selectSeed(captures []*artifact.Artifact, attempt, test string) (*artifact.Artifact, error) {
	var selected *artifact.Artifact
	for _, a := range captures {
		if attempt != "" && a.TestCase.ID != attempt || test != "" && a.TestCase.Name != test {
			continue
		}
		if a.Exploration != nil {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("seed selection is ambiguous; specify --attempt")
		}
		selected = a
	}
	if selected == nil {
		return nil, fmt.Errorf("no matching authored test attempt")
	}
	if selected.TestCase.Status != "pass" || selected.CaptureIncomplete {
		return nil, fmt.Errorf("seed must be a passing, complete test attempt")
	}
	return selected, nil
}

func seedSteps(seed *artifact.Artifact) ([]step, error) {
	for _, r := range seed.Redactions {
		if strings.HasPrefix(strings.ToLower(r.Field), "request.body.") {
			return nil, fmt.Errorf("seed request bodies were redacted; capture a replayable fixture without sensitive body values")
		}
	}
	flows := append([]artifact.Flow(nil), seed.Traffic...)
	sort.SliceStable(flows, func(i, j int) bool { return flows[i].StartedAt.Before(flows[j].StartedAt) })
	var out []step
	var previousEnd time.Time
	for _, f := range flows {
		if f.Channel != "test_to_sut" {
			continue
		}
		if f.Protocol != "http" || f.TransportError != "" || f.Response.HTTP == nil || f.Response.HTTP.Status < 200 || f.Response.HTTP.Status >= 300 {
			return nil, fmt.Errorf("seed provider flows must be successful HTTP exchanges")
		}
		if !previousEnd.IsZero() && f.StartedAt.Before(previousEnd) {
			return nil, fmt.Errorf("concurrent provider requests cannot be replayed as a sequential scenario")
		}
		if !f.StartedAt.IsZero() {
			previousEnd = f.StartedAt.Add(time.Duration(f.DurationMS) * time.Millisecond)
		}
		r, err := seedRequest(f.Request.HTTP)
		if err != nil {
			return nil, fmt.Errorf("flow %s: %w", f.ID, err)
		}
		out = append(out, step{ID: f.ID, Request: r})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("seed has no provider HTTP requests")
	}
	return out, nil
}

func containsStatus(statuses []int, status int) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}
	return false
}
func hasStep(steps []step, id string) bool {
	for _, s := range steps {
		if s.ID == id {
			return true
		}
	}
	return false
}
func mergeValues(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}
func merge(in *hookInput, out hooks.Output) {
	mergeValues(in.Bindings, out.Bindings)
	mergeValues(in.State, out.State)
	for k, v := range out.Headers {
		in.Headers[k] = v
	}
}
func copyContext(base hookInput) hookInput {
	out := base
	out.Bindings = map[string]any{}
	out.State = map[string]any{}
	out.Headers = map[string]string{}
	mergeValues(out.Bindings, base.Bindings)
	mergeValues(out.State, base.State)
	for k, v := range base.Headers {
		out.Headers[k] = v
	}
	return out
}
func extractValues(rules []Extraction, flowID string, msg *artifact.HttpMessage, bindings map[string]any) error {
	for _, rule := range rules {
		if rule.FlowID != flowID {
			continue
		}
		var v any
		if rule.In == "header" {
			values := http.Header(msg.Headers).Values(rule.Path)
			if len(values) != 1 {
				return fmt.Errorf("extraction %s requires exactly one response header", rule.Name)
			}
			v = values[0]
		} else {
			body, err := decodeJSON([]byte(bodyText(msg)))
			if err != nil {
				return fmt.Errorf("extraction %s requires JSON", rule.Name)
			}
			v, err = jsonAt(body, rule.Path)
			if err != nil {
				return fmt.Errorf("extraction %s failed", rule.Name)
			}
		}
		bindings[rule.Name] = v
	}
	return nil
}
func hasHeader(a *artifact.Artifact, phase, name string) bool {
	for _, f := range a.Traffic {
		m := f.Request.HTTP
		if phase == "response" {
			m = f.Response.HTTP
		}
		if m != nil {
			for k := range m.Headers {
				if strings.EqualFold(k, name) {
					return true
				}
			}
		}
	}
	return false
}
