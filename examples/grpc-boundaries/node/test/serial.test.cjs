// No gRPC instrumentation is loaded in this process. Sequential ownership works
// with an unchanged Go provider and ordinary Node client/mock.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { fixture, call, grpc, delay } = require('../harness.cjs');

test('Node client → Go provider → Node mock, without reflection or tracing', async t => {
  const f = await fixture(t, { name: 'go-node-serial', mode: 'go' });
  const direct = f.getClient(f.sutUrl);
  const proxied = f.getClient(f.runtime.sutUrl);
  const expected = await call(direct, 'ordinary-client');
  await f.runtime.runTest('existing Go provider and Node mock', async session => {
    f.remember(session);
    assert.deepEqual(await call(proxied, 'ordinary-client'), expected);
  });
  await f.finishAndInspect(({ manifest, attempts }) => {
    assert.equal(manifest.provided_apis.length, 1);
    assert.equal(manifest.dependencies.length, 1);
    assert.equal(attempts.length, 1);
    const flows = attempts[0].capture.traffic;
    assert.equal(flows.length, 2);
    assert.deepEqual(flows.map(flow => flow.channel).sort(), ['sut_to_dependency', 'test_to_sut']);
    assert.ok(flows.every(flow => flow.test_correlation === 'active_session'));
    assert.ok(flows.every(flow => flow.response.grpc_status === grpc.status.OK));
  });
});

for (const tls of [false, true]) test(`native provider parity over ${tls ? 'TLS with local CA' : 'h2c'}`, async t => {
  const f = await fixture(t, { name: tls ? 'provider-tls' : 'provider-h2c', tls });
  const direct = f.getClient(f.sutUrl);
  const proxied = f.getClient(f.runtime.sutUrl);
  for (const service of ['success', 'invalid', 'empty']) {
    const metadata = new grpc.Metadata();
    metadata.add('example-bin', Buffer.from([17, 0, 255]));
    metadata.add('x-example', 'request-one'); metadata.add('x-example', 'request-two');
    const expected = await call(direct, service, { metadata });
    await f.runtime.runTest(`${service} ${tls ? 'TLS' : 'h2c'}`, async session => {
      f.remember(session);
      assert.deepEqual(await call(proxied, service, { metadata }), expected);
      assert.equal(expected.code, service === 'invalid' ? grpc.status.INVALID_ARGUMENT : grpc.status.OK, expected.details);
    });
  }
  for (const cancel of [false, true]) {
    const expected = await call(direct, 'slow', { timeout: 100, cancel });
    assert.equal(expected.code, cancel ? grpc.status.CANCELLED : grpc.status.DEADLINE_EXCEEDED);
    const attempt = f.runtime.createTestAttempt(cancel ? 'client cancellation' : 'client deadline');
    await attempt.run(async session => {
      f.remember(session);
      const actual = await call(proxied, 'slow', { timeout: 100, cancel });
      assert.equal(actual.code, expected.code);
      // Let the cancelled HTTP/2 exchange settle before reporting its outcome.
      await delay(100);
    });
    // Cancellation is useful diagnostic evidence, not complete provider assurance.
    try { await attempt.finish({ status: 'fail' }); }
    catch (error) { assert.ok(error.response?.capture?.capture_incomplete || error.schemaValidation); }
  }
  await f.finishAndInspect(({ manifest, attempts }) => {
    assert.deepEqual(manifest.dependencies, []);
    assert.equal(attempts.length, 5);
    for (const attempt of attempts) {
      assert.equal(attempt.capture.traffic.length, 1);
      const flow = attempt.capture.traffic[0];
      assert.equal(flow.channel, 'test_to_sut');
      assert.equal(flow.request.metadata['Example-Bin']?.[0], attempt.capture.testcase.name.includes('client ') ? undefined : 'EQD/');
      if (attempt.capture.testcase.name.startsWith('empty')) {
        assert.equal(flow.response.messages.length, 1);
        assert.ok(attempt.bodies.some(body => body.phase === 'response' && body.retained_bytes === 0));
      }
      if (attempt.capture.testcase.name.startsWith('invalid')) {
        assert.equal(flow.response.grpc_status, grpc.status.INVALID_ARGUMENT);
        assert.equal(flow.response.messages?.length || 0, 0);
        const terminal = flow.response.status_source === 'headers' ? flow.response.metadata : flow.response.trailers;
        assert.equal(terminal['Example-Bin'][0], 'AP8q');
      }
      if (attempt.capture.testcase.name.startsWith('client ')) assert.equal(attempt.capture.testcase.status, 'fail');
    }
  });
});
