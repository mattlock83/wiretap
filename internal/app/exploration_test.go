package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/explore"
	"github.com/phrond/wiretap/internal/hooks"
)

const explorationSUTSchema = `openapi: 3.0.3
info: {title: Orders, version: 1.0.0}
paths:
  /orders:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [sku]
              properties:
                sku: {type: string, minLength: 2}
      responses:
        '200':
          description: accepted
          content:
            application/json:
              schema:
                type: object
                required: [accepted]
                properties:
                  accepted: {type: boolean}
        '400':
          description: invalid
          content:
            application/json:
              schema:
                type: object
                required: [error]
                properties:
                  error: {type: string}
`

type explorationFixture struct {
	app                 *App
	url, session, proxy string
	requests            atomic.Int32
	registration        map[string]any
	traceparent         string
}

func newExplorationFixture(t *testing.T, explicit ...bool) *explorationFixture {
	t.Helper()
	f := &explorationFixture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"available":true}`)
	}))
	t.Cleanup(upstream.Close)
	var dependencyURL string
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer runtime-only-secret" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
			return
		}
		var body map[string]any
		decodeErr := json.NewDecoder(r.Body).Decode(&body)
		sku, ok := body["sku"].(string)
		if decodeErr != nil || !ok || len(sku) < 2 {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":"invalid sku"}`)
			return
		}
		req, _ := http.NewRequestWithContext(r.Context(), "POST", dependencyURL+"/items?fixed=yes", strings.NewReader(`{"sku":"widget"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("traceparent", r.Header.Get("traceparent"))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			http.Error(w, "dependency request failed", 502)
			return
		}
		_, _ = io.WriteString(w, `{"accepted":true}`)
	}))
	t.Cleanup(sut.Close)
	control, a := dependencyCaptureRuntime(t)
	f.app, f.url = a, control.URL
	start := postDependencyJSON(t, f.url, "/v1/startRun", map[string]any{"proxy": map[string]any{"target_url": sut.URL}})
	f.proxy = start["sut_url"].(string)
	dependencyURL = f.proxy + "/__wiretap/dep/inventory"
	f.registration = map[string]any{
		"name": "orders happy path", "sut_base_url": sut.URL,
		"schemas":      []artifact.SchemaRef{{ID: "orders", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: explorationSUTSchema}}},
		"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: upstream.URL + "/api", UpstreamKind: "mock", Schema: &artifact.SchemaRef{ID: "inventory", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: dependencyCaptureInventorySchema}}}},
	}
	if len(explicit) > 0 && explicit[0] {
		f.traceparent = "00-11111111111111111111111111111111-2222222222222222-01"
		f.registration["explicit_session"] = true
		f.registration["trace_context"] = map[string]string{"traceparent": f.traceparent}
	}
	setup := postDependencyJSON(t, f.url, "/v1/setupTest", f.registration)
	f.session = setup["session_id"].(string)
	f.happy(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.stopExplorations(ctx)
	})
	return f
}

func (f *explorationFixture) happy(t *testing.T) {
	t.Helper()
	f.happyWithTrace(t, f.traceparent)
}
func (f *explorationFixture) happyWithTrace(t *testing.T, traceparent string) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.proxy+"/orders", strings.NewReader(`{"sku":"widget"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer runtime-only-secret")
	if traceparent != "" {
		req.Header.Set("traceparent", traceparent)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("happy path HTTP %d: %s", res.StatusCode, raw)
	}
}

func explorationPost(t *testing.T, f *explorationFixture, path string, payload any) explorationState {
	t.Helper()
	data := postDependencyJSON(t, f.url, path, payload)
	raw, _ := json.Marshal(data)
	var state explorationState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func (f *explorationFixture) start(t *testing.T, budget int, events ...string) explorationSelector {
	t.Helper()
	var scopes []explore.HookScope
	for _, event := range events {
		scopes = append(scopes, explore.HookScope{ID: event, Event: event, Operations: []string{"POST /orders"}})
	}
	state := explorationPost(t, f, "/v1/explore", exploreRequest{SessionID: f.session, Selection: explore.Selection{Operations: []string{"POST /orders"}}, Config: explore.Config{MaxCases: budget, HookScopes: scopes, HookTimeout: "2s"}})
	return explorationSelector{SessionID: f.session, ExplorationID: state.ID}
}

func (f *explorationFixture) pump(t *testing.T, selector explorationSelector, callback func(explorationAction, explore.HookInput) (hooks.Output, string)) explorationState {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		state := explorationPost(t, f, "/v1/explore/next", selector)
		if state.State != "running" {
			return state
		}
		if state.Action == nil {
			continue
		}
		var input explore.HookInput
		if err := json.Unmarshal(state.Action.Input, &input); err != nil {
			t.Fatal(err)
		}
		out, message := callback(*state.Action, input)
		postDependencyJSON(t, f.url, "/v1/explore/respond", explorationResponse{explorationSelector: selector, ActionID: state.Action.ID, Output: out, Error: message})
	}
	t.Fatal("exploration did not finish")
	return explorationState{}
}

func authHook(_ explorationAction, input explore.HookInput) (hooks.Output, string) {
	if input.Event == "before_request" {
		return hooks.Output{Headers: map[string]string{"Authorization": "Bearer runtime-only-secret"}}, ""
	}
	return hooks.Output{}, ""
}

func TestExploreActiveTestSharesBundleAndCapturesFreshDependencies(t *testing.T) {
	f := newExplorationFixture(t)
	selector := f.start(t, 0, "before_explore", "before_case", "before_request", "check_case", "after_case", "after_explore")
	events := map[string]int{}
	traces := map[string]bool{}
	state := f.pump(t, selector, func(action explorationAction, input explore.HookInput) (hooks.Output, string) {
		events[input.Event]++
		if input.Event == "before_explore" || input.Event == "after_explore" || input.Event == "before_case" || input.Event == "after_case" {
			// Fixture APIs and their downstream calls must not inflate evidence.
			f.happyWithTrace(t, input.TraceContext["traceparent"])
		}
		if input.Event == "before_case" {
			if input.TraceContext["traceparent"] == "" || traces[input.TraceContext["traceparent"]] {
				t.Errorf("case trace was missing or reused: %#v", input.TraceContext)
			}
			traces[input.TraceContext["traceparent"]] = true
		}
		if input.Event == "check_case" {
			if input.Mode == "baseline" && len(input.Exchanges) != 2 {
				t.Errorf("baseline check received %d flows, want provider and fresh dependency", len(input.Exchanges))
			}
			return hooks.Output{Checks: []hooks.Check{{Name: "fixture invariant", Passed: true}}}, ""
		}
		return authHook(action, input)
	})
	if state.State != "complete" || state.Result == nil || !state.Result.Complete || state.Result.Executed == 0 || state.Result.Failed != 0 {
		t.Fatalf("result = %+v", state)
	}
	if events["before_explore"] != 1 || events["after_explore"] != 1 || events["after_case"] != state.Result.Executed+state.Result.Baselines {
		t.Fatalf("lifecycle events = %v, result = %+v", events, state.Result)
	}
	run, arts, err := bundle.Load(f.app.currentBundle().Path())
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "open" || len(run.Pending) != 1 || len(arts) != state.Result.Executed+1 {
		t.Fatalf("open run state = %+v", run)
	}
	parentID := run.Pending[0].AttemptID
	for _, art := range arts {
		if art.Exploration == nil || art.Exploration.SourceAttemptID != parentID || art.Exploration.InvocationID != selector.ExplorationID || art.Exploration.SourceKind != "active_test" || art.Exploration.SourceRunID != run.RunID {
			t.Fatalf("invalid provenance: %+v", art.Exploration)
		}
		if art.Exploration.Mode == "baseline" {
			var provider, dependency *artifact.Flow
			for i := range art.Traffic {
				if art.Traffic[i].Channel == "test_to_sut" {
					provider = &art.Traffic[i]
				} else {
					dependency = &art.Traffic[i]
				}
			}
			if provider == nil || dependency == nil || dependency.ParentFlowID != provider.ID || dependency.TraceID != provider.TraceID {
				t.Fatalf("generated dependency is not linked: %+v", art.Traffic)
			}
		}
		raw, _ := json.Marshal(art)
		if bytes.Contains(raw, []byte("runtime-only-secret")) {
			t.Error("runtime authentication leaked into generated evidence")
		}
	}
	// A second invocation uses the same immutable authored seed and distinct IDs.
	second := f.start(t, 1, "before_request")
	secondResult := f.pump(t, second, authHook)
	if secondResult.State != "complete" || secondResult.Result.Complete || secondResult.Result.Skipped == 0 {
		t.Fatalf("explicit budget result = %+v", secondResult)
	}
	finished := postDependencyJSON(t, f.url, "/v1/teardownTest", map[string]any{"session_id": f.session, "status": "pass"})
	parent := dependencyArtifact(t, finished)
	if parent.TestCase.Status != "pass" || len(parent.Traffic) != 2 {
		t.Fatalf("parent capture was changed by exploration: %+v", parent)
	}
	bundlePath := f.app.currentBundle().Path()
	postDependencyJSON(t, f.url, "/v1/finishRun", map[string]any{})
	report, err := coverage.Generate(t.Context(), bundlePath, "Orders", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Contracts.Rejections == nil || report.Contracts.Rejections.Covered != state.Result.Generated || report.Contracts.Rejections.Failed != 0 {
		t.Fatalf("rejection coverage = %+v", report.Contracts.Rejections)
	}
}

func TestExploreHooksFailureAndCancellationStillCleanUp(t *testing.T) {
	for _, mode := range []string{"hook_error", "cancel", "teardown"} {
		t.Run(mode, func(t *testing.T) {
			f := newExplorationFixture(t)
			selector := f.start(t, 0, "before_case", "after_case", "after_explore")
			first := explorationPost(t, f, "/v1/explore/next", selector)
			if first.Action == nil || first.Action.Event != "before_case" {
				t.Fatalf("first state = %+v", first)
			}
			apiStatus(t, f.url, "/v1/finishRun", map[string]any{}, 409)
			apiStatus(t, f.url, "/v1/explore/next", explorationSelector{SessionID: "wrong-parent", ExplorationID: selector.ExplorationID}, 404)
			if mode == "hook_error" {
				reply := explorationResponse{explorationSelector: selector, ActionID: first.Action.ID, Error: "fixture creation failed"}
				postDependencyJSON(t, f.url, "/v1/explore/respond", reply)
				postDependencyJSON(t, f.url, "/v1/explore/respond", reply) // retry accepted exactly once
				reply.Error = "different result"
				apiStatus(t, f.url, "/v1/explore/respond", reply, 409)
			} else if mode == "cancel" {
				postDependencyJSON(t, f.url, "/v1/explore/cancel", selector)
			} else {
				apiStatus(t, f.url, "/v1/teardownTest", map[string]any{"session_id": f.session, "status": "pass"}, 409)
			}
			cleanups := map[string]int{}
			state := f.pump(t, selector, func(_ explorationAction, input explore.HookInput) (hooks.Output, string) {
				cleanups[input.Event]++
				return hooks.Output{}, ""
			})
			if state.State != "failed" && state.State != "cancelled" {
				t.Fatalf("state = %+v", state)
			}
			if cleanups["after_case"] != 1 || cleanups["after_explore"] != 1 {
				t.Fatalf("cleanup callbacks = %v", cleanups)
			}
			parent := dependencyArtifact(t, postDependencyJSON(t, f.url, "/v1/teardownTest", map[string]any{"session_id": f.session, "status": "pass"}))
			if parent.TestCase.Status != "fail" {
				t.Fatal("ignoring an exploration failure incorrectly passed the parent")
			}
			postDependencyJSON(t, f.url, "/v1/finishRun", map[string]any{})
		})
	}
}

func TestExploreRejectsUnknownScopeBeforeHooksOrRequests(t *testing.T) {
	f := newExplorationFixture(t)
	apiStatus(t, f.url, "/v1/explore", map[string]any{"session_id": f.session, "operations": []string{"POST /missing"}, "hooks": []map[string]string{{"id": "never", "event": "before_explore"}}}, 400)
	if f.requests.Load() != 1 {
		t.Fatal("invalid scope executed traffic")
	}
	f.happy(t) // failed preparation released the parent
	if f.requests.Load() != 2 {
		t.Fatal("parent remained paused")
	}
}

func TestExploreParallelParentsKeepHooksAndDependenciesIsolated(t *testing.T) {
	f := newExplorationFixture(t, true)
	secondTrace := "00-33333333333333333333333333333333-4444444444444444-01"
	f.registration["name"] = "second orders test"
	f.registration["trace_context"] = map[string]string{"traceparent": secondTrace}
	secondSetup := postDependencyJSON(t, f.url, "/v1/setupTest", f.registration)
	f.happyWithTrace(t, secondTrace)
	firstSession := f.session
	first := f.start(t, 1, "before_explore", "before_case", "before_request", "after_case", "after_explore")
	f.session = secondSetup["session_id"].(string)
	second := f.start(t, 1, "before_explore", "before_case", "before_request", "after_case", "after_explore")
	for _, item := range []struct {
		selector    explorationSelector
		parentTrace string
	}{{first, f.traceparent}, {second, secondTrace}} {
		state := f.pump(t, item.selector, func(action explorationAction, input explore.HookInput) (hooks.Output, string) {
			if input.Event == "before_explore" || input.Event == "after_explore" {
				if input.TraceContext["traceparent"] != item.parentTrace {
					t.Errorf("global callback lost parent trace: %+v", input.TraceContext)
				}
			}
			if input.Event != "before_request" {
				f.happyWithTrace(t, input.TraceContext["traceparent"])
			}
			return authHook(action, input)
		})
		if state.State != "complete" {
			t.Fatalf("parallel exploration = %+v", state)
		}
	}
	for _, session := range []string{firstSession, f.session} {
		parent := dependencyArtifact(t, postDependencyJSON(t, f.url, "/v1/teardownTest", map[string]any{"session_id": session, "status": "pass"}))
		if parent.TestCase.Status != "pass" || len(parent.Traffic) != 2 {
			t.Fatalf("fixture or other parent traffic leaked: %+v", parent)
		}
	}
	_, arts, err := bundle.Load(f.app.currentBundle().Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, art := range arts {
		if art.Exploration == nil {
			continue
		}
		for _, flow := range art.Traffic {
			if flow.TraceID == "" || flow.TraceID != art.TestCase.TraceID {
				t.Fatalf("cross-case traffic: flow=%+v test=%+v", flow, art.TestCase)
			}
		}
	}
	postDependencyJSON(t, f.url, "/v1/finishRun", map[string]any{})
}

func TestExploreShutdownAbandonsDisconnectedHooks(t *testing.T) {
	f := newExplorationFixture(t)
	selector := f.start(t, 0, "before_case", "after_case", "after_explore")
	first := explorationPost(t, f, "/v1/explore/next", selector)
	if first.Action == nil {
		t.Fatal("expected pending hook")
	}
	bundlePath := f.app.currentBundle().Path()
	if err := f.app.interruptRun(); err != nil {
		t.Fatal(err)
	}
	run, arts, err := bundle.Load(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "interrupted" || len(arts) != 1 || arts[0].TestCase.Status != "fail" {
		t.Fatalf("shutdown did not preserve failed generated evidence: %s, %d", run.RunState, len(arts))
	}
	state := explorationPost(t, f, "/v1/explore/next", selector)
	if state.State == "running" {
		t.Fatal(fmt.Sprintf("abandoned hook left invocation running: %+v", state))
	}
}
