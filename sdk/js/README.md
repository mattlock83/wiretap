# Jest SDK

Add HTTP API capture to your existing Jest tests. Wiretap records a portable run
archive, JSON/REST Arazzo workflows and API coverage. Keep your assertions, mocks
and HTTP clients.

The adapter starts Wiretap on available ports, captures each executed test,
finalizes exports after cleanup, and stops its process. Jest owns the test
outcomes. Failed assertions, timeouts and cleanup failures remain visible;
API contract violations are separate evidence.

## Install and configure once

Use Node.js 22+ and Jest 29.7 or 30. Install the local SDK tarball alongside Jest
and put the Wiretap executable on `PATH`, or set `WIRETAP_BINARY` to its absolute
path:

```sh
npm install --save-dev /absolute/path/to/wiretap-sdk-0.1.0.tgz
```

See [local installation](https://github.com/mattlock83/wiretap/blob/main/docs/local-release.md)
for building the binary and tarball. The SDK includes TypeScript declarations
and downloads no executable.

Put `.wiretap.yaml` and your API contract in the Jest project:

```yaml
version: 1
application: {id: my-api}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text, html], arazzo: true}
sut:
  id: my-api
  kind: service
  schema: openapi.yaml
```

Wrap your existing Jest configuration:

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');
module.exports = withWiretap(existingConfig, { app: '<rootDir>/app.js' });
```

`app` exports your existing Express/Node HTTP handler. It loads inside Jest so
existing mocks work. Change a shared Supertest client from `supertest(app)` to
`supertest(process.env.WIRETAP_SUT_URL || app)`, then run your usual Jest command.
For an already running API, omit `app` and add its `url` under `sut`.
See the [Jest guide](docs/jest.md) for dynamic fixtures, retries, tracing and
selective capture, or run the [existing Jest example](https://github.com/mattlock83/wiretap/tree/main/examples/existing-jest).

The main HTTP API requires an OpenAPI spec by default. HTTP dependencies and
mocks need only their URL and kind; supplying a dependency schema enables its
validation and contract coverage. Every supplied spec is validated. Use
synthetic data and dummy credentials: captured traffic is retained unchanged.
See [boundary configuration](https://github.com/mattlock83/wiretap/blob/main/docs/configuration.md)
for mock capture and provider mappings.

The package root and `@wiretap/sdk/jest` expose `withWiretap`, `captureJest` and
`createJestSuite`. `@wiretap/sdk/tracing` initializes instrumentation before an
application imports its clients. `@wiretap/sdk/jest-environment` supplies the
Jest environment used by the adapter.

## Keep the outputs

After successful finalization, the adapter prints exact archive, coverage and
Arazzo paths to stderr. Retain the complete output directory in CI on success
and failure. Each unique run contains its canonical `last-run.json` receipt,
bounded `wiretap.log`, captured traffic, frozen contracts and exports. The
output-root receipt is an advisory latest snapshot; concurrent runs keep their
own records. Read `bundle_path`, `outputs.coverage`, `outputs.arazzo` and
`outputs.arazzo_files` from the receipt for exact artifact paths.

Coverage defaults to JSON, text and HTML. Open `outputs.coverage.html` directly
in a browser for API coverage, condition gaps and linked test runs. The dashboard
works offline; no report server is needed. Set `capture.outputs.coverage` or
project `outputs.coverage` to select formats.

The owned process keeps a stdin lifeline to Wiretap. If Jest dies, EOF asks
Wiretap to preserve an interrupted archive. Abrupt termination cannot complete
normal exports or save the owner's receipt. Normal shutdown fails cleanup if
Wiretap cannot stop or its finalization receipt is incomplete or mismatched.

Jest uses one worker by default. Parallel workers and concurrent tests require
explicit sessions and W3C trace propagation through captured application
boundaries. See the [Jest guide](docs/jest.md).
