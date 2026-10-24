require('../tracing');
const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { once } = require('node:events');
const { startCapture, bindTest } = require('../src');
const { BasicTracerProvider, SimpleSpanProcessor, InMemorySpanExporter, AlwaysOffSampler } = require('@opentelemetry/sdk-trace-base');
async function fixture(t, config = {}) {
  const calls = [], requests = [];
  const server = http.createServer(async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    res.setHeader('content-type', 'application/json');
    if (!req.url.startsWith('/v1/')) { requests.push({ path: req.url, parent: req.headers.traceparent }); res.end('{}'); return; }
    const body = JSON.parse(raw || '{}'); calls.push({ path: req.url, body, parent: req.headers.traceparent });
    if (req.url === '/v1/setupTest') res.end(JSON.stringify({ session_id: `session-${calls.length}`, name: body.name, sut_url: url, proxy_listen: url }));
    else if (req.url === '/v1/teardownTest') res.end(JSON.stringify(config.teardown || { capture: { testcase: { status: body.status }, schema_validation: { status: 'skipped' } } }));
    else res.end(JSON.stringify({ sut_url: url }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const url = `http://127.0.0.1:${server.address().port}`;
  t.after(() => new Promise(resolve => server.close(resolve)));
  const runtime = await startCapture({ baseUrl: url, schemaRequired: false, explicitSession: true, ...config });
  return { runtime, calls, requests, url };
}
const httpGet = url => new Promise((resolve, reject) => http.get(url, res => { res.resume(); res.on('end', resolve); }).on('error', reject));
test('ordinary fetch and Node HTTP clients keep isolated test roots under overlap', async t => {
  const { runtime, calls, requests, url } = await fixture(t);
  let entered = 0, unblock;
  const gate = new Promise(resolve => { unblock = resolve; });
  await Promise.all(['one', 'two'].map(name => runtime.runTest(name, async session => {
    if (++entered === 2) unblock(); await gate;
    assert.equal(runtime.activeSession.sessionId, session.sessionId);
    await fetch(`${url}/${name}/fetch`); await httpGet(`${url}/${name}/http`);
    assert.equal(runtime.activeSession.sessionId, session.sessionId);
  })));
  const roots = calls.filter(x => x.path === '/v1/setupTest');
  assert.equal(roots.length, 2); assert.notEqual(roots[0].body.trace_context.traceparent, roots[1].body.trace_context.traceparent);
  for (const root of roots) {
    const trace = root.body.trace_context.traceparent.split('-')[1];
    const outgoing = requests.filter(x => x.path.startsWith(`/${root.body.name}/`));
    assert.equal(outgoing.length, 2);
    for (const req of outgoing) assert.equal(req.parent?.split('-')[1], trace);
  }
  assert.ok(calls.every(call => call.parent === undefined), 'control requests are not test traffic');
  assert.equal(runtime.activeSession, null); await runtime.finishRun();
});
test('retries create fresh unsampled roots without losing propagation', async t => {
  const provider = new BasicTracerProvider({ sampler: new AlwaysOffSampler() });
  const { runtime, calls, requests, url } = await fixture(t, { tracerProvider: provider });
  for (let i = 0; i < 2; i++) await runtime.runTest('same test', () => fetch(url + '/attempt'));
  const roots = calls.filter(x => x.path === '/v1/setupTest').map(x => x.body.trace_context.traceparent);
  assert.notEqual(roots[0].split('-')[1], roots[1].split('-')[1]); assert.ok(roots.every(parent => parent.endsWith('-00')));
  assert.deepEqual(requests.map(x => x.parent.split('-')[1]), roots.map(x => x.split('-')[1]));
});
test('framework lifecycle handles bind separate hooks and preserve overlapping attempts', async t => {
  const { runtime, calls, requests, url } = await fixture(t);
  const attempts = ['first', 'second'].map(name => ({ name, attempt: runtime.createTestAttempt(name) }));
  for (const phase of ['setup', 'test', 'cleanup']) {
    await Promise.all(attempts.map(({ name, attempt }) => attempt.run(async session => {
      assert.equal(runtime.activeSession.sessionId, session.sessionId);
      await fetch(`${url}/${name}/${phase}`);
    })));
  }
  for (const { attempt } of attempts) {
    await attempt.finish({ status: 'pass' });
    await attempt.finish({ status: 'fail' }); // duplicate completion cannot change an outcome
    await assert.rejects(attempt.run(() => {}), /already closed/);
  }
  const roots = calls.filter(call => call.path === '/v1/setupTest');
  assert.equal(new Set(roots.map(root => root.body.trace_context.traceparent)).size, 2);
  for (const root of roots) {
    const outgoing = requests.filter(request => request.path.startsWith(`/${root.body.name}/`));
    assert.equal(outgoing.length, 3);
    assert.ok(outgoing.every(request => request.parent.split('-')[1] === root.body.trace_context.traceparent.split('-')[1]));
  }
  assert.deepEqual(calls.filter(call => call.path === '/v1/teardownTest').map(call => call.body.status), ['pass', 'pass']);
  await runtime.finishRun();
  assert.equal(calls.filter(call => call.path === '/v1/finishRun').length, 1);
});
test('a framework can finish a timed-out attempt before its callback settles', async t => {
  const { runtime, calls } = await fixture(t);
  const attempt = runtime.createTestAttempt('timed out');
  let started, release;
  const ready = new Promise(resolve => { started = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  const running = attempt.run(async () => { started(); await gate; });
  await ready;
  await attempt.finish({ status: 'fail', error: new Error('runner timeout') });
  release();
  await running;
  const completed = calls.filter(call => call.path === '/v1/teardownTest');
  assert.equal(completed.length, 1);
  assert.equal(completed[0].body.status, 'fail');
});
test('schema errors and onSession failures close root spans and preserve original failures', async t => {
  const exporter = new InMemorySpanExporter();
  const provider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });
  const { runtime, calls } = await fixture(t, { tracerProvider: provider, teardown: { capture: { schema_validation: { status: 'fail', errors: [{ message: 'schema mismatch' }] } } } });
  const original = new Error('original failure');
  await assert.rejects(runtime.runTest('callback failure', () => { throw original; }), err => err === original);
  await assert.rejects(runtime.runTest('schema failure', () => {}), /schema mismatch/);
  await assert.rejects(runtime.runTest('hook failure', () => assert.fail('must not run'), { onSession() { throw original; } }), err => err === original);
  assert.equal(calls.filter(x => x.path === '/v1/teardownTest').length, 3);
  assert.deepEqual(exporter.getFinishedSpans().map(span => span.status.code), [2, 2, 2]);
});
test('shared suites do not stop the runtime, and manual sessions prevent premature shutdown', async t => {
  const { runtime, calls } = await fixture(t, { manageRuntime: false });
  await runtime.setupTest({ name: 'manual' }); await assert.rejects(runtime.finishRun(), /teardown all/); await runtime.teardownTest();
  assert.deepEqual(await runtime.finishRun(), { status: 'shared' }); assert.equal(calls.some(x => x.path === '/v1/finishRun'), false);
});
test('node:test binding gives each concurrent callback its own session', async t => {
  const { runtime } = await fixture(t); const registered = [];
  const wrapped = bindTest(runtime, (name, fn) => registered.push(fn), { explicitSession: true });
  for (const name of ['first', 'second']) wrapped(name, async (_, session) => {
    await new Promise(resolve => setImmediate(resolve)); assert.equal(runtime.activeSession.sessionId, session.sessionId);
  });
  await Promise.all(registered.map(fn => fn({})));
});

test('falsy thrown values remain test failures even when cleanup also fails', async t => {
  const { runtime, calls } = await fixture(t, { teardown: { capture: { schema_validation: { status: 'fail' } } } });
  let rejected = false;
  try { await runtime.runTest('falsy failure', () => { throw 0; }); }
  catch (error) { rejected = true; assert.equal(error, 0); }
  assert.ok(rejected);
  assert.equal(calls.find(call => call.path === '/v1/teardownTest').body.status, 'fail');
});

test('separate fixture activations initialize onSession once per attempt', async t => {
  const { runtime } = await fixture(t);
  let initialized = 0;
  const attempt = runtime.createTestAttempt('fixtures', {
    onSession(session) {
      initialized++;
      assert.equal(runtime.activeSession, session);
    }
  });
  for (const phase of ['setup', 'test', 'cleanup']) {
    await attempt.run(session => {
      assert.equal(runtime.activeSession, session, phase);
      assert.equal(initialized, 1, phase);
    });
  }
  await attempt.finish();
});
