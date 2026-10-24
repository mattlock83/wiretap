// Package wiretap integrates standard Go tests with HTTP and gRPC capture.
package wiretap

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

// Runtime connects to an externally managed Wiretap process. It never launches
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

// OutputOptions selects exports made by the suite owner after all workers finish.
// Omit these options to inherit the runtime project's outputs configuration.
type OutputOptions struct {
	Coverage       []string        `json:"coverage,omitempty"`
	Postman        bool            `json:"postman,omitempty"`
	PostmanOptions *PostmanOptions `json:"postman_options,omitempty"`
}
type PostmanOptions struct {
	OmitIncomplete bool                         `json:"omit_incomplete,omitempty"`
	GRPC           map[string]PostmanGRPCTarget `json:"grpc,omitempty"`
}
type PostmanGRPCTarget struct {
	URL       string `json:"url,omitempty"`
	ProtoRoot string `json:"proto_root,omitempty"`
	ProtoFile string `json:"proto_file,omitempty"`
}
type OutputResult struct {
	PostmanFiles     []string `json:"postman_files,omitempty"`
	PostmanOmissions []struct {
		AttemptID string `json:"attempt_id"`
		FlowID    string `json:"flow_id"`
		Protocol  string `json:"protocol"`
		Message   string `json:"message"`
	} `json:"postman_omissions,omitempty"`
	BundlePath string            `json:"bundle_path"`
	Coverage   map[string]string `json:"coverage"`
	Postman    []string          `json:"postman"`
	Errors     []struct {
		Format  string `json:"format"`
		Message string `json:"message"`
	} `json:"errors"`
}
type FinishResult struct {
	Status     string        `json:"status"`
	RunID      string        `json:"run_id"`
	BundlePath string        `json:"bundle_path"`
	Outputs    *OutputResult `json:"outputs,omitempty"`
}

// FinishRunWithOutputs returns exact artifact paths, including available outputs
// when a different export fails. The non-nil error still fails suite cleanup.
func (r *Runtime) FinishRunWithOutputs(ctx context.Context, options *OutputOptions) (*FinishResult, error) {
	result := &FinishResult{}
	err := r.call(ctx, "/v1/finishRun", struct {
		Outputs *OutputOptions `json:"outputs,omitempty"`
	}{options}, result)
	return result, err
}

type TestIdentity struct {
	ID             string `json:"id"`
	Variant        string `json:"variant,omitempty"`
	AttemptOrdinal *int   `json:"attempt_ordinal,omitempty"`
}
type TestOptions struct {
	Identity        *TestIdentity  `json:"test_identity,omitempty"`
	Application     *Application   `json:"application,omitempty"`
	SchemaRequired  *bool          `json:"schema_required,omitempty"`
	APIID           string         `json:"api_id,omitempty"`
	SUTTargetKind   string         `json:"sut_target_kind,omitempty"`
	TestSource      map[string]any `json:"test_source,omitempty"`
	SUTSource       map[string]any `json:"sut_source,omitempty"`
	SUTBaseURL      string         `json:"sut_base_url,omitempty"`
	SUTProtocol     string         `json:"sut_protocol,omitempty"`
	Schemas         []Schema       `json:"schemas,omitempty"`
	Dependencies    []Dependency   `json:"dependencies,omitempty"`
	ExplicitSession bool           `json:"explicit_session,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Meta            map[string]any `json:"meta,omitempty"`
	DrainTimeout    time.Duration  `json:"-"`
	// SetupTimeout includes first-test contract discovery for every boundary.
	// Zero uses 30 seconds. Increase it for larger or slower gRPC inventories.
	SetupTimeout time.Duration `json:"-"`
}

type Session struct {
	provider     trace.TracerProvider
	ID           string            `json:"session_id"`
	SUTURL       string            `json:"sut_url"`
	Dependencies map[string]string `json:"dependencies"`
	RunID        string            `json:"run_id"`
	BundlePath   string            `json:"bundle_path"`
	Context      context.Context   `json:"-"`
}

var fallbackProvider = sdktrace.NewTracerProvider()

// StartTest registers a fresh root span for this attempt. Call after t.Parallel
// so a waiting test does not occupy the serial fallback session. Cleanup runs
// even after t.Fatal; register this before user cleanup so it runs last.
func (r *Runtime) StartTest(t testing.TB, options TestOptions) *Session {
	t.Helper()
	if options.DrainTimeout < 0 || options.DrainTimeout > time.Minute {
		t.Fatal("Wiretap DrainTimeout must be between zero and one minute")
		return nil
	}
	if options.SetupTimeout < 0 {
		t.Fatal("Wiretap SetupTimeout must not be negative")
		return nil
	}
	provider := r.TracerProvider
	if provider == nil {
		provider = otel.GetTracerProvider()
	}
	ctx, span := provider.Tracer("wiretap.tests").Start(context.Background(), t.Name(), trace.WithNewRoot())
	if !span.SpanContext().IsValid() {
		span.End()
		if r.TracerProvider != nil {
			t.Fatal("Wiretap requires a tracer provider that creates valid span contexts")
			return nil
		}
		provider = fallbackProvider
		ctx, span = fallbackProvider.Tracer("wiretap.tests").Start(context.Background(), t.Name(), trace.WithNewRoot())
	}
	if options.Identity == nil && len(t.Name()) <= 256 {
		options.Identity = &TestIdentity{ID: t.Name()}
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	payload := struct {
		TestOptions
		Name         string            `json:"name"`
		TraceContext map[string]string `json:"trace_context"`
	}{options, t.Name(), map[string]string{"traceparent": carrier["traceparent"], "tracestate": carrier["tracestate"]}}
	session := &Session{Context: ctx, provider: provider}
	setupTimeout := options.SetupTimeout
	if setupTimeout == 0 {
		setupTimeout = 30 * time.Second
	}
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), setupTimeout)
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
			} `json:"capture"`
		}
		err := r.call(cleanupCtx, "/v1/teardownTest", map[string]any{"session_id": session.ID, "status": status, "drain_timeout_ms": timeout.Milliseconds()}, &response)
		if err == nil && response.Capture.CaptureIncomplete {
			err = fmt.Errorf("Wiretap capture incomplete for %s", t.Name())
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
	// Preserve explicit request tracing; otherwise bind the enclosing test.
	ctx := request.Context()
	if !trace.SpanContextFromContext(ctx).IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(t.ctx))
	}
	return t.transport.RoundTrip(request.Clone(ctx))
}
func (r *Runtime) call(ctx context.Context, path string, payload, output any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	baseURL := r.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("WIRETAP_BASE_URL")
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
		if output != nil {
			_ = json.Unmarshal(body, output)
		}
		return fmt.Errorf("Wiretap %s: HTTP %d: %s", path, res.StatusCode, body)
	}
	if output != nil {
		return json.NewDecoder(res.Body).Decode(output)
	}
	return nil
}
