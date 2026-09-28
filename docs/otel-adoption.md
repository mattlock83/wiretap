# Adopting Phrond with test traces

Phrond uses standard W3C trace context to associate captured HTTP exchanges with
individual test attempts. Start with the [existing-tests guide](getting-started-existing-tests.md),
then choose a runner integration below. No collector or exporter is required.

## What teams keep

Keep the existing application, HTTP client, assertions, and mock server. Phrond
adds test lifecycle integration and captures HTTP traffic routed through its
proxy. Existing mock tools still own stubs, responses, verification, and lifecycle.

No application or mock source change should be required specifically for Phrond.
Some test harness and endpoint configuration is required; this is not a promise
that every test suite can run with completely unchanged test code.

Phrond uses standard W3C `traceparent` and `tracestate` propagation.
Phrond's internal session and flow IDs remain, along with explicit test-to-Phrond
session selection. The SUT never needs to propagate those internal IDs.

## A test is the root of a trace

Each test attempt, including a retry or repeated execution, gets a fresh root
span and trace ID. The adapter associates that trace with a capture session and
records the test name and outcome. A span represents an operation; a trace groups
related spans. The test's HTTP client sends requests within the test's context.

```text
Test attempt: creates an order                 one trace / capture session
  Test → Phrond → SUT: POST /orders             request + response
  SUT → Phrond → existing inventory mock       request + response
  Test → Phrond → SUT: GET /orders/123          request + response
```

When the SUT already propagates standard context from its inbound server to its
outbound HTTP clients, its dependency requests retain the test's trace ID.
Phrond can associate those exchanges with the test, including when tests overlap.
The mock only needs to receive normal HTTP requests; it does not need OTel.

Phrond still records request and response payloads at the proxy. OTel supplies
context for correlation, not full body capture. No collector, exporter, or
tracing backend is required for this feature, and capture must work even when
trace sampling is disabled. Inspect existing SUT spans in the team's tracing backend; Phrond records
proxy exchanges and their context without importing those spans.

One test trace can contain several inbound SUT requests. Knowing the test does
not always prove which inbound request caused a dependency call: the SUT's
internal span ancestry may be invisible to the proxy. Phrond shows test
ownership established by trace IDs separately from unique-candidate or
timing-based parent inferences and ambiguity.

## What users need to do

1. **Connect test setup and teardown.** Use the runner adapter or helper
   to start each attempt's trace and capture session, then record pass/fail.
2. **Route both sides through Phrond.** Point test requests at the SUT capture
   endpoint and configure the SUT's dependency endpoints to the mock capture
   routes. Keep the actual SUT and mock addresses as forwarding destinations.
3. **Configure client instrumentation.** Retain the existing client while adding
   the instrumentation appropriate to its HTTP stack, as described below.
4. **Choose a concurrency setup.** Verify actual inbound-to-outbound propagation
   before sharing one SUT across parallel tests; otherwise isolate or serialize.
5. **Await the test's work before teardown.** Wait for HTTP calls and any expected
   background dependency activity. Closing the test early cannot establish the
   ownership of later work reliably.
6. **Supply schemas.** Configure specs in `.phrond.yaml`. Generation is an
   explicit SUT option; an individual API can explicitly omit a spec with
   `schema_required: false`. See [configuration](configuration.md).

Your harness starts the Phrond process; test framework hooks call its HTTP API
through the SDK to start and finalize capture. Set `PHROND_BASE_URL` when needed.
One suite-level owner finalizes after all workers finish. Serial capture remains the default, with one implicit session. Shared-SUT parallel
mode uses explicit sessions and a stable dependency capture URL, configured once
in the SUT; Phrond selects the owning session using each request's trace. Select
that mode only when propagation works on the relevant application path.

### Node.js and the optional `node:test` adapter

Install the SDK dependencies (`npm ci --prefix sdk/js` when working in this
repository). For CommonJS tests, load instrumentation before HTTP client modules:

```bash
node --require @phrond/sdk/tracing --test test/*.test.js
```

