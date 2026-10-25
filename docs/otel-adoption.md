# Tracing and concurrency

Wiretap uses standard W3C trace context to associate captured HTTP and native
gRPC exchanges with individual test attempts. Start with the
[existing-tests guide](getting-started-existing-tests.md), then choose a runner integration below. No collector or exporter is required.

## What teams keep

Keep the existing application, HTTP client, assertions, and mock server. Wiretap
adds test lifecycle integration and captures traffic routed through its
listeners. Existing mock tools still own stubs, responses, verification, and
lifecycle.

No application or mock source change should be required specifically for Wiretap.
Some test harness and endpoint configuration is required; this is not a promise
that every test suite can run with completely unchanged test code.

Wiretap uses standard W3C `traceparent` and `tracestate` propagation.
Wiretap's internal session and flow IDs remain, along with explicit test-to-Wiretap
session selection. The SUT never needs to propagate those internal IDs.

## A test is the root of a trace

Each test attempt, including a retry or repeated execution, gets a fresh root
span and trace ID. The adapter associates that trace with a capture session and
records the test name and outcome. A span represents an operation; a trace groups
related spans. The test's HTTP client sends requests within the test's context.

```text
Test attempt: creates an order                 one trace / capture session
  Test → Wiretap → SUT: POST /orders             request + response
  SUT → Wiretap → existing inventory mock       request + response
  Test → Wiretap → SUT: GET /orders/123          request + response
```

When the SUT already propagates standard context from its inbound server to its
outbound HTTP clients, its dependency requests retain the test's trace ID.
Wiretap can associate those exchanges with the test, including when tests overlap.
The mock only needs to receive normal HTTP requests; it does not need OTel.

Wiretap still records request and response payloads at the proxy. OTel supplies
context for correlation, not full body capture. No collector, exporter, or
tracing backend is required. Capture works when sampling is disabled. Wiretap
records proxy exchanges and context; use your existing tracing backend to
inspect application spans.

One test trace can contain several inbound SUT requests. Knowing the test does
not always prove which inbound request caused a dependency call: the SUT's
internal span ancestry may be invisible to the proxy. Wiretap shows test
ownership established by trace IDs separately from unique-candidate or
timing-based parent inferences and ambiguity.

## What users need to do

1. **Connect test setup and teardown.** Use the runner adapter or helper
   to start each attempt's trace and capture session, then record pass/fail.
2. **Route both sides through Wiretap.** Point test requests at the SUT capture
   endpoint and configure the SUT's dependency endpoints to the mock capture
   routes. Keep the actual SUT and mock addresses as forwarding destinations.
3. **Configure client instrumentation.** Retain the existing client while adding
   the instrumentation appropriate to its HTTP stack, as described below.
4. **Choose a concurrency setup.** Verify actual inbound-to-outbound propagation
   before sharing one SUT across parallel tests; otherwise isolate or serialize.
5. **Await the test's work before teardown.** Wait for HTTP calls and any expected
   background dependency activity. Closing the test early cannot establish the
   ownership of later work reliably.
6. **Configure schemas.** The main HTTP API requires its OpenAPI spec by default.
   HTTP dependency specs are opt-in; without one, capture still works and its
   contract-coverage denominator is unknown. Supplied specs always validate.
   gRPC always needs a descriptor, using reflection when no file is supplied. See
   [configuration](configuration.md).

Managed Go, Jest, Mocha, Playwright and pytest integrations start Wiretap, finalize
capture/exports after user cleanup, and stop their owned process. They choose
available local ports and expose the capture URLs to the test setup. Keep the
existing SUT and mock lifecycle, or configure supported HTTP handler hosting.
The runner guides linked below describe those choices.

For a project-owned process, the JavaScript, Go and Python `Runtime` APIs connect
to the running control service. Set `WIRETAP_BASE_URL` or the SDK's explicit
address. One suite owner finalizes after all workers finish; individual workers
must not finalize each other's shared run.

Serial capture remains the default, with one implicit session. Shared-SUT
parallel mode uses explicit sessions and stable dependency capture URLs,
configured once in the SUT. Wiretap selects the owning session from each
request's trace. Enable that mode only when propagation works on the relevant
application path.

### Plain JavaScript and `node:test`

Use the [plain JavaScript guide](../sdk/js/docs/javascript.md) when your harness
owns Wiretap. Install the SDK dependencies (`npm ci --prefix sdk/js` in this
repository). For CommonJS tests, load instrumentation before HTTP client modules:

```bash
node --require @wiretap/sdk/tracing --test test/*.test.js
```

