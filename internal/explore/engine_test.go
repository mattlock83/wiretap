package explore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/hooks"
	"go.opentelemetry.io/otel/trace"
)

func activeSeed(t *testing.T, schema string) (*bundle.Writer, *artifact.Artifact) {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Configure(bundle.Identity{APIID: "orders", SUTTargetKind: "service"}); err != nil {
		t.Fatal(err)
	}
	test := artifact.TestCase{ID: "parent", Name: "creates an order", StartedAt: time.Now().UTC(), Status: "running"}
	refs, err := w.Register(test, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: schema}}})
	if err != nil {
		t.Fatal(err)
	}
	seed := &artifact.Artifact{Version: 1, TestCase: test, Schemas: refs, Traffic: []artifact.Flow{{ID: "seed-request", Channel: "test_to_sut", Protocol: "http", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://original/orders?count=2", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: seedBody}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201}}}}}
	return w, seed
}
func servingSpec(t *testing.T, p *Plan) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		captured := request{Method: r.Method, URL: "http://provider" + r.URL.String(), Headers: r.Header, Body: raw}
		w.Header().Set("Content-Type", "application/json")
		if p.spec.validate(captured) != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid input"}`)
		} else {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new"}`)
		}
	}))
}
func TestPrepareScopedSnapshotAndAmbiguousCalls(t *testing.T) {
	_, seed := activeSeed(t, testSpec)
	p, err := Prepare(seed, "orders", Selection{Operations: []string{"post /orders"}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	seed.Traffic[0].Request.HTTP.Body.Text = `{"changed":true}`
	if string(p.steps[0].Request.Body) != seedBody {
		t.Fatal("plan did not snapshot seed")
	}
	if p.cfg.MaxCases != 0 {
		t.Fatal("default unexpectedly limits cases")
	}
	_, seed = activeSeed(t, testSpec)
	duplicate := seed.Traffic[0]
	duplicate.ID = "repeated"
	seed.Traffic = append(seed.Traffic, duplicate)
	if _, err = Prepare(seed, "orders", Selection{}, Config{}); err == nil || !strings.Contains(err.Error(), "flow_id") {
		t.Fatalf("ambiguous call accepted: %v", err)
	}
	if _, err = Prepare(seed, "orders", Selection{FlowID: "repeated"}, Config{}); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []Selection{{Operations: []string{"POST /missing"}}, {FlowID: "missing"}, {Operations: []string{"/orders"}}} {
		if _, err = Prepare(seed, "orders", selection, Config{}); err == nil {
			t.Fatalf("selection accepted: %+v", selection)
		}
	}
}
func TestExecuteInOpenBundleLifecycleAndUnlimitedDefault(t *testing.T) {
	w, seed := activeSeed(t, testSpec)
	var scopes []HookScope
	for _, event := range []string{"before_explore", "before_case", "before_request", "check_case", "after_case", "after_explore"} {
		scopes = append(scopes, HookScope{ID: event, Event: event, Operations: []string{"POST /orders"}})
	}
	scopes = append(scopes, HookScope{ID: "second-setup", Event: "before_case"})
	p, err := Prepare(seed, "orders", Selection{}, Config{HookScopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	server := servingSpec(t, p)
	defer server.Close()
	var events []string
	begin, end, snapshots := 0, 0, 0
	active := false
	execution := Execution{Writer: w, Target: server.URL, InvocationID: "explore-one", ParentAttemptID: seed.TestCase.ID,
		BeginCase: func(ctx context.Context, test artifact.TestCase) (context.Context, error) {
			begin++
			active = true
			return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled})), nil
		},
		SnapshotCase: func(ctx context.Context, art *artifact.Artifact) error { snapshots++; return nil },
		EndCase: func(ctx context.Context, art *artifact.Artifact) error {
			if !active {
				t.Error("capture ended twice")
			}
			end++
			active = false
			return nil
		},
		CallHook: func(ctx context.Context, in HookInput) (hooks.Output, error) {
			events = append(events, in.HookID)
			if in.Event == "before_explore" || in.Event == "after_explore" {
				if active {
					t.Error("explore hook ran inside case")
				}
				return hooks.Output{}, nil
			}
			if !active || in.TraceContext["traceparent"] == "" {
				t.Errorf("missing isolated case context: %+v", in)
			}
			if in.Operation != "POST /orders" {
				t.Fatalf("wrong scope: %q", in.Operation)
			}
			switch in.HookID {
			case "before_case":
				return hooks.Output{State: map[string]any{"fresh": true}}, nil
			case "second-setup":
				if in.State["fresh"] != true {
					t.Error("scoped outputs did not compose")
				}
			case "before_request":
				return hooks.Output{Headers: map[string]string{"Traceparent": "00-22222222222222222222222222222222-3333333333333333-01", "Tracestate": "other=stale", "X-Wiretap-Session-Id": "parent"}}, nil
			case "check_case":
				for _, flow := range in.Exchanges {
					if flow.TraceID != "01000000000000000000000000000000" || flow.RequestSpanID != "0100000000000000" || flow.TraceContextSource != "injected" || flow.TestCorrelation != "trace" {
						t.Fatalf("missing generated request trace metadata: %+v", flow)
					}
					headers := http.Header(flow.Request.HTTP.Headers)
					if headers.Get("Traceparent") != in.TraceContext["traceparent"] || headers.Get("Tracestate") != "" || headers.Get("X-Wiretap-Session-Id") != "" {
						t.Fatalf("callback escaped generated trace ownership: %v", headers)
					}
				}
				if snapshots != begin {
					t.Error("case wasn't drained before checks")
				}
				return hooks.Output{Checks: []hooks.Check{{Name: "side effects", Passed: true}}}, nil
			}
			return hooks.Output{}, nil
		}}
	result, err := Execute(t.Context(), p, execution)
	if err != nil || result.Failed != 0 || !result.Complete || result.Executed != p.Generated() {
		t.Fatalf("result %+v: %v", result, err)
	}
	if begin != p.Generated()+1 || end != begin || snapshots != begin {
		t.Fatalf("lifecycle begin=%d snapshot=%d end=%d", begin, snapshots, end)
	}
	if len(w.Snapshot().Pending) != 1 || w.Snapshot().Pending[0].AttemptID != seed.TestCase.ID {
		t.Fatal("executor finalized or displaced parent")
	}
	if events[0] != "before_explore" || events[len(events)-1] != "after_explore" {
		t.Fatal(events)
	}
	_, captures, err := bundle.Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range captures {
		if a.Exploration.SourceAttemptID != seed.TestCase.ID || a.Exploration.InvocationID != "explore-one" || !strings.HasPrefix(a.TestCase.ID, "explore-one-") {
			t.Fatalf("missing provenance %+v", a)
		}
	}
	// A second call shares coverage keys but has distinct immutable attempts.
	execution.InvocationID = "explore-two"
	if _, err = Execute(t.Context(), p, execution); err != nil {
		t.Fatal(err)
	}
}
func TestExecuteHookFailureStillCleansUpAndKeepsEvidence(t *testing.T) {
	for _, failedEvent := range []string{"before_explore", "before_case", "before_request", "check_case", "after_case", "after_explore"} {
		t.Run(failedEvent, func(t *testing.T) {
			w, seed := activeSeed(t, testSpec)
			var scopes []HookScope
			for _, event := range []string{"before_explore", "before_case", "before_request", "check_case", "after_case", "after_explore"} {
				scopes = append(scopes, HookScope{ID: event, Event: event})
			}
			p, err := Prepare(seed, "orders", Selection{}, Config{HookScopes: scopes, MaxCases: 1})
			if err != nil {
				t.Fatal(err)
			}
			server := servingSpec(t, p)
			defer server.Close()
			var afterExplore, afterCase, ended bool
			_, err = Execute(t.Context(), p, Execution{Writer: w, Target: server.URL, InvocationID: "failure", ParentAttemptID: "parent", BeginCase: func(ctx context.Context, _ artifact.TestCase) (context.Context, error) { return ctx, nil }, EndCase: func(ctx context.Context, _ *artifact.Artifact) error {
				if ctx.Err() != nil {
					t.Error("cleanup context cancelled")
				}
				ended = true
				return nil
			}, CallHook: func(ctx context.Context, in HookInput) (hooks.Output, error) {
				if in.Event == "after_explore" {
					afterExplore = true
				}
				if in.Event == "after_case" {
					afterCase = true
				}
				if in.Event == failedEvent {
					return hooks.Output{}, errors.New("fixture failure")
				}
				if in.Event == "check_case" {
					return hooks.Output{Checks: []hooks.Check{{Name: "valid", Passed: true}}}, nil
				}
				return hooks.Output{}, nil
			}})
			if err == nil || !afterExplore {
				t.Fatalf("error=%v after_explore=%v", err, afterExplore)
			}
			if failedEvent != "before_explore" && (!afterCase || !ended) {
				t.Fatalf("case cleanup missing: after_case=%v end=%v", afterCase, ended)
			}
			if len(w.Snapshot().Pending) != 1 {
				t.Fatal("generated pending attempts leaked")
			}
		})
	}
}
func TestScopeRetainsPrerequisitesAndRequestHookUsesActualOperation(t *testing.T) {
	schema := testSpec + `  /ready:
    get:
      responses:
        '200': {description: ready}
`
	w, seed := activeSeed(t, schema)
	prerequisite := artifact.Flow{ID: "setup", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://old/ready"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}}
	seed.Traffic = append([]artifact.Flow{prerequisite}, seed.Traffic...)
	p, err := Prepare(seed, "orders", Selection{Operations: []string{"POST /orders"}}, Config{MaxCases: 1, HookScopes: []HookScope{{ID: "ready", Event: "before_request", Operations: []string{"GET /ready"}}}})
	if err != nil {
		t.Fatal(err)
	}
	requests, hooksCalled := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/ready" {
			w.WriteHeader(200)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if p.spec.validate(request{Method: r.Method, URL: "http://provider" + r.URL.String(), Headers: r.Header, Body: raw}) != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid"}`)
		} else {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new"}`)
		}
	}))
	defer server.Close()
	result, err := Execute(t.Context(), p, Execution{Writer: w, Target: server.URL, InvocationID: "scoped", ParentAttemptID: "parent", CallHook: func(_ context.Context, in HookInput) (hooks.Output, error) {
		hooksCalled++
		if in.Operation != "GET /ready" {
			t.Fatal(in.Operation)
		}
		return hooks.Output{}, nil
	}})
	if err != nil || result.Executed != 1 || requests != 4 || hooksCalled != 2 || result.Complete || result.Skipped == 0 {
		t.Fatalf("result=%+v requests=%d hooks=%d err=%v", result, requests, hooksCalled, err)
	}
}