```js
const { createNodeTestSuite } = require("@phrond/sdk");
const assert = require("node:assert/strict");
const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [ordersOpenAPI],
  dependencies: { inventory: { upstreamUrl: process.env.MOCK_URL, schema: inventoryOpenAPI } },
  explicitSession: true, // only when the SUT propagates W3C context
  manageRuntime: false  // shared process: orchestration closes it after all workers
});
phrond.test("creates an order", async () => {
  const response = await fetch(`${phrond.sutUrl}/orders`, {
    method: "POST", headers: { "content-type": "application/json" },
    body: JSON.stringify({ productId: "coffee", quantity: 1 })
  });
  assert.equal(response.status, 201);
});
```

The SDK instruments Node `http`/`https` and native `fetch` through
standard HTTP and Undici instrumentation. Keep existing calls and assertions;
Other clients work when they use an instrumented stack, but custom transports, ESM module hooks, and in-process mock interception
need separate verification. The SDK requires Node 18.19+ or 20.6+; its
`node:test` adapter requires **Node 20.12+** for the runner's
[final outcome API](https://nodejs.org/api/test.html#contextpassed).
HTTP and native fetch are covered by SDK integration tests; HTTPS capture is
also covered by the proxy's decrypted HTTPS tests.

For an existing OTel setup, initialize it first and pass `instrumentation: false`
to `startCapture`/`createNodeTestSuite`; omit the Phrond preload to avoid duplicate
instrumentation. Phrond reuses an existing global provider/context manager. You
can also pass `tracerProvider`. A supplied provider must create valid contexts;
an explicitly supplied no-op provider fails setup. Sampling can be disabled.

`runtime.runTest(name, async session => { ... }, options)` is the lower-level
wrapper for other promise-based runners. It creates an isolated async context,
registers a fresh root and finalizes capture even when the callback throws.
This generic wrapper sees callback errors only. To integrate other framework
hooks, use `runtime.createTestAttempt(name, options)`, run each captured callback
with `attempt.run`, and call `attempt.finish({ status, error })` from the
framework's completion hook, including timeouts. Keep a separate handle for
every attempt/retry. The optional Node/Jest adapters implement their own outcome
integration. With `node:test`, `t.after`
cleanup and nested `t.test` failures contribute to the owning attempt. The
original test tree and hook registration order are preserved.
`setupTest`/`teardownTest` remain available for manual serial fixtures. Manual
setup does not activate a global test span; use `runTest` for ordinary client
instrumentation, or explicitly activate its span with standard OTel context APIs.

### Optional Jest adapter

Use the provided Node environment so Jest's VM and native HTTP stack share the
same OTel context. This is tested with Jest 30; browser/jsdom environments are
not supported by this adapter.

```js
// jest.config.cjs
module.exports = { testEnvironment: "@phrond/sdk/jest-environment" };
```

```js
const { createJestSuite } = require("@phrond/sdk/jest");
const phrond = createJestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [ordersOpenAPI],
  dependencies: { inventory: { upstreamUrl: process.env.MOCK_URL, schema: inventoryOpenAPI } }
});
phrond.test.concurrent.each(["coffee", "tea"])("gets %s", async product => {
  const response = await fetch(`${phrond.sutUrl}/products/${product}`);
  expect(response.status).toBe(200);
});
```

The adapter supports ordinary tests, `.concurrent`, `.each`, `.only`,
`.skip`, and Jest retries. Each attempt uses its full describe/test name and
each table row gets its resolved title. Promise/async callbacks are supported;
Jest's `done` callback style is not supported here. Completion comes from Jest's
runner, including timeouts, assertion-count checks, and per-test hook failures.
Retries create new roots even if the old callback has not finished.

The wrapper covers the test callback and its optional `testDefaults.onSession`
hook. Put captured setup and cleanup inside that callback using `try/finally`;
requests in separate Jest `beforeEach`/`afterEach` hooks are outside the trace.
Ordinary tests default to serial capture. For an uninstrumented shared SUT,
run Jest with `--runInBand` and avoid `.concurrent`. Concurrent tests always
use explicit sessions; set `explicitSession: true` on the suite for multiple
Jest workers sharing a traced SUT. Only enable parallel tests after confirming
SUT propagation, or isolate each worker's runtime and SUT.

Jest suites always use `manageRuntime: false`. Start Phrond with the existing
harness and call `POST /v1/finishRun` from Jest global teardown after **all**
workers finish. See the [example teardown](../examples/basic-rest/jest-teardown.cjs).
Bundles and views are automatic; schema exports are optional. Worker teardown
never kills another worker's proxy. Existing
host-level OTel instrumentation can be preloaded with `NODE_OPTIONS`; set
`PHROND_EXISTING_OTEL=1` to keep the Jest environment from installing duplicates.

### Go tests and `httptest`

Keep the standard `http.Client` and wrap its transport with standard OTel HTTP
instrumentation. Requests must carry the owning test's context. A per-test bound transport provides that association when changing request
construction is impractical. Parallel tests must not depend on globally changing
the active test or mutating a shared client's transport.

`httptest.NewServer` can be used as the real SUT destination behind Phrond.
Simultaneous tests with different SUT server addresses need separate Phrond
runtimes; this design supports one reverse SUT target per runtime.
`httptest.NewRecorder` with a direct handler call has no inbound network exchange
for Phrond to intercept; capturing that leg requires adapting the test harness
to use an HTTP server and route its calls through Phrond.

## Choosing a supported setup

| Existing setup | Experience / required action |
| --- | --- |
| SUT propagates W3C context through server and dependency clients | Parallel tests can share a SUT when each attempt has its own context and Phrond resolves dependency traffic to that trace's session. Application state and mock isolation remain the test suite's responsibility. |
| SUT has OTel installed but does not propagate on the relevant HTTP path | Treat it as no propagation. Library presence alone does not establish correlation. |
| Shared SUT without propagation | Run tests sequentially, with one SUT request at a time and completed dependency activity before moving on. Timing supplies inferred request relationships. |
| Separate SUT and Phrond runtime for each parallel worker | Keep workers parallel and run tests and SUT requests sequentially within each worker. Each worker's outbound path must remain isolated too. |
| Several overlapping requests within one test trace | Group propagated calls under the test; an exact inbound parent may remain ambiguous without additional span ancestry. |
| Existing network mock server | Keep its behavior and configuration; route SUT calls through Phrond to it. No mock instrumentation is required. |
| In-process or client-intercepting mock | Traffic short-circuited before reaching the proxy is invisible. Adapt the harness to use a reachable HTTP mock if those exchanges must be captured. |

Test instrumentation cannot make an uninstrumented SUT propagate context. Teams
can adopt Phrond without adding that instrumentation to their application by
choosing sequential execution or isolated workers. These arrangements establish
test ownership; timing still does not prove causality for arbitrary background
work or overlapping requests.

## Evidence and completion

Attempts retain `testcase.trace_id`, `testcase.root_span_id`, sender
`request_span_id`, `trace_context_source`, `test_correlation`, and
`parent_correlation`. A trace match identifies the test; `trace_unique` and
`timing` parent relationships are inferences. `ambiguous`/`unmatched` flows omit
`parent_flow_id`. The bundle's test pages show these distinctions and preserve
retry IDs; the bundled HTML dashboard presents the same evidence.
Executable Workflow requests omit captured trace/session headers; captured
evidence retains the received W3C context.

Await all expected activity before finishing a test. Teardown stops new
admission and waits up to five seconds for accepted requests; `drainTimeoutMs`
(JS) or `drain_timeout_ms` (API) changes that bound, from 0 to 60000 milliseconds.
A timeout writes a failed attempt with `capture_incomplete` and diagnostics;
it does not silently claim a complete capture. Closed trace IDs cannot be reused
until a successful runtime shutdown (`finishRun`). If a
timed-out attempt still has accepted requests in flight, Phrond rejects new
implicit sessions and unlabeled fallback traffic until they finish. Untraced
late/background work cannot be
reliably assigned, even in serial mode; drain or isolate that work in the harness.

## Background

- [OpenTelemetry context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/)
- [JavaScript instrumentation](https://opentelemetry.io/docs/languages/js/libraries/)
- [Go HTTP transport instrumentation](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp#NewTransport)
