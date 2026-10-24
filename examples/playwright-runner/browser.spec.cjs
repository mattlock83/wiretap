const { test, expect } = require('../../sdk/js/playwright');

test('browser navigation carries the test trace and existing headers', async ({ page, wiretap }) => {
  const response = await page.goto('/todos');
  expect(response.status()).toBe(200);
  expect(response.headers()['x-request-traceparent']).toBe(wiretap.traceContext.traceparent);
  expect(response.headers()['x-test-client']).toBe('playwright');
  expect(Array.isArray(JSON.parse(await page.locator('body').innerText()))).toBe(true);
});
