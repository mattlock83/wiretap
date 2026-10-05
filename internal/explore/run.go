package explore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/capture"
	"github.com/phrond/wiretap/internal/hooks"
	"github.com/phrond/wiretap/internal/redact"
	"github.com/phrond/wiretap/internal/tracecontext"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type step struct {
	GRPC      *grpcSeed
	ID        string
	Operation string
	Request   request
}

type HookInput struct {
	GRPCRequest     *GRPCHookRequest          `json:"grpc_request,omitempty"`
	HookID          string                    `json:"hook_id,omitempty"`
	TraceContext    map[string]string         `json:"trace_context,omitempty"`
	Operation       string                    `json:"operation,omitempty"`
	Operations      []string                  `json:"operations,omitempty"`
	InvocationID    string                    `json:"invocation_id"`
	ParentAttemptID string                    `json:"parent_attempt_id"`
	Version         int                       `json:"version"`
	Event           string                    `json:"event"`
	RunID           string                    `json:"run_id"`
	CaseID          string                    `json:"case_id,omitempty"`
	Scenario        string                    `json:"scenario"`
	Mode            string                    `json:"mode,omitempty"`
	Mutation        *artifact.RejectionTarget `json:"mutation,omitempty"`
	FlowID          string                    `json:"flow_id,omitempty"`
	Request         *request                  `json:"request,omitempty"`
	Bindings        map[string]any            `json:"bindings"`
	State           map[string]any            `json:"state"`
	Headers         map[string]string         `json:"headers"`
	Exchanges       []artifact.Flow           `json:"exchanges,omitempty"`
	Outcome         string                    `json:"outcome,omitempty"`
	Error           string                    `json:"error,omitempty"`
}

type hookInput = HookInput

type hookCaller func(context.Context, string, *hookInput) (hooks.Output, error)

