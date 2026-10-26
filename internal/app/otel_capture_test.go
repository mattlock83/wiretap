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
	"sync"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/tracecontext"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func traceRegistration(n int) map[string]string {
	return map[string]string{"traceparent": fmt.Sprintf("00-%032x-%016x-00", n, n), "tracestate": "vendor=value"}
}
func apiStatus(t *testing.T, base, path string, payload any, want int) {
	t.Helper()
	data, _ := json.Marshal(payload)
	res, err := http.Post(base+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s status %d want %d: %s", path, res.StatusCode, want, body)
	}
}
func requestStatus(t *testing.T, target, parent, session string, want int) {
	t.Helper()
	req, _ := http.NewRequest("GET", target, nil)
	if parent != "" {
		req.Header.Set("traceparent", parent)
	}
	if session != "" {
		req.Header.Set("X-Wiretap-Session-Id", session)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("request status %d want %d: %s", res.StatusCode, want, body)
	}
}

func TestOTelParallelSessionsRouteBeforeDependencySelection(t *testing.T) {
	// AlwaysOff proves capture does not depend on exporting/sampling spans.
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	props := propagation.TraceContext{}
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(provider), otelhttp.WithPropagators(props))}
	var dependencyURL string
	var mu sync.Mutex
	observed := map[string]int{}
	mock := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Wiretap-Session-Id") != "" {
				t.Error("session selection header reached mock")
			}
			sc := tracecontext.Extract(r.Header)
			if !sc.IsValid() || sc.IsSampled() {
				t.Error("missing unsampled W3C context")
			}
			mu.Lock()
			observed[label+":"+sc.TraceID().String()]++
			mu.Unlock()
			io.WriteString(w, label)
		}))
	}
	first, second := mock("first"), mock("second")
	defer first.Close()
	defer second.Close()
	sut := httptest.NewServer(otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), "GET", dependencyURL+"/items", nil)
		res, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer res.Body.Close()
		io.Copy(w, res.Body)
	}), "sut", otelhttp.WithTracerProvider(provider), otelhttp.WithPropagators(props)))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	info := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{"proxy": map[string]string{"target_url": sut.URL}})
	proxyURL := info["sut_url"].(string)
	dependencyURL = proxyURL + "/__wiretap/dep/inventory"
	sessions := make([]map[string]any, 2)
	for i, upstream := range []string{first.URL, second.URL} {
		sessions[i] = postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
			"name": fmt.Sprintf("attempt-%d", i), "schema_required": false, "explicit_session": true, "sut_base_url": sut.URL,
			"trace_context": traceRegistration(i + 1), "dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: upstream, Schemas: []artifact.SchemaRef{*permissiveDependencySchema()}}},
		})
		if sessions[i]["dependencies"].(map[string]any)["inventory"] != dependencyURL {
			t.Fatal("traced dependency URL must stay stable")
		}
	}
	// Active configuration cannot be redirected by run startup.
	apiStatus(t, control.URL, "/v1/startRun", map[string]any{"proxy": map[string]any{"target_url": first.URL}}, 400)
	apiStatus(t, control.URL, "/v1/finishRun", map[string]any{}, 409)
	requestStatus(t, proxyURL, traceRegistration(1)["traceparent"], sessions[1]["session_id"].(string), 409)
	requestStatus(t, dependencyURL+"/items", "", "", 409)
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			root, _ := (tracecontext.Context{Traceparent: traceRegistration(i + 1)["traceparent"]}).Validate()
			req, _ := http.NewRequestWithContext(trace.ContextWithSpanContext(context.Background(), root), "GET", proxyURL+"/orders", nil)
			res, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			want := []string{"first", "second"}[i]
			if string(body) != want {
				t.Errorf("test %d got %s want %s", i, body, want)
			}
		}(i)
	}
	wg.Wait()
	for i, session := range sessions {
		art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]}))
		if len(art.Traffic) != 2 || art.TestCase.TraceID != fmt.Sprintf("%032x", i+1) || art.TestCase.RootSpanID != fmt.Sprintf("%016x", i+1) {
			t.Fatalf("wrong attempt identity: %#v", art)
		}
		for _, flow := range art.Traffic {
			if flow.TraceID != art.TestCase.TraceID || flow.RequestSpanID == "" || flow.TraceContextSource != "received" || flow.TestCorrelation != "trace" {
				t.Errorf("lost context: %#v", flow)
			}
			if flow.Channel == "sut_to_dependency" && (flow.ParentFlowID == "" || flow.ParentCorrelation != "trace_unique") {
				t.Errorf("lost parent inference: %#v", flow)
			}
		}
	}
	// A finished trace cannot be registered again or fall into the next default.
	apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "reuse", "schema_required": false, "trace_context": traceRegistration(1)}, 409)
	next := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "next", "schema_required": false})
	requestStatus(t, proxyURL, traceRegistration(1)["traceparent"], "", 409)
	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": next["session_id"]}))
	if len(art.Traffic) != 0 {
		t.Fatal("late request contaminated next test")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Fatalf("wrong mock routing: %#v", observed)
	}
}