```js
const { createNodeTestSuite } = require("@wiretap/sdk");
const assert = require("node:assert/strict");
const wiretap = createNodeTestSuite({
  baseUrl: process.env.WIRETAP_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [ordersOpenAPI],
  dependencies: { inventory: { upstreamUrl: process.env.MOCK_URL, schema: inventoryOpenAPI } },
  explicitSession: true, // only when the SUT propagates W3C context
  manageRuntime: false  // one outer owner finalizes after all workers
});
wiretap.test("creates an order", async () => {
  const response = await fetch(`${wiretap.sutUrl}/orders`, {
    method: "POST", headers: { "content-type": "application/json" },
    body: JSON.stringify({ productId: "coffee", quantity: 1 })
  });
  assert.equal(response.status, 201);
});
```

The SDK instruments Node `http`/`https` and native `fetch` through
standard HTTP and Undici instrumentation. Keep existing calls and assertions.
Other clients work when they use an instrumented stack; custom transports,
ESM module hooks and in-process mock interception need separate verification.
The SDK requires **Node 22+**; the `node:test` adapter uses the runner's
[final outcome API](https://nodejs.org/api/test.html#contextpassed).
HTTP and native fetch are covered by SDK integration tests; HTTPS capture is
also covered by the proxy's decrypted HTTPS tests.

For an existing OTel setup, initialize it first and pass `instrumentation: false`
to `startCapture`/`createNodeTestSuite`; omit the Wiretap preload to avoid duplicate
instrumentation. Wiretap reuses an existing global provider/context manager. You
can also pass `tracerProvider`. A supplied provider must create valid contexts;
an explicitly supplied no-op provider fails setup. Sampling can be disabled.

`runtime.runTest(name, async session => { ... }, options)` is the lower-level
wrapper for other promise-based runners. It creates an isolated async context,
registers a fresh root and finalizes capture even when the callback throws.
This generic wrapper sees callback errors only. To integrate other framework
hooks, use `runtime.createTestAttempt(name, options)`, run each captured callback
with `attempt.run`, and call `attempt.finish({ status, error })` from the
framework's completion hook, including timeouts. Keep a separate handle for
every attempt/retry. Runner adapters implement their own outcome integration.
With `node:test`, `t.after` cleanup and nested `t.test` failures contribute to
the owning attempt. The original test tree and hook registration order are preserved.
`setupTest`/`teardownTest` remain available for manual serial fixtures. Manual
setup does not activate a global test span; use `runTest` for ordinary client
instrumentation, or explicitly activate its span with standard OTel context APIs.

### Managed Jest capture

Wrap an existing Node/Jest Circus configuration with `withWiretap`. The adapter
installs the Wiretap Node environment so Jest's VM and native HTTP stack share
the attempt's OTel context:

```js
const { withWiretap } = require("@wiretap/sdk/jest");
module.exports = withWiretap(existingConfig, {
  app: "<rootDir>/app.js"
});
```

Point the shared API client at `process.env.WIRETAP_SUT_URL`. Omit `app` when the
harness already hosts the API, and configure its target in `.wiretap.yaml`.
Existing tests, mocks and fixture hooks keep their runner-owned outcomes.
Automatic capture supports sync, promise, `done` and generator callbacks, retries,
assertion-count checks and per-test hook failures. `beforeAll`/`afterAll` remain
suite work without an individual test owner.

The default is one worker. Parallel workers require `explicitSession: true` and
propagation through captured dependencies. Concurrent tests use explicit
sessions too; Jest does not run ordinary per-test hooks for `.concurrent`, so
keep those fixtures in the callback. Existing host OTel instrumentation can be
preloaded with `NODE_OPTIONS`; set `WIRETAP_EXISTING_OTEL=1` to reuse it without
duplicate instrumentation.

The managed global teardown finalizes after all workers and per-test cleanup,
then calls the project's existing global teardown. Workers do not own the shared
Wiretap process. `createJestSuite` remains available
for selective capture with a project-owned lifecycle. See the
[Jest guide](../sdk/js/docs/jest.md) for supported versions, configuration,
dynamic fixtures and selective capture.

### Managed Playwright and Mocha capture

The [Playwright adapter](../sdk/js/docs/playwright.md) wraps the configuration and
extends the standard test fixture. It supplies trace headers to request/browser
fixtures directly, without depending on Node HTTP instrumentation. Keep relative
API URLs so traffic reaches the capture proxy. Custom contexts need their own
Wiretap URL and trace headers. One worker is the default; parallel capture needs
explicit sessions and propagated context for captured downstream calls.

The [Mocha adapter](../sdk/js/docs/mocha.md) wraps the existing configuration or
loads as a root-hook plugin. It activates capture for per-test setup, the test
callback and cleanup, then finalizes the owned runtime after suite cleanup.
Mocha capture supports serial runs. Both adapters retain the runner's final
outcome, including failed fixtures and retries, independently of API validation.

### Python and pytest

Use the [Python SDK and pytest plugin](../sdk/python/README.md) with the existing
HTTP or gRPC client. The `wiretap` fixture captures the attempt through fixture
cleanup and exposes `url`, `dependencies` and `headers`. Pass those headers to
HTTP clients or as gRPC metadata; Python does not install client instrumentation
or an OTel provider. The `trace_headers()` helper reads the current attempt's
headers inside its context.

Each pytest process owns an independent runtime. With pytest-xdist, give each
worker its own application and mock routing when those depend on its capture
addresses. Application outbound calls still need W3C propagation to correlate
with the same test; passing headers on the test's inbound call alone cannot
instrument the application.

### Go tests and `httptest`

Use `wiretap.Run(m)` in the existing `TestMain` and `wiretap.StartTest(t)` in a
test or shared helper. Each package owns one Wiretap process and finalizes after
its test cleanup. `RunOptions.Handler` can host an existing HTTP handler on an
available port; otherwise keep the existing application server. See the
[Go quickstart](../sdk/go/README.md) and
[advanced integration](../sdk/go/docs/advanced.md).

Keep the standard `http.Client` and wrap its transport with standard OTel HTTP
instrumentation. Requests must carry the owning test's context. A transport
bound to the test provides that association when changing request construction
is impractical. Parallel tests must not depend on globally changing
the active test or mutating a shared client's transport.

`httptest.NewServer` can be used as the real SUT destination behind Wiretap.
Simultaneous tests with different SUT server addresses need separate Wiretap
runtimes; this design supports one reverse SUT target per runtime.
`httptest.NewRecorder` with a direct handler call has no inbound network exchange
for Wiretap to intercept; capturing that leg requires adapting the test harness
to use an HTTP server and route its calls through Wiretap.

## Choosing a supported setup

| Existing setup | Experience / required action |
| --- | --- |
| SUT propagates W3C context through server and dependency clients | Parallel tests can share a SUT when each attempt has its own context and Wiretap resolves dependency traffic to that trace's session. Application state and mock isolation remain the test suite's responsibility. |
| SUT has OTel installed but does not propagate on the relevant HTTP path | Treat it as no propagation. Library presence alone does not establish correlation. |
| Shared SUT without propagation | Run tests sequentially, with one SUT request at a time and completed dependency activity before moving on. Timing supplies inferred request relationships. |
| Separate SUT and Wiretap runtime for each parallel worker | Keep workers parallel and run tests and SUT requests sequentially within each worker. Each worker's outbound path must remain isolated too. |
| Several overlapping requests within one test trace | Group propagated calls under the test; an exact inbound parent may remain ambiguous without additional span ancestry. |
| Existing network mock server | Keep its behavior and configuration; route SUT calls through Wiretap to it. No mock instrumentation is required. |
| In-process or client-intercepting mock | Traffic short-circuited before reaching the proxy is invisible. Adapt the harness to use a reachable HTTP mock if those exchanges must be captured. |

Test instrumentation cannot make an uninstrumented SUT propagate context. Teams
can adopt Wiretap without adding that instrumentation to their application by
choosing sequential execution or isolated workers. These arrangements establish
test ownership; timing still does not prove causality for arbitrary background
work or overlapping requests.

## Evidence and completion

Attempts retain `testcase.trace_id`, `testcase.root_span_id`, sender
`request_span_id`, `trace_context_source`, `test_correlation`, and
`parent_correlation`. A trace match identifies the test; `trace_unique` and
`timing` parent relationships are inferences. `ambiguous`/`unmatched` flows omit
`parent_flow_id`. The raw test recordings preserve these distinctions and each
attempt's identity. Captured evidence retains the received W3C context.
Framework outcomes and contract-validation findings remain separate. Capture
incompleteness or lifecycle failures can still fail SDK cleanup.

Await all expected activity before finishing a test. Teardown stops new
admission and waits up to five seconds for accepted requests; `drainTimeoutMs`
(JS) or `drain_timeout_ms` (API) changes that bound, from 0 to 60000 milliseconds.
A timeout writes a failed attempt with `capture_incomplete` and diagnostics;
it does not silently claim a complete capture. Closed trace IDs cannot be reused
until successful run finalization (`finishRun`). The control API leaves the
process running; a managed SDK owner stops its process after finalization. If a timed-out attempt still has accepted requests in flight,
Wiretap rejects new implicit sessions and unlabeled fallback traffic until they
finish. Untraced late/background work cannot be reliably assigned, even in serial
mode; drain or isolate that work in the harness.

## Background

- [OpenTelemetry context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/)
- [JavaScript instrumentation](https://opentelemetry.io/docs/languages/js/libraries/)
- [Go HTTP transport instrumentation](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp#NewTransport)
