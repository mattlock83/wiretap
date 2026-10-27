# Jest guide

Wiretap records HTTP traffic from existing Jest tests, retains their outcomes,
measures OpenAPI coverage and exports eligible JSON/REST workflows as Arazzo.
Keep your test files, assertions, mocks and clients. In-process mock function
calls do not cross the HTTP proxy and are not captured. Use synthetic data and
dummy credentials; retained traffic is unchanged.

## Install the runtime and SDK

Use Node.js 22+ and Jest 29.7 or 30 with Jest Circus and the Node test
environment. From a Wiretap checkout with Go 1.26.10+ installed, build and pack:

```sh
go build -o dist/wiretap ./cmd/wiretap
npm pack ./sdk/js --pack-destination ./dist
```

Install the generated SDK tarball in your test project alongside Jest:

```sh
npm install --save-dev /absolute/path/to/wiretap-sdk-0.1.0.tgz
```

Put `wiretap` on `PATH`, or set `WIRETAP_BINARY` to its absolute path. The SDK
includes TypeScript declarations and downloads and publishes nothing. See
[local installation](https://github.com/mattlock83/wiretap/blob/main/docs/local-release.md#install-a-prepared-release)
for prepared binaries and platform-specific instructions.

## Configure the API

Add `.wiretap.yaml` and your API contract to the Jest project:

```yaml
version: 1
application: {id: my-api}
output_dir: artifacts/wiretap
sut:
  id: my-api
  kind: service
  schema: openapi.yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
```

The main API requires OpenAPI by default; `schema_required: false` explicitly
permits omission. Dependencies need only their URL and kind. Add a dependency
`schema` to enable validation and contract coverage, or `schema_required: true`
to require one. Every supplied contract is validated and frozen before test
traffic is admitted. Schema paths resolve relative to the configuration file.

A schema-free dependency still contributes captured examples and eligible
Arazzo workflows; its contract coverage remains unknown. Point the application's
inventory client at the session's dependency capture URL. See
[configuration](https://github.com/mattlock83/wiretap/blob/main/docs/configuration.md)
for project fields, schema defaults and provider mappings.

## Wrap the Jest configuration

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');

module.exports = withWiretap({
  // Keep your existing testMatch, reporters, transforms and setup files here.
}, {
  app: '<rootDir>/app.js',
});
```

`app` exports your existing Express/Node HTTP handler. CommonJS and default
exports are supported, including configured transforms and native ESM when
enabled in Jest. Wiretap loads it inside each test file's Jest VM after setup
files and tests register their mocks, hosts it before user `beforeAll` hooks,
and closes it after user `afterAll` hooks. Existing setup files retain their
relative order; no separate Wiretap setup file is needed.

Point the shared client at the capture URL. For Supertest, change
`supertest(app)` to:

```js
const api = supertest(process.env.WIRETAP_SUT_URL || app);
```

Run your usual Jest command. The adapter chooses available ports, starts Wiretap,
captures each executed attempt, finalizes after cleanup even when assertions
fail, and stops its process. To select paths explicitly:

```js
withWiretap(existingConfig, {
  app: '<rootDir>/app.js',
  binary: '/path/to/wiretap',
  config: '.wiretap.yaml',
  outputDir: 'artifacts/wiretap',
});
```

Relative `config` and `outputDir` paths resolve from Jest's `rootDir`; `app`
also supports Jest's `<rootDir>` token.

## An already running API

Omit `app`, put `url: http://127.0.0.1:8080` under `sut` in `.wiretap.yaml`,
and use `withWiretap(existingConfig)`. Keep the shared client pointed at
`process.env.WIRETAP_SUT_URL`. Your harness owns the API process. Run the suite
to verify routing, trust and trace propagation.

## Dynamic fixtures and session settings

For an app created by a fixture, dynamic settings or `onSession` callbacks,
omit the configuration `app` and call `captureJest` once from an existing
`setupFilesAfterEnv` module:

```js
const { captureJest } = require('@wiretap/sdk/jest');

captureJest({
  app: require('../app'),
  onSession(session) {
    // Configure fixtures using session.sutUrl, session.dependencyUrls
    // and session.traceContext for this attempt.
  },
});
```

Keep `withWiretap` as the suite owner; this registration supplies its dynamic
capture settings. Do not also manage the same test's attempts manually.
Serializable overrides can go in the configuration's `capture` option, for
example `{ capture: { tags: ['integration'] } }`. Callbacks and custom tracer
providers belong in `captureJest`, because Jest serializes worker configuration.

Go applies project defaults. Omit an option to inherit it; `schemas: []` or
`dependencies: {}` clears that collection for the test, and
`schemaRequired: false` permits a missing SUT spec. Overrides do not change later
tests' defaults. SDK dependencies are keyed by asset ID, and contracts use a
`schemas` array:

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
selected by their URL. Explicit capture `baseUrl` or `proxy` options configure
their selected runtime rather than attaching to the owner's runtime.

## Tracing and concurrency

Wiretap instruments Node HTTP clients and `fetch` by default. If your application
already owns that instrumentation, set
`withWiretap(existingConfig, { capture: { instrumentation: false } })` before
Jest starts. A standalone environment uses
`testEnvironmentOptions.wiretap.instrumentation: false`. The environment override
`WIRETAP_EXISTING_OTEL=1` also prevents Wiretap from installing client
instrumentation. Session spans and trace contexts remain available; your existing
instrumentation must propagate them. `captureJest` rejects `instrumentation`
because its registration runs after the environment initializes tracing.

Files run with one worker by default. Parallel workers require
`withWiretap(existingConfig, { explicitSession: true })` and W3C trace-context
propagation through every relevant SUT boundary. Jest `.concurrent` tests also
require propagation and isolated fixture state. Instrument the application's
server and outbound clients; a header on the test's first request alone is not
enough. See [tracing](https://github.com/mattlock83/wiretap/blob/main/docs/otel-adoption.md).

Custom environments and watch mode are unsupported. Run separate Jest projects
individually, then combine same-build archives with a
[run set](https://github.com/mattlock83/wiretap/blob/main/docs/run-sets.md).

## Test outcomes and cleanup

Capture includes ordinary `beforeEach`/`afterEach` work. Sync, async, `done`,
generator, parameterized and retry callbacks retain their Jest outcomes,
including timeouts and hook failures.
Retries keep a stable test identity and receive a fresh trace per attempt.
Suite-wide `beforeAll`/`afterAll` work is outside individual attempts; use
per-test hooks for requests that should be captured. A skipped test that never
starts creates no attempt.

Existing global setup runs before Wiretap starts, and existing global teardown
runs after finalization. Test-file environments attach to this runtime. The
owner waits for workers and cleanup before finalizing. Await background work
before a test ends. API contract violations stay separate from framework
outcomes; capture and cleanup errors fail the integration. If cleanup fails
after a test failure, Jest reports both errors with the original failure first.

On handled interruption the adapter attempts finalization and stops its process.
An unfinished attempt leaves an interrupted archive without complete exports.
The owned process keeps a stdin lifeline: if Jest dies, EOF asks Wiretap to
preserve an interrupted archive, but abrupt termination cannot complete normal
exports or save the owner's receipt. Normal shutdown fails cleanup if Wiretap
cannot stop or its finalization receipt is incomplete or mismatched.

## A standalone runtime

If your harness owns the runtime, start `wiretap --config .wiretap.yaml`, select
`testEnvironment: '@wiretap/sdk/jest-environment'` in Jest, and register
`captureJest` from `setupFilesAfterEnv`. Set its `baseUrl` to the control API
address, or use `WIRETAP_BASE_URL`. The environment starts capture and manages
each attempt; your single suite owner must finalize with `POST /v1/finishRun`
after all workers and cleanup finish, then stop the process. Do not also use
`withWiretap` to own this runtime. See the
[runtime API](https://github.com/mattlock83/wiretap/blob/main/docs/runtime-api.md)
for manual suite/test lifecycle calls and proxy options.

## Retain and inspect the evidence

Each run has its own archive, canonical `last-run.json` receipt and bounded
`wiretap.log`; concurrent owners keep independent records. The adapter prints
exact archive and export paths to stderr. Read `bundle_path`, `outputs.coverage`,
`outputs.arazzo` and `outputs.arazzo_files` from that run's receipt. Keep the
complete directory in CI on success and failure:

| Path | Contents |
| --- | --- |
| `bundle.json` | Run identity, API inventory and test references. |
| `tests/` | Immutable attempt recordings and outcomes. |
| `contracts/` | Frozen OpenAPI contracts and imports. |
| `payloads/` | Retained request and response bytes. |
| `exports/coverage.json` | API and contract-condition coverage. |
| `exports/coverage.html` | Offline coverage and test-run dashboard. |
| `exports/arazzo/` | Eligible workflows and OpenAPI source companions. |
| `last-run.json`, `wiretap.log` | This run's receipt and bounded runtime log. |

Completed runs export JSON and HTML coverage plus eligible Arazzo workflows.
Open `outputs.coverage.html` directly in a browser, or regenerate reports offline:

```sh
wiretap report --bundle ./artifacts/wiretap/<run-id> --format text
wiretap report --bundle ./artifacts/wiretap/<run-id> --format html --output report.html
wiretap report --bundle ./artifacts/wiretap/<run-id> --format all
```

Coverage JSON with optional specs and Arazzo can also
[rebuild the dashboard](https://github.com/mattlock83/wiretap/blob/main/docs/artifacts.md#offline-html-dashboard).
Arazzo excludes non-JSON traffic; incomplete eligible captures can fail export.
Raw evidence and other successful outputs remain available. Keep each Arazzo
document and its companions together. See
[artifacts and exports](https://github.com/mattlock83/wiretap/blob/main/docs/artifacts.md)
for eligibility, limits and export errors.

## Check captured mocks against provider tests

Keep complete consumer and provider archives, including their Arazzo exports:

```sh
wiretap check --consumer ./orders-run --provider ./inventory-run \
  --dependency inventory
```

Every selected captured mock request and response needs one complete passing
provider test exchange as evidence. The checker verifies Arazzo against the
archives and validates supported frozen OpenAPI rules where available. It does
not replay requests or execute workflows. A pass covers observed examples,
not every API scenario, workflow or deployment. See
[provider mappings and shared snapshots](https://github.com/mattlock83/wiretap/blob/main/docs/assumption-checks.md#share-provider-snapshots)
and [CI retention and gates](https://github.com/mattlock83/wiretap/blob/main/docs/ci-example.md).
