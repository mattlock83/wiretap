const test = require('node:test');
const assert = require('node:assert/strict');
const { startCapture } = require('../src');

function mockControl(t, { hang = () => true } = {}) {
  const durations = [];
  const realSetTimeout = global.setTimeout;
  t.mock.method(global, 'setTimeout', (callback, duration, ...args) => {
    durations.push(duration);
    // Exercise real timer cancellation and AbortSignal behavior without waiting
    // thirty seconds for each default deadline acceptance case.
    return realSetTimeout(callback, Math.min(duration, 15), ...args);
  });
  t.mock.method(global, 'fetch', async (_url, options) => {
    const path = new URL(_url).pathname;
    if (hang(path)) return new Promise((_resolve, reject) => {
      options.signal?.addEventListener('abort', () => reject(options.signal.reason), { once: true });
    });
    return new Response(JSON.stringify(path === '/v1/setupTest'
      ? { session_id: 's', sut_url: 'http://sut', name: 'example' }
      : {}), { headers: { 'content-type': 'application/json' } });
  });
  return durations;
}

test('default control startup has a finite deadline and an actionable error', async t => {
  const durations = mockControl(t);
  await assert.rejects(startCapture({ schemaRequired: false }), error =>
    error.code === 'WIRETAP_CONTROL_TIMEOUT' && error.message.includes('POST /v1/startRun') && error.message.includes('30000ms'));
  assert.deepEqual(durations, [30000]);
});

test('default teardown deadline accommodates the requested drain', async t => {
  const durations = mockControl(t, { hang: path => path === '/v1/teardownTest' });
  const runtime = await startCapture({ schemaRequired: false });
  await runtime.setupTest({ name: 'example' });
  await assert.rejects(runtime.teardownTest({ drainTimeoutMs: 60000 }), /70000ms/);
  assert.deepEqual(durations, [30000, 30000, 70000]);
});

test('explicit control deadlines apply to setup, cleanup and reporting', async t => {
  const durations = mockControl(t, { hang: path => path === '/v1/report' || path === '/v1/teardownTest' });
  const runtime = await startCapture({ schemaRequired: false, timeoutMs: 50 });
  await runtime.setupTest({ name: 'example' });
  await assert.rejects(runtime.report(), /POST \/v1\/report timed out after 50ms/);
  await assert.rejects(runtime.teardownTest({ drainTimeoutMs: 60000 }), /50ms/);
  assert.deepEqual(durations, [50, 50, 50, 50]);
});

test('zero opts out without adding a deadline or AbortSignal', async t => {
  let calls = 0;
  t.mock.method(global, 'fetch', async (_url, options) => {
    assert.equal(options.signal, undefined);
    calls++;
    return new Response('{}', { headers: { 'content-type': 'application/json' } });
  });
  const runtime = await startCapture({ schemaRequired: false, timeoutMs: 0 });
  await runtime.report();
  await runtime.finishRun();
  assert.equal(calls, 3);
});

test('invalid deadline values fail before contacting the runtime', async t => {
  t.mock.method(global, 'fetch', () => { throw Error('must not contact runtime'); });
  for (const timeoutMs of [-1, 0.5, NaN, Infinity, '10', 2147483648]) {
    await assert.rejects(startCapture({ schemaRequired: false, timeoutMs }), /timeoutMs must be an integer/);
  }
});
