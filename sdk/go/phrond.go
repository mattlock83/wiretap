// Package phrond integrates standard Go tests and HTTP clients with Phrond.
package phrond

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Runtime connects to an externally managed Phrond process. It never launches
// processes or changes global HTTP clients, providers, or propagators.
type Runtime struct {
	BaseURL        string
	Client         *http.Client
	TracerProvider trace.TracerProvider
}

// FinishRun finalizes the bundle and stops capture through the HTTP API. Call
// from suite cleanup after all tests/workers sharing this runtime have finished.
// It leaves the control process running so another capture run can start.
func (r *Runtime) FinishRun(ctx context.Context) error {
	return r.call(ctx, "/v1/finishRun", map[string]any{}, nil)
}

type TestOptions struct {
	SchemaRequired  *bool          `json:"schema_required,omitempty"`
	Redactions      []Redaction    `json:"redactions,omitempty"`
	APIID           string         `json:"api_id,omitempty"`
	SUTTargetKind   string         `json:"sut_target_kind,omitempty"`
	TestSource      map[string]any `json:"test_source,omitempty"`
	SUTSource       map[string]any `json:"sut_source,omitempty"`
	SUTBaseURL      string         `json:"sut_base_url,omitempty"`
	Schemas         []Schema       `json:"schemas,omitempty"`
	Dependencies    []Dependency   `json:"dependencies,omitempty"`
	ExplicitSession bool           `json:"explicit_session,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Meta            map[string]any `json:"meta,omitempty"`
	DrainTimeout    time.Duration  `json:"-"`
}

type Session struct {
	provider     trace.TracerProvider
	ID           string            `json:"session_id"`
	SUTURL       string            `json:"sut_url"`
	Dependencies map[string]string `json:"dependencies"`
	RunID        string            `json:"run_id"`
	BundlePath   string            `json:"bundle_path"`
	IndexPath    string            `json:"index_path"`
	ViewerPath   string            `json:"viewer_path"`
	Context      context.Context   `json:"-"`
}

var fallbackProvider = sdktrace.NewTracerProvider()

// StartTest registers a fresh root span for this attempt. Call after t.Parallel
// so a waiting test does not occupy the serial fallback session. Cleanup runs
// even after t.Fatal; register this before user cleanup so it runs last.
func (r *Runtime) StartTest(t testing.TB, options TestOptions) *Session {
	t.Helper()
	if options.DrainTimeout < 0 || options.DrainTimeout > time.Minute {
		t.Fatal("Phrond DrainTimeout must be between zero and one minute")
		return nil
	}
	provider := r.TracerProvider
	if provider == nil {
		provider = otel.GetTracerProvider()
	}
	ctx, span := provider.Tracer("phrond.tests").Start(context.Background(), t.Name(), trace.WithNewRoot())
	if !span.SpanContext().IsValid() {
		span.End()
		if r.TracerProvider != nil {
			t.Fatal("Phrond requires a tracer provider that creates valid span contexts")
			return nil
		}
		provider = fallbackProvider
		ctx, span = fallbackProvider.Tracer("phrond.tests").Start(context.Background(), t.Name(), trace.WithNewRoot())
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	payload := struct {
		TestOptions
		Name         string            `json:"name"`
		TraceContext map[string]string `json:"trace_context"`
	}{options, t.Name(), map[string]string{"traceparent": carrier["traceparent"], "tracestate": carrier["tracestate"]}}
	session := &Session{Context: ctx, provider: provider}
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSetup()
	if err := r.call(setupCtx, "/v1/setupTest", payload, session); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		t.Fatal(err)
		return nil
	}
	t.Cleanup(func() {
		status := "pass"
		if t.Failed() {
			status = "fail"
		} else if t.Skipped() {
			status = "skip"
		}
		timeout := options.DrainTimeout
		if timeout == 0 {
			timeout = 5 * time.Second
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
		defer cancel()
		var response struct {
			Capture struct {
				CaptureIncomplete bool `json:"capture_incomplete"`
				TestCase          struct {
					Status string `json:"status"`
				} `json:"testcase"`
			} `json:"capture"`
		}
		err := r.call(cleanupCtx, "/v1/teardownTest", map[string]any{"session_id": session.ID, "status": status, "drain_timeout_ms": timeout.Milliseconds()}, &response)
		if err == nil && (response.Capture.CaptureIncomplete || response.Capture.TestCase.Status == "fail") {
			err = fmt.Errorf("Phrond capture or validation failed for %s", t.Name())
		}
		if err != nil {
			span.RecordError(err)
			t.Error(err)
		}
		if t.Failed() {
			span.SetStatus(codes.Error, "test or capture failed")
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	})
	return session
}

// HTTPClient clones a client's configuration and binds its requests to this
// test. Pass a client whose transport has not already been instrumented. Code
// with existing otelhttp instrumentation can instead use Session.Context on
// its requests and keep its original client.
func (s *Session) HTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = boundTransport{ctx: s.Context, transport: otelhttp.NewTransport(base,
		otelhttp.WithPropagators(propagation.TraceContext{}), otelhttp.WithTracerProvider(s.provider))}
	return &clone
}

type boundTransport struct {
	ctx       context.Context
	transport http.RoundTripper
}

func (t boundTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Retain cancellation and values on the request while binding its parent.
	ctx := trace.ContextWithSpanContext(request.Context(), trace.SpanContextFromContext(t.ctx))
	return t.transport.RoundTrip(request.Clone(ctx))
}
func (r *Runtime) call(ctx context.Context, path string, payload, output any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	baseURL := r.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("PHROND_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18000"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := r.Client
	if client == nil {
		client = &http.Client{}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
		return fmt.Errorf("Phrond %s: HTTP %d: %s", path, res.StatusCode, body)
	}
	if output != nil {
		return json.NewDecoder(res.Body).Decode(output)
	}
	return nil
}
