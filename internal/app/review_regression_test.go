package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/core"
	phrond "github.com/phrond/evermock/sdk/go"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestReviewLateUntracedDependencyAfterDrainTimeout(t *testing.T) {
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer mock.Close()
	var dependencyURL string
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		res, err := http.Get(dependencyURL + "/items")
		if err != nil {
			t.Error(err)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusConflict {
			t.Errorf("late dependency status %d", res.StatusCode)
		}
		io.Copy(w, res.Body)
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	payload := map[string]any{"name": "old", "sut_base_url": sut.URL, "generate_schema": true, "trace_context": traceRegistration(81), "dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: mock.URL, Schema: permissiveDependencySchema()}}}
	old := postDependencyJSON(t, control.URL, "/v1/setupTest", payload)
	dependencyURL = old["dependencies"].(map[string]any)["inventory"].(string)
	go func() {
		defer close(completed)
		res, err := http.Get(old["sut_url"].(string))
		if err != nil {
			t.Error(err)
			return
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}()
	<-entered
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": old["session_id"], "drain_timeout_ms": 0})
	payload["name"] = "new"
	payload["trace_context"] = traceRegistration(82)
	apiStatus(t, control.URL, "/v1/setupTest", payload, http.StatusConflict)
	close(release)
	<-completed
	next := postDependencyJSON(t, control.URL, "/v1/setupTest", payload)
	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": next["session_id"]}))
	if len(art.Traffic) != 0 {
		t.Fatalf("late old dependency captured in new session: %#v", art.Traffic)
	}
}

func TestReviewShutdownClearsRetiredTraces(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	payload := map[string]any{"name": "old", "generate_schema": true, "trace_context": traceRegistration(90)}
	old := postDependencyJSON(t, control.URL, "/v1/setupTest", payload)
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": old["session_id"]})
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	postDependencyJSON(t, control.URL, "/v1/setupTest", payload)
}

func TestDrainDoesNotBlockOtherTestsOrLoseResponse(t *testing.T) {
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, "ok")
	}))
	defer sut.Close()
	cfg := core.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Artifact.OutputDir = t.TempDir()
	cfg.HTTP.WriteTimeout = 40 * time.Millisecond
	app, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewUnstartedServer(app.server.Handler)
	control.Config.WriteTimeout = app.server.WriteTimeout
	control.Start()
	defer control.Close()
	defer app.proxyComponent.Stop(t.Context())
	setup := func(n int) map[string]any {
		return postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
			"name": "parallel", "generate_schema": true, "sut_base_url": sut.URL,
			"explicit_session": true, "trace_context": traceRegistration(n),
		})
	}
	first := setup(101)
	go func() {
		defer close(completed)
		requestStatus(t, first["sut_url"].(string), traceRegistration(101)["traceparent"], "", http.StatusOK)
	}()
	<-entered
	teardownDone := make(chan error, 1)
	go func() {
		data, _ := json.Marshal(map[string]any{"session_id": first["session_id"], "drain_timeout_ms": 1500})
		res, err := http.Post(control.URL+"/v1/teardownTest", "application/json", bytes.NewReader(data))
		if err == nil {
			defer res.Body.Close()
			_, err = io.ReadAll(res.Body)
		}
		teardownDone <- err
	}()
	// Observe admission closing before testing the independent control paths.
	deadline := time.Now().Add(time.Second)
	for {
		app.sessionsMu.Lock()
		closing := app.sessions[first["session_id"].(string)].closing
		app.sessionsMu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("teardown did not start")
		}
		time.Sleep(time.Millisecond)
	}
	apiStatus(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": first["session_id"]}, http.StatusConflict)
	next := setup(102)
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": next["session_id"]})
	time.Sleep(80 * time.Millisecond) // Exceed the actual server's normal write deadline.
	close(release)
	<-completed
	if err := <-teardownDone; err != nil {
		t.Fatalf("drain response lost: %v", err)
	}
}