func TestTraceRegistrationValidationAndImplicitIsolation(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	for _, parent := range []string{"bad", "00-00000000000000000000000000000000-1111111111111111-01", "00-11111111111111111111111111111111-0000000000000000-01"} {
		apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "invalid", "schema_required": false, "trace_context": map[string]string{"traceparent": parent}}, 400)
	}
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "new", "schema_required": false})
	apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "overlap", "schema_required": false}, 409)
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]})

}

func TestCaptureDrainAndTimeoutDoNotReassignInFlightTraffic(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; io.WriteString(w, "done") }))
			defer sut.Close()
			control, _ := dependencyCaptureRuntime(t)
			session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "slow", "sut_base_url": sut.URL, "schema_required": false, "trace_context": traceRegistration(5)})
			go func() {
				defer close(completed)
				res, err := http.Get(session["sut_url"].(string))
				if err == nil {
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
				} else {
					t.Error(err)
				}
			}()
			<-entered
			done := make(chan map[string]any, 1)
			go func() {
				ms := 5000
				if timeout {
					ms = 0
				}
				done <- postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "drain_timeout_ms": ms})
			}()
			if timeout {
				art := dependencyArtifact(t, <-done)
				if !art.CaptureIncomplete || art.TestCase.Status != "fail" {
					t.Fatal("timeout must produce incomplete failed artifact")
				}
				apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "next", "schema_required": false}, http.StatusConflict)
				close(release)
				<-completed
				next := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "next", "schema_required": false})
				art = dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": next["session_id"]}))
				if len(art.Traffic) != 0 {
					t.Fatal("completion recorded into next session")
				}
			} else {
				select {
				case <-done:
					t.Fatal("teardown did not wait for accepted request")
				case <-time.After(20 * time.Millisecond):
				}
				close(release)
				<-completed
				art := dependencyArtifact(t, <-done)
				if art.CaptureIncomplete || len(art.Traffic) != 1 || art.Traffic[0].TraceContextSource != "injected" || art.Traffic[0].TestCorrelation != "active_session" {
					t.Fatalf("drained capture wrong: %#v", art)
				}
			}
		})
	}
}

func TestParentInferenceNeverChoosesAnArbitraryOrFutureRequest(t *testing.T) {
	start := time.Now()
	parent := func(id string, offset time.Duration) artifact.Flow {
		return artifact.Flow{ID: id, Channel: "test_to_sut", TraceID: "same", StartedAt: start.Add(offset), DurationMS: 20}
	}
	for _, tc := range []struct {
		name                   string
		parents                []artifact.Flow
		depTrace, reason, want string
	}{
		{"shared overlapping", []artifact.Flow{parent("one", 0), parent("two", time.Millisecond)}, "same", "ambiguous", ""},
		{"future", []artifact.Flow{parent("future", 10*time.Millisecond)}, "same", "unmatched", ""},
		{"foreign", []artifact.Flow{parent("one", 0)}, "foreign", "unmatched", ""},
		{"untraced", []artifact.Flow{parent("one", 0)}, "", "timing", "one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flows := append(tc.parents, artifact.Flow{ID: "dep", Channel: "sut_to_dependency", TraceID: tc.depTrace, StartedAt: start.Add(2 * time.Millisecond)})
			linkCausality(flows)
			dep := flows[len(flows)-1]
			if dep.ParentCorrelation != tc.reason || dep.ParentFlowID != tc.want {
				t.Fatalf("unexpected inference: %#v", dep)
			}
		})
	}
}

func TestSerialUninstrumentedDependencyRemainsUntraced(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") != "" {
			t.Error("fabricated propagation")
		}
		io.WriteString(w, "ok")
	}))
	defer mock.Close()
	var dependencyURL string
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := http.Get(dependencyURL + "/items")
		if err != nil {
			t.Error(err)
			return
		}
		defer res.Body.Close()
		io.Copy(w, res.Body)
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "serial", "sut_base_url": sut.URL, "schema_required": false, "trace_context": traceRegistration(8), "dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: mock.URL, Schemas: []artifact.SchemaRef{*permissiveDependencySchema()}}}})
	dependencyURL = session["dependencies"].(map[string]any)["inventory"].(string)
	requestStatus(t, session["sut_url"].(string), "invalid", "", 200)
	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]}))
	for _, flow := range art.Traffic {
		if strings.HasPrefix(flow.Channel, "sut_to_") && (flow.TraceID != "" || flow.ParentCorrelation != "timing" || flow.TestCorrelation != "active_session") {
			t.Fatalf("untraced dependency mislabeled: %#v", flow)
		}
	}
}
