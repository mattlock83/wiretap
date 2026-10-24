# JavaScript SDK

Add API capture to your existing tests. Wiretap produces a portable run archive,
Postman collections and API coverage from the requests your tests already make.
Keep your assertions, mocks and HTTP clients.

| Your test runner | Integration |
| --- | --- |
| [Jest](docs/jest.md) | Wrap the config and change your shared client's URL. Existing test imports stay unchanged. |
| [Playwright Test](docs/playwright.md) | Wrap the config and change the `test` import in tests or a shared fixtures module. |
| [Mocha](docs/mocha.md) | Wrap the config and change your shared client's URL. Existing test imports stay unchanged. |
| [Plain JavaScript / Node test runner](docs/javascript.md) | Use the existing lifecycle API or `createNodeTestSuite` with a project-owned harness. |

The Jest, Playwright and Mocha adapters start Wiretap on available ports, capture
test attempts, finalize exports after test cleanup, and stop the process. Your runner owns
execution and outcomes. Failed assertions, timeouts and cleanup failures remain
visible; API contract violations are separate evidence.

## Install and configure once

The SDK requires Node.js 22+; CI covers Node 22 and 24. Mocha 12 requires 22.12+.
Supported adapters are Jest 29.7 or 30, Playwright Test 1.64.1+ within 1.x, and
Mocha 11 (11.7.5+) or 12. Install the local SDK tarball alongside your existing
runner and put the Wiretap executable on `PATH`, or set
`WIRETAP_BINARY` to its absolute path:

```sh
npm install --save-dev /absolute/path/to/wiretap-sdk-0.1.0.tgz
```

See [local installation](https://github.com/mattlock83/wiretap/blob/main/docs/local-release.md)
for building the binary and tarball. The SDK does not download binaries or install
optional test runners. TypeScript declarations are included.

Put `.wiretap.yaml` and your API contract in the test project. The runtime also
discovers this configuration in parent directories:

```yaml
version: 1
application: {id: my-api}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text], postman: true}
sut:
  id: my-api
  kind: service
  schema: openapi.yaml
  # Add url: http://127.0.0.1:3000 for an already running API.
```

Use synthetic data and dummy credentials. Captured traffic is retained unchanged.
For downstream mock capture, gRPC and tracing, see
[boundary configuration](https://github.com/mattlock83/wiretap/blob/main/docs/configuration.md)
and the [plain JavaScript guide](docs/javascript.md).

The main HTTP API requires its OpenAPI spec by default. HTTP dependencies and
mocks need only their URL and kind; add a dependency `schema` to opt into
validation and contract coverage. No opt-out flag is needed for schema-free
dependency capture. Supplied specs always validate. gRPC still needs a Protobuf
descriptor, discovered through reflection when no source file is supplied.

For an HTTP inventory mock, add this alongside `sut` in the same file:

```yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
```

Point the application's inventory client at the returned dependency capture URL
so its calls are captured. The plain API exposes `capture.dependencyUrl("inventory")`;
runner hooks/fixtures expose the session's dependency URLs. See
[HTTP dependency schema options](docs/javascript.md#http-dependency-schema-options)
to add a spec or require one. Without a spec, calls and Postman examples remain
available, while that dependency's contract coverage is unknown.

## Jest

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');
module.exports = withWiretap(existingConfig, { app: '<rootDir>/app.js' });
```

Change the shared API client from `supertest(app)` to
`supertest(process.env.WIRETAP_SUT_URL || app)`, then run your normal Jest command.
The app loads inside Jest so existing mocks work. Omit `app` when your harness
already hosts the API. [Jest guide →](docs/jest.md)

## Playwright Test

```js
// playwright.config.js
const { withWiretap } = require('@wiretap/sdk/playwright');
module.exports = withWiretap(existingConfig);
```

```js
// tests or your shared fixtures module
const { test, expect } = require('@wiretap/sdk/playwright');
```

Keep the existing `use.baseURL` and `webServer` settings. Relative requests from
`request` and browser fixtures route through capture. Use
`capturePlaywright(existingTest)` when you already extend the test fixture.
[Playwright guide →](docs/playwright.md)

## Mocha

```js
// .mocharc.cjs
const { withWiretap } = require('@wiretap/sdk/mocha');
module.exports = withWiretap(existingConfig, { app: './app.js' });
```

Point the shared API client at `process.env.WIRETAP_SUT_URL`, then run your normal
Mocha command. Omit `app` if your suite already hosts the API; in that case,
`mocha --require @wiretap/sdk/mocha` also works with `.wiretap.yaml`.
[Mocha guide →](docs/mocha.md)

## Plain JavaScript

The existing API stays available for custom lifecycles:

```js
const { startCapture } = require('@wiretap/sdk');
const capture = await startCapture(); // connects to your running Wiretap
await capture.runTest('lists todos', async ({ sutUrl }) => {
  // Use your existing client and assertions here.
});
await capture.finishRun();
```

This shows the core calls. For complete startup, failure handling and multi-file
ownership, use the [JavaScript guide](docs/javascript.md) and
[copyable Node suite](https://github.com/mattlock83/wiretap/tree/main/examples/node-suite).
Framework integrations should use `createTestAttempt` to observe final runner
outcomes independently of callback settlement.

## Keep the outputs

Retain the complete output directory in CI on success and failure. Managed
JavaScript runs put the latest `last-run.json` receipt and bounded `wiretap.log`
beside the unique run archive directories:

```text
artifacts/wiretap/
  last-run.json
  wiretap.log
  run-.../
```

The receipt's `bundle_path`, `outputs.coverage` and `outputs.postman` identify the
exact archive and export paths. Each archive contains raw traffic, contracts and
per-attempt outcomes. Use different `outputDir` values for independent runs that
execute concurrently so their latest receipt and log do not overwrite each other.
The plain lifecycle API returns receipts; its harness chooses where to save them.

Managed JavaScript runs keep a stdin lifeline to Wiretap, as the Go SDK does.
If the owning runner process dies, EOF asks Wiretap to stop and preserve an
interrupted raw archive. Abrupt termination cannot complete normal exports or
save the owner's receipt: `last-run.json` may be absent or refer to an earlier
run. Killing Wiretap itself can leave an open archive if shutdown cannot finish.
Normal managed shutdown force-kills a child that exceeds its shutdown timeout.

Jest and Playwright default to one worker. Parallel capture requires explicit
sessions and W3C trace propagation through captured application dependencies.
Mocha capture currently supports serial runs. The runner guides document fixture
scope, retries, interruption and configuration limits.
