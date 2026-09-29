# JavaScript test integration

The SDK is a library called from your existing test framework's lifecycle.
It controls a separately running Wiretap service over HTTP and creates standard
trace contexts for capture. It does not discover, schedule, or launch tests.
TypeScript declarations are included. See [configuration](../../docs/configuration.md).

## Native test lifecycle

For a simple Node suite, retain the standard test function and hooks:

```js
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("@wiretap/sdk");
let capture;
before(async () => { capture = await startCapture(); });
after(async () => { if (capture) await capture.finishRun(); });

test("gets an order", () => capture.runTest("gets an order", async session => {
  const response = await fetch(`${session.sutUrl}/orders/123`);
  assert.equal(response.status, 200);
}));
```

Start `wiretap` separately, then run the existing test command with tracing loaded
before client modules:

```bash
node --require @wiretap/sdk/tracing --test test/orders.test.js
```

Retain your existing HTTP client, serialization, response parsing, status handling,
and timeouts. Point it at the session's `sutUrl`. Wiretap instruments Node HTTP/HTTPS
and native fetch. Custom transports need appropriate standard OTel instrumentation.
There is no SDK request/response client.

`runTest` observes only the supplied callback's outcome and guarantees teardown
when it settles. It cannot observe unrelated hooks, assertion-count checks, or a
runner timeout whose callback never settles.

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
actual result, and surface capture/validation errors as test or suite failures.
It closes capture; it does not cancel user callbacks or background work.

For a runner with no executable callback hooks, use `setupTest`/`teardownTest`
and standard OTel context binding, or call the language-independent
[HTTP API](../../docs/runtime-api.md). Manual setup alone does not activate a
span around unrelated callbacks. Use a new handle/root for every retry.

## Optional Node adapter

`createNodeTestSuite` and `bindTest` provide runner-specific convenience when
wanted. They require Node 20.12+ and account for callback, subtest, `t.after`, and
timeout outcomes. They delegate test registration to `node:test` and are not
required by the core library. Existing hooks retain their ordering.

## Optional Jest adapter and concurrency

Use `createJestSuite` from `@wiretap/sdk/jest` with
`testEnvironment: "@wiretap/sdk/jest-environment"`. The Jest 30 adapter supports
async callbacks, `.concurrent`, `.each`, `.only`, `.skip`, and retries.
Ordinary tests default to serial capture. Use `--runInBand` for an uninstrumented
shared SUT. Concurrent tests use explicit sessions; set `explicitSession: true`
for multiple workers sharing a SUT that propagates W3C context.

The wrapper activates context for the callback and optional
`testDefaults.onSession`. Separate Jest `beforeEach`/`afterEach` requests are
outside that trace; put captured setup/cleanup within the callback. See
[tracing adoption](../../docs/otel-adoption.md) for examples and limitations.

## Configuration and ownership

Start the binary with `wiretap [--config path]` using the existing harness. The
SDK connects through HTTP at `WIRETAP_BASE_URL` or `http://127.0.0.1:18000`.
It never invokes the CLI or launches the test process. `.wiretap.yaml` supplies
shared defaults; SDK options can override them.

A single suite can call `startCapture` in suite setup and `finishRun` in suite
cleanup. When multiple suites/workers share a run, set `manageRuntime: false`
in worker integrations. Their `finishRun` calls check local cleanup but leave
the shared capture running. One global suite owner calls `/v1/finishRun` after
all workers have completed. The optional Jest adapter always uses this shared
mode; configure Jest global teardown as shown in the
[example](../../examples/basic-rest/jest-teardown.cjs).

`finishRun` finalizes the bundle and stops the capture proxy. It does not stop
the HTTP control process. Your harness owns that process and the SUT/mocks.
No environment flag changes run ownership implicitly.

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

API-provided file paths resolve on the runtime host. Schemas are required by
default. Set `schemaRequired: false` only on an API whose spec is intentionally
absent; supplied specs still validate strictly. Dependencies retain this same
per-API requirement. Dependencies marked `upstreamKind: "mock"` automatically get
a separate inferred OpenAPI draft in the bundle, even with a supplied spec.
The draft documents observations and never changes validation. Follow the
Dependencies links in the bundle to inspect it.

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
- `runtime.report({ bundlePath })` refreshes bundle views.
- `runtime.finishRun()` finalizes capture when `manageRuntime` is enabled.
  It never terminates the control process.

Sessions expose `runId`, `bundlePath`, `indexPath`, and `viewerPath`. Teardown
returns `attempt_path` and `attempt_page_path`. Capture drains accepted requests
for five seconds by default; `drainTimeoutMs` selects 0–60000ms. Incomplete
capture and enforced schema failures fail cleanup. An original callback error
is preserved if cleanup also fails. Explicitly omitted schemas show skipped or
partial validation without turning unrelated schema failures into success.