func TestUnlimitedDefaultExecutesMoreThanHundredCases(t *testing.T) {
	var parameters strings.Builder
	values := []string{}
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&parameters, "        - {name: p%d, in: query, required: true, schema: {type: integer, minimum: 1}}\n", i)
		values = append(values, fmt.Sprintf("p%d=1", i))
	}
	schema := "openapi: 3.0.3\ninfo: {title: Many, version: '1'}\npaths:\n  /orders:\n    post:\n      parameters:\n" + parameters.String() + "      responses:\n        '201': {description: success}\n        '400': {description: invalid}\n"
	w, seed := activeSeed(t, schema)
	seed.Traffic[0].Request.HTTP.URL = "http://old/orders?" + strings.Join(values, "&")
	seed.Traffic[0].Request.HTTP.Body = nil
	p, err := Prepare(seed, "orders", Selection{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Generated() <= 100 {
		t.Fatalf("expected more than old default, got %d", p.Generated())
	}
	server := servingSpec(t, p)
	defer server.Close()
	result, err := Execute(t.Context(), p, Execution{Writer: w, Target: server.URL, InvocationID: "all", ParentAttemptID: "parent"})
	if err != nil || !result.Complete || result.Executed != p.Generated() || result.Failed != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPlanSnapshotsHookConfiguration(t *testing.T) {
	_, seed := activeSeed(t, testSpec)
	cfg := Config{HookScopes: []HookScope{{ID: "setup", Event: "before_case", Operations: []string{"post /orders"}}}}
	p, err := Prepare(seed, "orders", Selection{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HookScopes[0].Operations[0] != "post /orders" {
		t.Fatal("Prepare mutated caller configuration")
	}
	cfg.HookScopes[0].Operations[0] = "GET /elsewhere"
	if p.cfg.HookScopes[0].Operations[0] != "POST /orders" {
		t.Fatal("plan retains mutable caller scope")
	}
}

func TestIncompleteGeneratedResponseStopsRemainingCases(t *testing.T) {
	w, seed := activeSeed(t, testSpec)
	p, err := Prepare(seed, "orders", Selection{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	calls, cleanups := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"fresh"}`)
			return
		}
		// A declared body longer than its transmitted prefix produces a real
		// incomplete exchange, without making test timing part of the assertion.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":"cut off`)
	}))
	defer server.Close()
	result, err := Execute(t.Context(), p, Execution{Writer: w, Target: server.URL, InvocationID: "incomplete", ParentAttemptID: "parent", BeginCase: func(ctx context.Context, _ artifact.TestCase) (context.Context, error) { return ctx, nil }, EndCase: func(context.Context, *artifact.Artifact) error { cleanups++; return nil }})
	if err == nil || !strings.Contains(err.Error(), "incomplete") || result.Executed != 1 || result.Failed != 1 || result.Complete || calls != 2 || cleanups != 2 {
		t.Fatalf("result=%+v calls=%d cleanups=%d err=%v", result, calls, cleanups, err)
	}
}
