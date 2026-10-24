# Plain JavaScript and Node test runner

Use the framework-neutral API when your harness owns startup and cleanup.
The [Jest](jest.md), [Playwright Test](playwright.md) and [Mocha](mocha.md) adapters
automate that lifecycle for their runners. The core API remains available unchanged.

## Node test runner

Use the outcome-aware adapter with Node 22+. It observes callback, subtest,
`t.after` and timeout results while retaining the framework's test tree.
For a single test file that owns its runtime:

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@wiretap/sdk");
const suite = createNodeTestSuite();

suite.test("gets an order", async (t, session) => {
  const response = await fetch(`${session.sutUrl}/orders/123`);
  assert.equal(response.status, 200);
  // Keep assertions and per-test fixtures here; t.after cleanup is observed.
});
```

Start Wiretap separately, then load tracing before client modules:

```bash
node --require @wiretap/sdk/tracing --test test/orders.test.js
```

For multiple files/workers, use `createNodeTestSuite({ manageRuntime: false })`
in each worker and let a parent harness finalize once. Copy the
[complete two-file suite](https://github.com/mattlock83/wiretap/tree/main/examples/node-suite)
for startup, readiness, test execution, finalization on failure and CI paths.
Per-test fixtures belong in the captured callback or its `t.after` callbacks;
suite-wide hooks run outside individual attempts. Existing hook order is retained.

Retain your existing HTTP client, serialization, response parsing, status handling
and timeouts. Point it at the session's `sutUrl`. Wiretap instruments Node HTTP/HTTPS
and native fetch. Custom transports need appropriate standard OTel instrumentation.
There is no SDK request/response client.

Use synthetic data and dummy credentials. Wiretap retains captured traffic as
sent and has no built-in redaction. Deliberate invalid requests can pass their
framework assertions while retaining separate contract-validation violations.

For a callback-only integration, `runtime.runTest` records that callback's
outcome and guarantees teardown when it settles. It cannot observe unrelated
hooks, assertion-count checks, or runner timeouts whose callbacks never settle;
prefer the adapter when those are part of your framework lifecycle.

## Integrating another framework

Use `capture.createTestAttempt(name, options)` for an independent lifecycle
handle. Wire these operations into your framework's existing events:

| Framework event | SDK call |
| --- | --- |
| Attempt starts (including each retry) | `attempt = capture.createTestAttempt(name, options)` |
| Setup, test, or cleanup callback | `await attempt.run(session => callback(session))` |
| Final completion, failure, skip, or timeout | `await attempt.finish({ status, error })` |
| Entire suite finishes, including all workers | Run owner calls `capture.finishRun()` |

Keep each handle with its framework test attempt; do not use a shared mutable
current-test variable for concurrent tests. `run` can activate the same attempt's
context around separate fixture callbacks. `finish` is idempotent and can run
even if a timed-out callback has not settled. Await it, pass the framework's
actual result, and surface capture errors as test or suite failures. Contract-validation results
remain separate from the framework outcome.
It closes capture; it does not cancel user callbacks or background work.

For a runner with no executable callback hooks, use `setupTest`/`teardownTest`
and standard OTel context binding, or call the language-independent
[HTTP API](https://github.com/mattlock83/wiretap/blob/main/docs/runtime-api.md). Manual setup alone does not activate a
span around unrelated callbacks. Use a new handle/root for every retry.

## Selective Jest capture

Use `createJestSuite` from `@wiretap/sdk/jest` with
`testEnvironment: "@wiretap/sdk/jest-environment"`. The explicit adapter supports
async callbacks, `.concurrent`, `.each`, `.only`, `.skip`, and retries.
Ordinary tests default to serial capture. Use `--runInBand` for an uninstrumented
shared SUT. Concurrent tests use explicit sessions; set `explicitSession: true`
for multiple workers sharing a SUT that propagates W3C context.

The environment activates the same attempt context for the test and ordinary
Jest `beforeEach`/`afterEach` hooks, including nested suites. Sync, promise/async,
and `done` fixture hooks retain their ordering and runner outcomes. The optional
`testDefaults.onSession` runs once before the first captured callback. Jest does
not run per-test hooks for `.concurrent` tests; put their fixtures in the test
callback with `try/finally`. Suite-wide `beforeAll`/`afterAll` work is outside
individual attempts. See [tracing adoption](https://github.com/mattlock83/wiretap/blob/main/docs/otel-adoption.md).

## Configuration and ownership

Start the binary with `wiretap [--config path]` using the existing harness. The
SDK connects through HTTP at `WIRETAP_BASE_URL` or `http://127.0.0.1:18000`.
The lower-level `startCapture` client never launches processes. `.wiretap.yaml`
supplies shared defaults; SDK options can override them. For managed local
ownership in a custom harness, `startManagedRuntime` from `@wiretap/sdk/managed-runtime`
returns `{ baseUrl, sutUrl, runtimeInfo, close }`; await `close()` in a `finally`
block. It owns only Wiretap, leaving the application and test runner to your harness.

