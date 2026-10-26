const test = require('node:test');
const assert = require('node:assert/strict');
const { CoreClient } = require('../src/control-client');

test('caller cancellation reaches the shared transport and preserves its reason', async t => {
  const controller = new AbortController();
  const reason = new Error('runner interrupted');
  t.mock.method(global, 'fetch', (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(options.signal.reason), { once: true });
    controller.abort(reason);
  }));
  await assert.rejects(new CoreClient({ timeoutMs: 1000 }).startRun({}, { signal: controller.signal }), error => error === reason);
});

test('an already cancelled caller never contacts the runtime', async t => {
  t.mock.method(global, 'fetch', () => assert.fail('must not fetch'));
  const reason = new Error('cancelled before startup');
  await assert.rejects(new CoreClient().startRun({}, { signal: AbortSignal.abort(reason) }), error => error === reason);
});

test('large export failures retain the complete receipt', async t => {
  const receipt = { error: 'incomplete capture', bundle_path: '/artifacts/run', outputs: { errors: Array.from({ length: 300 }, (_, i) => ({ message: `export ${i}: ${'x'.repeat(80)}` })) } };
  t.mock.method(global, 'fetch', () => Promise.resolve(Response.json(receipt, { status: 409 })));
  await assert.rejects(new CoreClient().finishRun(), error => {
    assert.equal(error.status, 409);
    assert.deepEqual(error.body, receipt);
    return true;
  });
});

test('control transport requires JSON responses for every lifecycle endpoint', async t => {
  t.mock.method(global, 'fetch', (_url, options) => {
    assert.equal(options.method, 'POST');
    assert.equal(options.body, '{}');
    return Promise.resolve(new Response('not JSON'));
  });
  const client = new CoreClient();
  for (const operation of ['startRun', 'setupTest', 'teardownTest', 'finishRun']) {
    await assert.rejects(client[operation](), new RegExp(`POST /v1/${operation} returned an invalid JSON response`));
  }
});
