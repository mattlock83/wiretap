const { test: base, expect } = require('@playwright/test');
const { withWiretap } = require('./src/playwright-config');

function capturePlaywright(test) {
  return test.extend({
    wiretapOptions: [{}, { option: true }],
    wiretap: [async ({ wiretapOptions }, use, testInfo) => {
      if (!process.env.WIRETAP_PLAYWRIGHT_COORDINATOR) {
        throw new Error('Wiretap Playwright fixtures require withWiretap(...) in playwright.config');
      }
      const coordinator = JSON.parse(process.env.WIRETAP_PLAYWRIGHT_COORDINATOR);
      const { headers, ...options } = { ...testInfo.project.use.wiretapOptions, ...wiretapOptions };
      if (typeof options.onSession === 'function' || options.tracerProvider) {
        throw new Error('Playwright wiretapOptions must be serializable; use Playwright fixtures for session hooks or the plain JavaScript SDK for custom tracer providers');
      }
      const response = await fetch(coordinator.url, {
        method: 'POST', headers: { authorization: `Bearer ${coordinator.token}`, 'content-type': 'application/json' },
        body: JSON.stringify({ id: testInfo.testId, retry: testInfo.retry,
          name: testInfo.titlePath.slice(1).join(' › '), project: testInfo.project.name,
          options: { ...options, sutBaseUrl: options.sutBaseUrl || process.env.PLAYWRIGHT_TEST_BASE_URL } }),
        signal: AbortSignal.timeout(coordinator.timeoutMs),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body.error || `Wiretap setup failed (${response.status})`);
      await use(body);
    }, { auto: true, timeout: 45000 }],
    baseURL: async ({ wiretap }, use) => { await use(wiretap.sutUrl); },
    extraHTTPHeaders: async ({ extraHTTPHeaders, wiretapOptions, wiretap }, use, testInfo) => {
      await use({ ...extraHTTPHeaders, ...testInfo.project.use.wiretapOptions?.headers, ...wiretapOptions.headers, ...wiretap.traceContext });
    },
    _wiretapConfiguration: [async ({ baseURL, extraHTTPHeaders, wiretap }, use) => {
      if (baseURL !== wiretap.sutUrl || extraHTTPHeaders?.traceparent !== wiretap.traceContext.traceparent) {
        throw new Error('Do not override Playwright baseURL or extraHTTPHeaders after withWiretap. Use test.use({ wiretapOptions: { sutBaseUrl, headers } }) instead.');
      }
      await use();
    }, { auto: true }],
  });
}

const test = capturePlaywright(base);
module.exports = { test, expect, withWiretap, capturePlaywright };
