const { test: base } = require('@playwright/test');
const { capturePlaywright, expect } = require('../../../playwright');

const custom = base.extend({
  olderFixture: [async ({}, use, info) => {
    await use();
    if (info.title === 'late fixture failure') throw new Error('user fixture cleanup failed');
  }, { auto: true }],
});
const test = capturePlaywright(custom);

test.beforeEach(async ({ request }, info) => {
  await request.get('/api/before');
  if (info.title === 'before hook failure') throw new Error('before failed');
});
test.afterEach(async ({ request }, info) => {
  await request.get('/api/after');
  if (info.title === 'after hook failure') throw new Error('after failed');
});

test('pass', async ({ request, wiretap }) => {
  const response = await request.get('/api/pass');
  const body = await response.json();
  expect(body.ok).toBe(true);
  expect(body.headers['x-existing']).toBe('kept');
  expect(body.headers.traceparent).toBe(wiretap.traceContext.traceparent);
});
test('assertion failure', async ({ request }) => {
  await request.get('/api/assertion'); expect(1).toBe(2);
});
test('timeout', async ({ request }) => {
  test.setTimeout(150);
  await request.get('/api/timeout'); await new Promise(() => {});
});
test('before hook failure', async ({ request }) => { await request.get('/api/must-not-run'); });
test('after hook failure', async () => {});
test('late fixture failure', async () => {});
test('expected failure', async () => { test.fail(); expect(1).toBe(2); });
test('runtime skip', async () => { test.skip(); });
test.skip('static skip', async () => {});
test.describe('retry', () => {
  test.describe.configure({ retries: 1 });
  test('retrying test', async ({ request }, info) => {
    await request.get('/api/retry'); expect(info.retry).toBe(1);
  });
});
