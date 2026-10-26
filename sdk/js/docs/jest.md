# Jest

For Jest 29.7 or 30 on Node 22+, first
[install the local SDK and runtime](../README.md#install-and-configure-once), then
wrap your existing Node-environment configuration:

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');

module.exports = withWiretap({
  // Keep your existing testMatch, reporters, transforms and setup files here.
}, {
  app: '<rootDir>/app.js',
});
```

`app` points to your existing Express/Node HTTP handler. CommonJS and default
exports are supported, including the project's configured transforms and native
ESM when enabled in Jest. Wiretap loads the app inside each test file's Jest VM,
after setup files and tests register their mocks. It hosts the handler before
user `beforeAll` hooks and closes it after user `afterAll` hooks. Existing setup
files retain their relative order; no separate Wiretap setup file is needed.

Point the shared test client at the capture URL. For example, change
`supertest(app)` to:

```js
const api = supertest(process.env.WIRETAP_SUT_URL || app);
```

Keep your test files, assertions, mocks and ordinary Jest callbacks. Configure
the API contract in `.wiretap.yaml`:

```yaml
version: 1
application: {id: my-api}
output_dir: artifacts/wiretap
sut:
  id: my-api
  kind: service
  schema: openapi.yaml
```

The main HTTP API requires that spec by default. HTTP dependencies and mocks
need no spec or opt-out flag; add a dependency schema only to enable its
validation and contract coverage. See [boundary configuration](https://github.com/mattlock83/wiretap/blob/main/docs/configuration.md)
for strict `schemaRequired: true` overrides and provider-check assurance.

Run your usual Jest command with `wiretap` on PATH, or set `WIRETAP_BINARY` to
your local binary. Explicit paths are also supported:

```js
withWiretap(existingConfig, {
  app: '<rootDir>/app.js',
  binary: '/path/to/wiretap',
  config: '.wiretap.yaml',
  outputDir: 'artifacts/wiretap',
});
```

The adapter selects available ports, starts Wiretap, records each executed test,
finalizes the run even when assertions fail, and stops its process. The SDK
downloads and publishes nothing. Install the SDK and Wiretap binary first.
Relative `config` and `outputDir` paths resolve from Jest's `rootDir`; the `app`
module also supports Jest's `<rootDir>` token.

## An already running API

Omit `app` and put `url: http://127.0.0.1:8080` under `sut` in `.wiretap.yaml`.
Keep pointing the shared client at `process.env.WIRETAP_SUT_URL`:

```js
module.exports = withWiretap(existingConfig);
```

## Results and lifecycle

Each unique run archive contains its canonical `last-run.json` receipt and a
bounded `wiretap.log`. The receipt gives the exact archive, coverage report and
Arazzo workflow paths. Keep the complete output directory in CI, including
failed runs. Use the archive path printed to stderr to locate that run's receipt;
concurrent owners retain independent records inside their archives.
Every completed run exports JSON and HTML coverage plus eligible Arazzo workflows.
Text summaries remain available through `wiretap report --bundle <archive> --format text`.
Open the receipt's
`outputs.coverage.html` directly in a browser to inspect API coverage and the
test runs behind each condition. The dashboard works offline. The coverage JSON
can also [rebuild it with specs and Arazzo](https://github.com/mattlock83/wiretap/blob/main/docs/artifacts.md#offline-html-dashboard).
Framework results and API contract violations are reported separately.
If capture cleanup also fails after a test failure, Jest reports both errors
while retaining the original framework failure first.

Capture includes ordinary `beforeEach`/`afterEach` work. Sync, async, `done`,
generator, parameterized and retry callbacks retain their Jest outcomes.
Suite-wide `beforeAll`/`afterAll` work is outside individual capture attempts.
On a handled interruption, Wiretap attempts to finalize, preserves the available
receipt and stops its process. An active unfinished attempt leaves an interrupted
archive without complete exports. Forced process termination cannot run cleanup.

Files run with one worker by default. Parallel workers require
`withWiretap(existingConfig, { explicitSession: true })` and W3C trace-context
propagation through every relevant SUT boundary. Jest `.concurrent` tests also
require that propagation. Custom environments and watch mode are unsupported. Run separate Jest projects
individually, then combine their archives with a run set.
Existing global setup runs before Wiretap starts; existing global teardown runs
after Wiretap finalizes. Test-file environments attach to this started runtime.
Explicit capture `baseUrl` or `proxy` options still configure their selected
runtime. Use per-test hooks for requests that should be captured.

## Dynamic fixtures and session settings

For an app created by a fixture, dynamic SDK settings or `onSession` callbacks,
omit the configuration `app` option and call `captureJest` from your existing
`setupFilesAfterEnv` module:

```js
const { captureJest } = require('@wiretap/sdk/jest');

captureJest({
  app: require('../app'),
  onSession(session) {
    // Configure a fixture using this attempt's session or dependency URLs.
  },
});
```

For serializable capture overrides, use the configuration's `capture` option,
for example `{ capture: { tags: ['integration'] } }`. Dynamic callbacks and a
custom tracer provider belong in `captureJest`, because Jest serializes its
configuration when starting workers.

Wiretap instruments Node HTTP clients and `fetch` by default. If your application
already owns that instrumentation, set
`withWiretap(existingConfig, { capture: { instrumentation: false } })` before
Jest starts. For a standalone Wiretap environment, use
`testEnvironmentOptions.wiretap.instrumentation: false`. The environment override
`WIRETAP_EXISTING_OTEL=1` also prevents Wiretap from installing client
instrumentation. Session spans and trace contexts remain available; your existing
instrumentation must propagate them. `captureJest` rejects `instrumentation`
because its registration runs after the environment initializes tracing.

Go applies project defaults. Omit a capture option to inherit it; `schemas: []`
or `dependencies: {}` explicitly clears that list for the test, and
`schemaRequired: false` explicitly permits a missing SUT spec. Overrides do not
change later tests' defaults. SDK dependencies use an object keyed by asset ID,
and supplied contracts use a `schemas` array:

```js
captureJest({
  dependencies: {
    inventory: {
      upstreamUrl: 'http://127.0.0.1:8081',
      upstreamKind: 'mock',
      schemas: [{ type: 'openapi', source: { type: 'file', path: 'inventory.yaml' } }],
    },
  },
});
```

Schema roles follow their SUT or dependency location. HTTP and HTTPS targets are
selected by their URL.

The [pinned external Express/Jest demo](https://github.com/mattlock83/wiretap/tree/main/examples/existing-jest)
verifies all 120 existing outcomes before and after capture, HTML/JSON API coverage and
Arazzo conformance to its pinned official schema, with exported
eligible JSON/REST workflow requests and success criteria compared against raw
archive records and payload files. Non-JSON calls remain in the archive.
