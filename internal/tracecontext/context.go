// Package tracecontext handles W3C propagation without requiring an exporter.
package tracecontext

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Context struct {
	Traceparent string `json:"traceparent"`
	Tracestate  string `json:"tracestate,omitempty"`
}

func Extract(headers http.Header) trace.SpanContext {
	// Multiple parents are ambiguous, even if Header.Get would choose one.
	if len(headers.Values("traceparent")) != 1 {
		return trace.SpanContext{}
	}
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.HeaderCarrier(headers))
	return trace.SpanContextFromContext(ctx)
}

func (c Context) Validate() (trace.SpanContext, error) {
	headers := http.Header{}
	headers.Set("traceparent", c.Traceparent)
	if c.Tracestate != "" {
		headers.Set("tracestate", c.Tracestate)
	}
	sc := Extract(headers)
	if !sc.IsValid() {
		return sc, fmt.Errorf("trace_context requires a valid W3C traceparent")
	}
	if c.Tracestate != "" {
		if _, err := trace.ParseTraceState(c.Tracestate); err != nil {
			return sc, fmt.Errorf("invalid tracestate: %w", err)
		}
	}
	return sc, nil
}

func Inject(headers http.Header, sc trace.SpanContext) {
	headers.Del("traceparent")
	headers.Del("tracestate")
	propagation.TraceContext{}.Inject(trace.ContextWithSpanContext(context.Background(), sc), propagation.HeaderCarrier(headers))
}