A single suite can call `startCapture` in suite setup and `finishRun` in suite
cleanup. When multiple suites/workers share a run, set `manageRuntime: false`
in worker integrations. Their `finishRun` calls check local cleanup but leave
the shared capture running. One global suite owner calls `/v1/finishRun` after
all workers have completed. Jest test workers always use this shared
mode. `withWiretap` supplies the global owner automatically; for an externally
managed runtime, configure global teardown as shown in the
[example](https://github.com/mattlock83/wiretap/blob/main/examples/basic-rest/jest-teardown.cjs).

`finishRun` finalizes the bundle and stops the capture proxy. It does not stop
the HTTP control process. Your harness owns that process and the SUT/mocks.
No environment flag changes run ownership implicitly.

Control API requests have a 30-second deadline by default. A default teardown
deadline expands to the requested drain plus ten seconds when necessary.
`timeoutMs` overrides every control deadline; set `timeoutMs: 0` to opt out.
Choose a larger positive value for slow contract discovery or finalization.
Timeouts reject with `code: "WIRETAP_CONTROL_TIMEOUT"` and identify the control
operation; surface these errors through your test or suite cleanup.

SDK configuration can override project defaults, or supply the full setup when
there is no `.wiretap.yaml`:

```js
const capture = await startCapture({
  sutBaseUrl: "http://127.0.0.1:8080",
  schemas: [{ id: "orders", type: "openapi",
    source: { type: "file", path: "/project/schemas/orders.yaml" } }],
  dependencies: {
    inventory: { upstreamUrl: "http://127.0.0.1:9090", upstreamKind: "mock",
      schema: { id: "inventory", type: "openapi",
        source: { type: "file", path: "/project/schemas/inventory.yaml" } } }
  }
});
```

For a gRPC project, `.wiretap.yaml` supplies `protocol: grpc`, service selection
and reflection or local Protobuf sources. The SDK inherits those defaults and
returns direct capture origins in `sutUrl` and `dependencyUrl`; use your existing
generated gRPC clients. `sutProtocol: "grpc"` and a dependency's
`protocol: "grpc"` pass through configured project boundaries; they cannot
create or retarget a gRPC boundary per test. gRPC contracts cannot be disabled and this SDK
does not instrument `@grpc/grpc-js` automatically; use standard OTel gRPC
instrumentation for parallel test ownership. See the [gRPC guide](https://github.com/mattlock83/wiretap/blob/main/docs/grpc-user-guide.md).

The [native Node gRPC example](https://github.com/mattlock83/wiretap/blob/main/examples/grpc-boundaries/node/README.md)
tests this with ordinary `@grpc/grpc-js` clients and mocks, local proto sources,
parallel calls, an HTTP provider calling a gRPC mock, and TLS. Its
[`tracing.cjs`](https://github.com/mattlock83/wiretap/blob/main/examples/grpc-boundaries/node/tracing.cjs) initializes
application-owned standard gRPC/HTTP instrumentation before client modules are
loaded; `startCapture({ instrumentation: false })` reuses that existing setup.
The gRPC packages stay in the example, outside the SDK's dependencies. No
Wiretap-specific RPC client, collector or reflection service is required.

API-provided file paths resolve on the runtime host. HTTP schemas are required by
default. Set `schemaRequired: false` only on an API whose spec is intentionally
absent; supplied specs still validate strictly. Dependencies retain this same
per-API requirement.

## Lower-level integration

- `startCapture(config)` connects to the runtime and loads project defaults.
- `runtime.createTestAttempt(name, options)` supplies a framework-neutral
  context/completion handle, described above.
- `runtime.runTest(name, async session => { ... }, options)` activates the test
  context and guarantees teardown after the callback.
- `runtime.setupTest({ name, ...options })` / `teardownTest({ status })` support
  custom fixtures. Manual setup registers a span but does not activate it around
  arbitrary requests; use `runTest` or standard OTel context binding.
- `runtime.dependencyUrl(assetId)` returns the configured dependency capture URL.
- `runtime.report({ bundlePath })` returns a JSON coverage report for the archive.
- `runtime.finishRun()` finalizes capture when `manageRuntime` is enabled.
  It never terminates the control process.

Sessions expose `runId` and `bundlePath`. Teardown returns `attempt_path` for
the recorded JSON evidence. Capture drains accepted requests
for five seconds by default; `drainTimeoutMs` selects 0–60000ms. Incomplete
capture fails cleanup; schema mismatches are retained as validation evidence
without changing the framework outcome. An original callback error
is preserved if cleanup also fails. Explicitly omitted schemas show skipped or
partial validation without changing validation findings for supplied schemas.

### Generate suite outputs

Configure `outputs: {coverage: ["json", "text"], postman: true}` in `startCapture`, or the
matching `outputs` block in `.wiretap.yaml`. `await capture.finishRun()` returns
`outputs.coverage` and `outputs.postman` with exact server-side paths. Shared
workers leave this to the global run owner. Cleanup errors retain `error.body`
with available outputs and archive path; preserve an original test error as well.
