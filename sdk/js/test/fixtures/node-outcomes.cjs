const { test, before, after, beforeEach } = require('node:test');
const { startCapture, bindTest } = require('../../src');
const { BasicTracerProvider, InMemorySpanExporter, SimpleSpanProcessor } = require('@opentelemetry/sdk-trace-base');
const exporter = new InMemorySpanExporter();
const provider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });
let server, runtime;
const beforeCounts = {};
beforeEach(t => { beforeCounts[t.name] = (beforeCounts[t.name] || 0) + 1; });
before(async () => {
  server = await require('./outcomes-server.cjs')();
  runtime = await startCapture({ baseUrl: server.url, generateSchema: true, tracerProvider: provider, manageRuntime: false });
});
const wrapped = bindTest(() => runtime, test);
wrapped('pass', () => {});
wrapped('hook failure', t => { t.after(() => { throw Error('cleanup failed'); }); });
wrapped('subtest failure', async t => { await t.test('child', () => { throw Error('child failed'); }); });
wrapped('timeout', { timeout: 60 }, () => new Promise(() => {}));
wrapped('validation', () => {});
wrapped('skip', t => { t.skip(); });
after(async () => { await runtime.finishRun(); await server.finish({
  open: runtime.openSessions.size, beforeCounts,
  spans: exporter.getFinishedSpans().map(span => ({ name: span.name, status: span.status.code }))
}); });