func runCase(ctx context.Context, w *bundle.Writer, s *spec, steps []step, m *mutation, base hookInput, cfg Config, target *url.URL, provenance artifact.Exploration, catalogue []artifact.RejectionTarget, notes []string, call hookCaller, runtimes ...*caseRuntime) (art *artifact.Artifact, err error) {
	var runtime *caseRuntime
	if len(runtimes) > 0 {
		runtime = runtimes[0]
	}
	id := "baseline"
	mode := "baseline"
	name := base.Scenario + " [baseline]"
	if m != nil {
		id = "negative-" + m.Target.Key[:20]
		if m.GRPC != nil {
			id += fmt.Sprintf("-message-%d", m.GRPC.index)
		}
		mode = "rejection"
		name = base.Scenario + " [" + m.Target.Constraint + " " + m.Target.Location + "]"
		t := m.Target
		provenance.Target = &t
	}
	if runtime != nil {
		if m == nil {
			id = runtime.baselineID
		} else {
			id = runtime.execution.InvocationID + "-" + id
		}
	}
	provenance.Mode = mode
	provenance.Catalogue = catalogue
	provenance.Notes = notes
	art = &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: id, Name: name, StartedAt: time.Now().UTC(), Status: "pass"}, Exploration: &provenance, SchemaValidation: &artifact.SchemaValidation{Status: "pass"}}
	var refs []artifact.SchemaRef
	if s != nil {
		refs = []artifact.SchemaRef{s.ref}
	}
	if runtime != nil {
		refs = runtime.schemas
	}
	frozen, e := w.Register(art.TestCase, refs)
	if e != nil {
		return nil, e
	}
	art.Schemas = frozen
	input := copyContext(base)
	input.CaseID = id
	input.Mode = mode
	input.Mutation = provenance.Target
	begun := false
	fail := func(message string, incomplete bool) {
		art.TestCase.Status = "fail"
		art.CaptureDiagnostics = append(art.CaptureDiagnostics, message)
		art.CaptureIncomplete = art.CaptureIncomplete || incomplete
		input.Error = message
	}
	defer func() {
		input.Request = nil
		input.GRPCRequest = nil
		input.Operation = base.Operation
		input.Exchanges = art.Traffic
		input.Outcome = art.TestCase.Status
		_, cleanupErr := call(context.WithoutCancel(ctx), "after_case", &input)
		if cleanupErr != nil {
			fail("after_case failed: "+cleanupErr.Error(), true)
			err = errors.Join(err, fmt.Errorf("after_case: %w", cleanupErr))
		}
		if runtime != nil && begun && runtime.execution.EndCase != nil {
			if endErr := runtime.execution.EndCase(context.WithoutCancel(ctx), art); endErr != nil {
				fail("capture cleanup failed: "+endErr.Error(), true)
				err = errors.Join(err, endErr)
			}
		}
		art.TestCase.FinishedAt = time.Now().UTC()
		// Runtime context never becomes evidence. Header credentials are always
		// removed; user-selected payload redactions apply before persistence.
		rules := append([]artifact.Redaction(nil), cfg.Redactions...)
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
			if hasHeader(art, "request", name) {
				rules = append(rules, artifact.Redaction{Field: "request.headers." + name, Rule: "remove"}, artifact.Redaction{Field: "request.trailers." + name, Rule: "remove"})
			}
		}
		if hasHeader(art, "response", "Set-Cookie") {
			rules = append(rules, artifact.Redaction{Field: "response.headers.Set-Cookie", Rule: "remove"}, artifact.Redaction{Field: "response.trailers.Set-Cookie", Rule: "remove"})
		}
		art.Redactions = rules
		_ = redact.Apply(art, rules)
		_, commitErr := w.Commit(art)
		err = errors.Join(err, commitErr)
	}()
	if runtime != nil && runtime.execution.BeginCase != nil {
		ctx, e = runtime.execution.BeginCase(ctx, art.TestCase)
		if e != nil {
			fail("capture setup failed: "+e.Error(), true)
			return art, e
		}
		begun = true
		carrier := propagation.MapCarrier{}
		propagation.TraceContext{}.Inject(ctx, carrier)
		input.TraceContext = map[string]string(carrier)
	}
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
		if st.GRPC != nil {
			if runtime == nil || runtime.execution.InvokeGRPC == nil {
				fail("native gRPC transport is unavailable", true)
				return art, fmt.Errorf("native gRPC transport is unavailable")
			}
			var selectedMutation *mutation
			if m != nil && index == m.Step {
				selectedMutation = m
			}
			input.Exchanges = art.Traffic
			grpcResult, grpcErr := runGRPCStep(ctx, st, selectedMutation, &input, cfg, runtime, call)
			if grpcResult.flow.ID != "" {
				art.Traffic = append(art.Traffic, grpcResult.flow)
			}
			art.Assertions = append(art.Assertions, grpcResult.assertions...)
			if grpcResult.verified {
				art.Exploration.RequestVerified = true
				art.SchemaValidation.Status = "expected_rejection"
			}
			if grpcResult.schemaFailed {
				art.SchemaValidation.Status = "fail"
			}
			if grpcResult.failed {
				fail(grpcResult.diagnostic, grpcResult.incomplete)
			}
			if grpcErr != nil {
				fail("gRPC request failed: "+grpcErr.Error(), true)
				return art, grpcErr
			}
			if grpcResult.failed {
				break
			}
			continue
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
		input.Operation = st.Operation
		sc := trace.SpanContextFromContext(ctx)
		if sc.IsValid() {
			tracecontext.Inject(r.Headers, sc)
			r.Headers.Del("X-Wiretap-Session-Id")
		}
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
		// Trace ownership is assigned by the runtime, never by fixture headers.
		// Inject again after callbacks so their headers cannot route dependency
		// evidence into an authored test or another generated case.
		if sc.IsValid() {
			tracecontext.Inject(r.Headers, sc)
			r.Headers.Del("X-Wiretap-Session-Id")
		}
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
		if sc.IsValid() {
			flow.TraceID = sc.TraceID().String()
			flow.RequestSpanID = sc.SpanID().String()
			flow.TraceContextSource = "injected"
			flow.TestCorrelation = "trace"
		}
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
	input.GRPCRequest = nil
	input.Operation = base.Operation
	if runtime != nil && begun && runtime.execution.SnapshotCase != nil {
		if e = runtime.execution.SnapshotCase(ctx, art); e != nil {
			fail("capture snapshot failed: "+e.Error(), true)
			return art, e
		}
	}
	input.Exchanges = art.Traffic
	input.Outcome = art.TestCase.Status
	checked, e := call(ctx, "check_case", &input)
	if e != nil {
		fail("check_case failed: "+e.Error(), true)
		return art, e
	}
	merge(&input, checked)

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
	for _, flow := range a.Traffic {
		message := flow.Request
		if phase == "response" {
			message = flow.Response
		}
		var groups []artifact.Headers
		if message.HTTP != nil {
			groups = append(groups, message.HTTP.Headers, message.HTTP.Trailers)
		}
		if message.GRPC != nil {
			groups = append(groups, message.GRPC.Metadata, message.GRPC.Trailers)
		}
		for _, headers := range groups {
			for key := range headers {
				if strings.EqualFold(key, name) {
					return true
				}
			}
		}
	}
	return false
}
