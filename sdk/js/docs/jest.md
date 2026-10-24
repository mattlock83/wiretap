# Jest

For Jest 29.7 or 30 on Node 22+, wrap your existing Node-environment configuration:

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
the API contract and exports in `.wiretap.yaml`:

```yaml
version: 1
application: {id: my-api}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text], postman: true}
sut:
  id: my-api
  kind: service
  schema: openapi.yaml
```

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

## An already running API

Omit `app` and put `url: http://127.0.0.1:8080` under `sut` in `.wiretap.yaml`.
Keep pointing the shared client at `process.env.WIRETAP_SUT_URL`:

```js
module.exports = withWiretap(existingConfig);
```

## Results and lifecycle

The output directory contains `last-run.json` with exact artifact paths, the
capture archive, coverage reports, Postman collections and a bounded
`wiretap.log`. Keep the complete directory in CI, including failed runs.
Framework results and API contract violations are reported separately.

Capture includes ordinary `beforeEach`/`afterEach` work. Sync, async, `done`,
generator, parameterized and retry callbacks retain their Jest outcomes.
Suite-wide `beforeAll`/`afterAll` work is outside individual capture attempts.
On interruption, Wiretap stops its process and preserves a receipt; an active
unfinished attempt leaves an interrupted archive without complete exports.

Files run with one worker by default. Parallel workers require
`withWiretap(existingConfig, { explicitSession: true })` and W3C trace-context
propagation through every relevant SUT boundary. Jest `.concurrent` tests also
require that propagation. Custom environments, watch mode and a shared
multi-project run need a project-owned lifecycle integration.

## Dynamic fixtures and selective capture

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

`createJestSuite` and the plain [JavaScript lifecycle API](javascript.md) remain
available for selective capture and project-owned runtime lifecycles. Use
`testEnvironment: '@wiretap/sdk/jest-environment'` when supplying those hooks
yourself. The selective `createJestSuite` wrapper supports async test callbacks;
the automatic adapter above supports Jest's existing callback forms.

The [pinned external Express/Jest demo](https://github.com/mattlock83/wiretap/tree/main/examples/existing-jest)
verifies all 120 existing outcomes before and after capture, API coverage and
Postman parsing with the official Node library.
