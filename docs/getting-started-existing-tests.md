# Capture existing tests

Wiretap runs beside your existing test suite and produces raw evidence, Postman
collections and API coverage. Use isolated services, synthetic data and dummy
secrets: captured traffic is retained as sent.

Choose the integration for your test runner:

| Runner | Smallest setup |
| --- | --- |
| [Jest](../sdk/js/docs/jest.md) | Wrap the config and point your shared client at `WIRETAP_SUT_URL`. |
| [Playwright Test](../sdk/js/docs/playwright.md) | Wrap the config and import the supplied `test` fixture. |
| [Mocha](../sdk/js/docs/mocha.md) | Wrap the config and point your shared client at `WIRETAP_SUT_URL`. |
| [Plain JavaScript / Node](../sdk/js/docs/javascript.md) | Use `startManagedRuntime` in the parent harness and the lifecycle API in tests. |
| [Go `testing`](../sdk/go/README.md) | Add `wiretap.Run(m)` in `TestMain` and `wiretap.StartTest(t)` in your shared test setup. |

The runner adapters start Wiretap, record final test outcomes and finalize exports
around your usual command. The [external Express/Jest demo](../examples/existing-jest/README.md)
checks 120 unchanged tests; the [Playwright Todo example](../examples/playwright-runner/README.md)
provides a small API suite you can copy. The [Go example](../examples/go-suite/README.md)
uses an ordinary HTTP handler and client, with one managed capture per package.
The [Mocha example](../examples/mocha-runner/README.md) keeps ordinary tests and
fixtures with one configuration wrapper.

The Node integration below shows a project-owned alternative with two test files
and one parent harness. For native gRPC, use the [gRPC guide](grpc-user-guide.md).

## 1. Run the complete adoption example

From the Wiretap checkout, with Go 1.26+ and Node.js 22+:

```bash
mkdir -p dist
go build -o dist/wiretap ./cmd/wiretap
npm ci --prefix sdk/js
cd examples/node-suite
npm install --no-save ../../sdk/js
node run-tests.mjs --binary ../../dist/wiretap
```

On Windows, build `dist/wiretap.exe` and pass `../../dist/wiretap.exe`.
No package publication is needed. For a portable SDK tarball and versioned
binaries, see [local installation](local-release.md).

The [complete example](../examples/node-suite/README.md) starts a synthetic
Orders service and Wiretap, checks readiness, runs **two separate test files**,
then finalizes once and stops both services. One test gets an order; the other
checks that an invalid ID is rejected with `400`. Both pass, while the invalid
request remains visible in validation evidence.

Copy `examples/node-suite/` into your test project, install the local SDK there,
and adapt `service.mjs`, `.wiretap.yaml` and the test command in `run-tests.mjs`
to your existing services and tests. `run-tests.mjs` is ordinary project-owned
orchestration, not a new test runner.

## 2. Configure the boundaries

Put `.wiretap.yaml` in the test project:

```yaml
version: 1
application: {id: orders-app, name: Orders}
outputs:
  coverage: [json, text]
  postman: true
sut:
  id: orders
  url: http://127.0.0.1:8080
  kind: service
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
```

The SUT is the system under test. Schema paths resolve relative to this file.
The main HTTP API requires OpenAPI by default. HTTP dependencies need no spec
or opt-out flag; add `schema` to opt into their validation and contract coverage,
or `schema_required: true` to require one. Without a dependency spec, calls and
Postman examples are retained with unknown contract coverage. A supplied invalid
spec fails setup. Omit `dependencies` for inbound-only capture.
Use stable API IDs for later comparison across repositories.

The managed runner adapters and Go's `Run` discover this configuration and
choose available local ports. The plain Node example uses
`startManagedRuntime({ binary, cwd: project, outputDir: artifacts })` for the
same startup/readiness/finalization behavior. Its parent starts the application,
passes the returned control address to workers as `WIRETAP_BASE_URL`, and calls
`owner.close()` after the test process exits. Keep the application and mocks
alive until that cleanup finishes.

Configure the application's inventory URL with the returned dependency capture
address: JavaScript exposes `capture.dependencyUrl("inventory")`, and Go exposes
`session.Dependencies["inventory"]`. Wiretap forwards it to the configured mock.
Tests call `session.sutUrl` in JavaScript or `session.SUTURL` in Go. Your mock
tooling owns its stubs and verification. For an externally managed process,
see [configuration](configuration.md).

## 3. Wrap each test with the outcome-aware adapter

Each test file in a shared run uses `manageRuntime: false`:

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@wiretap/sdk");
const suite = createNodeTestSuite({ manageRuntime: false });

suite.test("gets an order", async (t, session) => {
  const response = await fetch(`${session.sutUrl}/orders/123`);
  assert.equal(response.status, 200);
  // Keep existing fixtures, assertions and mock verification here.
  // t.after callbacks contribute to the final recorded test outcome.
});
```

Load tracing before HTTP clients, and initially run files sequentially:

```bash
node --require @wiretap/sdk/tracing --test --test-concurrency=1 test/orders.test.cjs test/invalid-order.test.cjs
```

Use this command inside the parent harness: it starts a run before launching
workers and calls `owner.close()` after the test process exits, including after
failure. Workers finish their own attempts; only the parent finalizes the run.
The [complete harness](../examples/node-suite/run-tests.mjs) preserves the test
process's original nonzero exit code if finalization also fails.

The adapter observes callbacks, subtests, `t.after` and timeouts. Put per-test
capture fixtures inside the adapter callback or its `t.after` callbacks;
separate suite-wide hooks are outside individual attempts. For another
framework, choose its [runner adapter or lifecycle handles](../sdk/js/README.md).
Await requests and expected dependency work before finishing each attempt.

For parallel tests sharing an application, propagate W3C `traceparent` from its
server to outbound clients. Otherwise keep the suite sequential or isolate each
worker's service and Wiretap runtime. See [tracing](otel-adoption.md).

## 4. Read and keep the results

The harness prints exact paths and a compact coverage summary. Read it in this
order: test outcomes, operations exercised, condition gaps, unknown evidence,
then validation diagnostics. A passing negative test can have request validation
violations and valid error-response evidence; these are separate facts.

The Node example and managed JavaScript runners write `last-run.json` and
`wiretap.log` in their configured output directory. Go's managed integration
keeps those files inside each package's unique run archive. Retain the entire
output directory; use a separate root for independent concurrent JavaScript
suites. Each completed run has:

```text
artifacts/<run-id>/
  bundle.json
  tests/                  test outcomes and captured traffic
  contracts/, payloads/   frozen specs and retained bytes
  exports/coverage.txt    compact first-result summary
  exports/coverage.json   machine-readable API coverage
  exports/postman/        Postman collections
  exports/result.json     exact output paths and export errors
```

Upload the complete directory on success **and failure**. The
[CI recipe](ci-example.md) uses the same runnable harness and unconditional
artifact upload. Rebuild the summary offline with:

```bash
wiretap report --bundle artifacts/<run-id> --format text
```

A mock response is a recorded assumption. To verify its examples against the
provider's own tests, share complete runs and Postman exports under application
folders, then use `wiretap check consumer --consumer <run> --artifacts <root>`
with the selected snapshots restored locally. See the
[assumption checker](assumption-checks.md) for provider mappings and selection. The
[machine interface](machine-interface.md) explains identities, versions,
provenance and evidence limits.
