const { withWiretap } = require('../../sdk/js/playwright');
const browser = process.env.WIRETAP_PLAYWRIGHT_BROWSER;
const parallel = process.env.WIRETAP_PLAYWRIGHT_PARALLEL === '1';

module.exports = withWiretap({
  testDir: '.',
  testMatch: ['todos.spec.cjs', ...(browser ? ['browser.spec.cjs'] : [])],
  outputDir: `${__dirname}/test-results`,
  globalSetup: './server.cjs',
  workers: parallel ? 2 : 1,
  fullyParallel: parallel,
  use: {
    extraHTTPHeaders: { 'x-test-client': 'playwright' },
    ...(browser && browser !== 'chromium' ? { channel: browser } : {}),
  },
}, { config: '.wiretap.yaml', outputDir: 'artifacts', explicitSession: parallel });