func TestGoSDKCapturesParallelTestsThroughRealRuntime(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer mock.Close()
	var dependencyURL string
	props := propagation.TraceContext{}
	outbound := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithPropagators(props))}
	sut := httptest.NewServer(otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), "GET", dependencyURL+"/items", nil)
		res, err := outbound.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer res.Body.Close()
		io.Copy(w, res.Body)
	}), "sut", otelhttp.WithPropagators(props)))
	defer sut.Close()
	control, app := dependencyCaptureRuntime(t)
	info := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{"proxy": map[string]string{"target_url": sut.URL}})
	dependencyURL = info["sut_url"].(string) + "/__phrond/dep/inventory"
	runtime := phrond.Runtime{BaseURL: control.URL}
	var mu sync.Mutex
	expected := map[string]string{}
	t.Run("parallel", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				session := runtime.StartTest(t, phrond.TestOptions{SUTBaseURL: sut.URL, GenerateSchema: true, ExplicitSession: true,
					Dependencies: []phrond.Dependency{{AssetID: "inventory", UpstreamURL: mock.URL, Schema: &phrond.Schema{ID: "inventory", Type: "openapi", Source: phrond.SchemaSource{Type: "inline", Content: permissiveDependencySchema().Source.Content}}}},
				})
				mu.Lock()
				expected[t.Name()] = trace.SpanContextFromContext(session.Context).TraceID().String()
				mu.Unlock()
				res, err := session.HTTPClient(http.DefaultClient).Get(session.SUTURL + "/orders")
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				if string(body) != "ok" {
					t.Fatalf("unexpected response %s", body)
				}
			})
		}
	})
	root := app.currentBundle().Path()
	if err := runtime.FinishRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, captures, err := bundle.Load(root)
	if err != nil || len(captures) != 2 {
		t.Fatalf("expected two committed attempts: %d %v", len(captures), err)
	}
	if run.RunState != "complete" {
		t.Fatalf("suite cleanup did not finalize the bundle: %s", run.RunState)
	}
	for _, doc := range captures {
		if doc.TestCase.Status != "pass" || len(doc.Traffic) != 2 || doc.TestCase.TraceID != expected[doc.TestCase.Name] {
			t.Fatalf("incorrect SDK artifact: %#v", doc)
		}
		for _, flow := range doc.Traffic {
			if flow.TraceID != doc.TestCase.TraceID || flow.TestCorrelation != "trace" {
				t.Fatalf("lost SDK propagation: %#v", flow)
			}
		}
	}
}

func TestUnknownTracePreservedAndSelectorsRejected(t *testing.T) {
	foreign := traceRegistration(900)["traceparent"]
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") != foreign {
			t.Error("foreign context overwritten")
		}
		io.WriteString(w, "ok")
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	first := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "first", "sut_base_url": sut.URL, "generate_schema": true, "explicit_session": true, "trace_context": traceRegistration(901)})
	second := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "second", "generate_schema": true, "explicit_session": true, "trace_context": traceRegistration(902)})
	requestStatus(t, first["sut_url"].(string), foreign, first["session_id"].(string), 200)
	scoped := first["sut_url"].(string) + "/__phrond/session/" + first["session_id"].(string) + "/dep/inventory"
	requestStatus(t, scoped, traceRegistration(902)["traceparent"], "", 409)
	doc := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": first["session_id"]}))
	if len(doc.CaptureDiagnostics) != 1 || len(doc.Traffic) != 1 || doc.Traffic[0].TraceID != fmt.Sprintf("%032x", 900) || doc.Traffic[0].TestCorrelation != "explicit_session" {
		t.Fatalf("missing mismatch evidence: %#v", doc)
	}
	requestStatus(t, second["sut_url"].(string), traceRegistration(901)["traceparent"], second["session_id"].(string), 409)
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": second["session_id"]})
}
