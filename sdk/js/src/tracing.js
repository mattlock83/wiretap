const api = require('@opentelemetry/api');
const { AsyncLocalStorageContextManager } = require('@opentelemetry/context-async-hooks');
const { BasicTracerProvider } = require('@opentelemetry/sdk-trace-base');
const { W3CTraceContextPropagator, suppressTracing } = require('@opentelemetry/core');

let initialized = false;
let fallback;
const propagator = new W3CTraceContextPropagator();

function initializeTracing({ instrumentation = true } = {}) {
  if (initialized) return;
  initialized = true;
  // Registration is rejected by the API when the application already owns these.
  const probeKey = api.createContextKey('wiretap.context-probe');
  const probe = api.context.active().setValue(probeKey, true);
  if (!api.context.with(probe, () => api.context.active().getValue(probeKey))) {
    const manager = new AsyncLocalStorageContextManager().enable();
    if (!api.context.setGlobalContextManager(manager)) manager.disable();
  }
  if (api.propagation.fields().length === 0) api.propagation.setGlobalPropagator(propagator);
  // The fallback provider creates valid contexts without an exporter or collector.
  fallback = new BasicTracerProvider();
  const delegate = api.trace.getTracerProvider().getDelegate?.();
  if (!delegate || delegate.constructor.name === 'NoopTracerProvider') {
    api.trace.setGlobalTracerProvider(fallback);
  }
  if (instrumentation) {
    const { registerInstrumentations } = require('@opentelemetry/instrumentation');
    const { HttpInstrumentation } = require('@opentelemetry/instrumentation-http');
    const { UndiciInstrumentation } = require('@opentelemetry/instrumentation-undici');
    registerInstrumentations({ instrumentations: [
      new HttpInstrumentation({ disableIncomingRequestInstrumentation: true }),
      new UndiciInstrumentation()
    ] });
  }
}

function newTestSpan(name, provider) {
  initializeTracing();
  let span = (provider || api.trace.getTracerProvider()).getTracer('wiretap.tests').startSpan(name, {}, api.ROOT_CONTEXT);
  if (!api.isSpanContextValid(span.spanContext())) {
    span.end();
    if (provider) throw new Error('Wiretap requires a tracer provider that creates valid test span contexts');
    span = fallback.getTracer('wiretap.tests').startSpan(name, {}, api.ROOT_CONTEXT);
  }
  return span;
}
function serialize(span) {
  const headers = {};
  propagator.inject(api.trace.setSpan(api.ROOT_CONTEXT, span), headers, api.defaultTextMapSetter);
  return { traceparent: headers.traceparent, ...(headers.tracestate ? { tracestate: headers.tracestate } : {}) };
}
function finish(span, error) {
  if (!span) return;
  if (error) { span.recordException(error); span.setStatus({ code: api.SpanStatusCode.ERROR, message: error.message }); }
  else span.setStatus({ code: api.SpanStatusCode.OK });
  span.end();
}
function withoutTracing(fn) { return api.context.with(suppressTracing(api.ROOT_CONTEXT), fn); }
module.exports = { initializeTracing, newTestSpan, serialize, finish, withoutTracing, api };
