const test = require('node:test');
const assert = require('node:assert/strict');
const api = require('@opentelemetry/api');
const { BasicTracerProvider, SimpleSpanProcessor, InMemorySpanExporter } = require('@opentelemetry/sdk-trace-base');
const { AsyncLocalStorageContextManager } = require('@opentelemetry/context-async-hooks');
const { W3CTraceContextPropagator } = require('@opentelemetry/core');
const { registerInstrumentations } = require('@opentelemetry/instrumentation');
const { UndiciInstrumentation } = require('@opentelemetry/instrumentation-undici');
const exporter = new InMemorySpanExporter();
const provider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });
api.trace.setGlobalTracerProvider(provider);
api.context.setGlobalContextManager(new AsyncLocalStorageContextManager().enable());
api.propagation.setGlobalPropagator(new W3CTraceContextPropagator());
registerInstrumentations({ instrumentations: [new UndiciInstrumentation()] });
const { startCapture } = require('../src');
const http = require('node:http');
const { once } = require('node:events');

test('existing provider and client instrumentation are reused without duplicate spans', async t => {
  let parent;
  const server = http.createServer(async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw || '{}');
    res.setHeader('content-type', 'application/json');
    if (req.url === '/v1/setupTest') res.end(JSON.stringify({ session_id: 'existing', name: body.name, sut_url: url }));
    else if (req.url === '/v1/teardownTest') res.end(JSON.stringify({ capture: { schema_validation: { status: 'skipped' } } }));
    else if (req.url === '/v1/startRun') res.end(JSON.stringify({ sut_url: url }));
    else { parent = req.headers.traceparent; res.end('{}'); }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const url = `http://127.0.0.1:${server.address().port}`;
  t.after(() => new Promise(resolve => server.close(resolve)));
  const runtime = await startCapture({ baseUrl: url, generateSchema: true, instrumentation: false });
  await runtime.runTest('existing provider', async () => { const response = await fetch(url + '/sut'); await response.text(); });
  assert.equal(api.trace.getTracerProvider().getDelegate(), provider);
  const spans = exporter.getFinishedSpans();
  const root = spans.find(span => span.name === 'existing provider');
  assert.ok(root);
  assert.equal(parent.split('-')[1], root.spanContext().traceId);
  assert.equal(spans.filter(span => span.kind === api.SpanKind.CLIENT).length, 1);
  const noOp = { getTracer: () => ({ startSpan: () => api.trace.wrapSpanContext(api.INVALID_SPAN_CONTEXT) }) };
  const invalid = await startCapture({ baseUrl: url, generateSchema: true, instrumentation: false, tracerProvider: noOp });
  await assert.rejects(invalid.runTest('invalid', () => {}), /valid test span contexts/);
});
