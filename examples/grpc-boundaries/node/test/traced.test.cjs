// This is the application's own preload, before the harness imports gRPC/HTTP.
const { api, provider } = require('../tracing.cjs');
const { test } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { fixture, call, grpc } = require('../harness.cjs');

function get(url) {
  return new Promise((resolve, reject) => {
    http.get(url, response => {
      let data = ''; response.on('data', chunk => { data += chunk; });
      response.on('end', () => resolve({ status: response.statusCode, body: JSON.parse(data) }));
    }).on('error', reject);
  });
}

for (const mode of ['consumer', 'mixed']) test(`parallel ${mode} paths reuse ordinary clients and application OTel`, async t => {
  const f = await fixture(t, { name: `parallel-${mode}`, mode, explicitSession: true });
  assert.equal(api.trace.getTracerProvider().getDelegate(), provider, 'SDK must preserve the application tracer provider');
  const connection = mode === 'mixed' ? null : f.getClient(f.runtime.sutUrl);
  // Both attempts are admitted before either sends an RPC. Reused channels and
  // interleaved dependency calls cannot rely on the sequential fallback.
  let arrivals = 0, release;
  const barrier = new Promise(resolve => { release = resolve; });
  await Promise.all(['parallel-one', 'parallel-two'].map(name => f.runtime.runTest(name, async session => {
    f.remember(session);
    if (++arrivals === 2) release(); await barrier;
    if (mode === 'mixed') {
      assert.deepEqual(await get(`${session.sutUrl}/check?service=${name}`), { status: 200, body: { status: 'SERVING' } });
    } else assert.equal((await call(connection, name)).code, grpc.status.OK);
  })));
  await f.finishAndInspect(async ({ attempts, projection }) => {
    assert.equal(attempts.length, 2);
    assert.notEqual(attempts[0].capture.testcase.trace_id, attempts[1].capture.testcase.trace_id);
    for (const attempt of attempts) {
      const flows = attempt.capture.traffic;
      assert.equal(attempt.capture.testcase.status, 'pass');
      assert.equal(flows.length, 2);
      const inbound = flows.find(flow => flow.channel === 'test_to_sut');
      const outbound = flows.find(flow => flow.channel === 'sut_to_dependency');
      assert.ok(inbound); assert.ok(outbound);
      assert.equal(outbound.asset_id, 'inventory');
      assert.equal(outbound.parent_flow_id, inbound.id);
      for (const flow of flows) {
        assert.equal(flow.trace_id, attempt.capture.testcase.trace_id);
        assert.equal(flow.test_correlation, 'trace');
        assert.equal(flow.trace_context_source, 'received');
        if (flow.protocol === 'grpc') {
          assert.deepEqual(await projection(attempt, flow, 'request'), { service: attempt.capture.testcase.name });
          assert.deepEqual(await projection(attempt, flow, 'response'), { status: 'SERVING' });
        }
      }
    }
  });
});
