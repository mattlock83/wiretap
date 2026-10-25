# Playwright Test

Add Wiretap with one configuration wrapper and one test import change. Existing
API assertions, hooks, reporters, browser options and custom fixtures remain in
place. Use Node.js 22+ and Playwright Test 1.64.1+ within 1.x.

[Install the local SDK tarball](../README.md#install-and-configure-once) alongside
your existing `@playwright/test`, and make the Wiretap executable available on
`PATH` or set `WIRETAP_BINARY` to its absolute path.
Keep your API contract and output settings in `.wiretap.yaml`:

```yaml
version: 1
application:
  id: todos
outputs:
  coverage: [json, text]
  postman: true
sut:
  id: todos
  kind: service
  schema: openapi.yaml
```

The main HTTP API requires that spec by default. HTTP dependencies and mocks
need no spec or opt-out flag; adding a dependency schema enables its validation
and contract coverage. See [HTTP dependency schema options](javascript.md#http-dependency-schema-options)
for strict `schemaRequired: true` overrides and provider-check assurance.

Wrap your existing configuration:

```js
// playwright.config.js
const { withWiretap } = require('@wiretap/sdk/playwright');

module.exports = withWiretap({
  testDir: './tests',
  use: { baseURL: 'http://127.0.0.1:3000' },
  // Keep your webServer, globalSetup, globalTeardown, projects and reporters.
});
```

Use the adapter's test fixture in your tests or shared fixtures module:

```js
const { test, expect } = require('@wiretap/sdk/playwright');

test('lists todos', async ({ request }) => {
  const response = await request.get('/todos');
  expect(response.ok()).toBeTruthy();
});
```

Run your usual `playwright test` command. Wiretap starts on available local ports,
routes relative requests through capture, adds a trace header to the standard
`request` and browser-context fixtures, and writes exports at the end. Playwright
reports the final outcome after hooks and fixture cleanup; retries create
separate attempts with a stable test identity. A failed assertion, timeout or
teardown stays failed in the artifact. An expected failure also retains its
actual failed execution status. Static skips have no capture attempt; a skip
after setup has a skipped attempt.

The existing `baseURL` selects the actual API target; tests receive the Wiretap
proxy as their base URL. `.wiretap.yaml` can supply the target instead. Existing
`webServer` readiness checks and global setup continue to use the actual server.
The existing `extraHTTPHeaders` are retained alongside Wiretap's trace context.
Use relative API URLs so traffic reaches the proxy. Absolute URLs to the original
server bypass capture. Browser traffic receives explicit headers; this does not
depend on Node's HTTP instrumentation.

## Existing fixtures

If your suite already extends Playwright's test object, wrap that object once in
your shared fixtures module:

```js
const { test: existingTest } = require('./application-fixtures');
const { capturePlaywright, expect } = require('@wiretap/sdk/playwright');

module.exports = { test: capturePlaywright(existingTest), expect };
```

Custom fixtures and hooks can use the usual `request`, `page`, `context` and
`baseURL` fixtures. The optional `wiretap` fixture exposes `sutUrl`,
`dependencyUrls` and `traceContext` for custom clients. When creating an
independent request/browser context yourself, provide its Wiretap URL and trace
headers explicitly; it does not inherit the runner's fixtures automatically.

## Options

```js
module.exports = withWiretap(existingConfig, {
  config: '.wiretap.yaml',
  outputDir: 'artifacts',
  // binary: '/path/to/wiretap',
  // capture: { drainTimeoutMs: 5000 },
});
```

Paths resolve from the Playwright configuration directory, or from `cwd` when
you set it in the wrapper options. `capture` supplies serializable shared-core
settings. Use Playwright fixtures for
session setup callbacks; custom tracing providers remain available through the
plain JavaScript SDK. Per-test or per-file settings
use `wiretapOptions`, including a target override or additional headers:

```js
test.use({ wiretapOptions: {
  sutBaseUrl: 'http://127.0.0.1:3001',
  headers: { authorization: 'Bearer dummy-token' },
} });
```

After applying the wrapper, use these options instead of overriding `baseURL`
or `extraHTTPHeaders` with `test.use()`. Wiretap detects a bypass and fails with
an actionable error. Keep its added reporter; a CLI `--reporter` override would
remove the completion hook and is rejected. The normal configured reporters
are preserved. Standard single-run CLI execution is supported; interactive
UI/watch sessions and merged reports are not capture runs.

## Parallel workers

The default is one worker, which also supports applications whose downstream
calls do not propagate trace context. To keep an existing parallel suite:

```js
module.exports = withWiretap({ ...existingConfig, workers: 4 }, {
  explicitSession: true,
});
```

Wiretap gives each test its own trace context. The application must propagate
W3C trace context to any captured dependencies; otherwise those downstream calls
cannot be attributed safely. The standard Playwright fixtures carry context for
requests to the API. Tests in different projects retain their project identity.
Parallel projects and per-test overrides must use the same SUT origin. The SDK
rejects target changes across origins in a parallel run; use separate runs or
one worker for different servers.

Ctrl+C and SIGTERM use Playwright's cancellation and cleanup path. Completed
cleanup records the interrupted test outcome and finalizes exports before the
owned process stops; forced process termination cannot complete that cleanup.

Each managed run writes its canonical `last-run.json` receipt and bounded
`wiretap.log` inside its unique archive directory. The receipt contains the exact
archive, coverage and Postman paths. Keep the complete output directory in CI,
including failed runs. The output-root `last-run.json` is an advisory latest
snapshot; archive-local receipts remain independent when owners run concurrently.

Only isolated synthetic test data and dummy secrets belong in these captures.
The SDK stores the captured traffic unchanged.

See the [runnable Todo example](https://github.com/mattlock83/wiretap/tree/main/examples/playwright-runner).
