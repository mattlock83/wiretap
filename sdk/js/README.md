# JavaScript SDK

The SDK makes Phrond a small capture fixture. Your existing test harness owns
the SUT and mock-server lifecycles and configuration; Phrond proxies inbound
test calls and optional outbound dependency calls, validates both against their
OpenAPI schemas, and writes correlated reports.

For a step-by-step migration that leaves an existing mock server unchanged,
see [Getting started with existing tests and a mock server](../../docs/getting-started-existing-tests.md).

Install dependencies with `npm ci` from this directory. Preload instrumentation
with `node --require @phrond/sdk/tracing --test test/*.test.js`. See
[tracing and Jest setup](../../docs/otel-adoption.md) for parallel tests and
existing OpenTelemetry providers.

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@phrond/sdk");

const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [{
    id: "hello-api",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "schemas/hello.openapi.yaml" }
  }]
});

phrond.test("hello world happy flow", async () => {
  const response = await phrond.http.get("/hello");
  assert.equal(response.status, 200);
});
```

`phrond.test` creates and finalizes the capture session automatically. The
literal test name is stored in `artifact.testcase.name`, and failures are
recorded with `status: fail` before being rethrown.

To capture calls from the SUT to an existing WireMock, MockServer, or dependency,
declare its real upstream URL and OpenAPI schema:

```js
const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [ordersOpenAPI],
  dependencies: {
    inventory: {
      upstreamUrl: process.env.WIREMOCK_BASE_URL,
      schema: inventoryOpenAPI
    }
  }
});
```

External orchestration configures the SUT's inventory base URL to
`http://<phrond-proxy>/__phrond/dep/inventory`. After suite startup,
`phrond.dependencyUrl("inventory")` returns this address; explicit parallel
sessions with registered traces use the same stable address and are selected by
the propagated trace. Untraced explicit API sessions receive session-scoped
addresses. Phrond forwards requests
and responses but never creates stubs, verifies mock calls, owns the mock
server, or changes its responses. Every dependency requires its own valid
OpenAPI schema.

Tests can assert the dependency response fields they actually consume without
asking Phrond to manage or verify the external mock server:

```js
await phrond.assertDependency("inventory", {
  method: "GET",
  path: "/inventory/product-123",
  providerState: {
    name: "product exists and is available",
    params: { productId: "product-123" }
  },
  response: {
    "$.productId": "product-123",
    "$.available": true
  }
});
```

The latest matching captured dependency exchange is checked immediately. Failed
assertions fail the owning test; passing field assertions are linked to their
captured flow while the full request and response remain in the artifact.
Nested fields and array indexes such as `$.items[0].sku` are supported.
`providerState` accepts a state name or `{ name, params }`; use
`providerStates` for multiple Pact provider prerequisites.

Generate standard Pact V3 contracts from the captured downstream exchanges:

```js
const result = await phrond.report({
  consumerName: "orders-service",
  providerNames: { inventory: "inventory-service" },
  pactDir: "artifacts/pacts"
});

console.log(result.pact_paths);
console.log(result.consumer_version, result.branch);
```

Set `consumerName` once on `createNodeTestSuite` to reuse it across reports.
When dependency traffic exists, reports generate `artifacts/pacts` by default.
The exporter writes one contract per provider, excludes failing tests,
deduplicates equivalent interactions, and includes only explicitly asserted
response fields; without assertions, the full response and OpenAPI-derived
type/format matchers are used. The existing Postman, workflow, and HTML reports
are still generated. See [Pact export](../../docs/pact-export.md).

Schemas are required by default. Teams without one can explicitly generate a
standard OpenAPI 3.1 draft from all captures in the suite:

```js
const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  generateSchema: {
    output: "artifacts/hello.generated.openapi.yaml",
    title: "Hello API",
    version: "1.0.0"
  }
});
```

The SDK writes the draft automatically before shutting down Phrond. Review it,
move it into your schemas directory, then replace `generateSchema` with
`schemas`. Supplying both is rejected, and generated files are not overwritten
unless `force: true` is set.

Lower-level API:

- `startCapture({ baseUrl, timeoutMs, proxy, schemas, generateSchema, sutBaseUrl, dependencies })`
- `runtime.setupTest({ name, tags, meta, schemas, generateSchema, sutBaseUrl, dependencies, explicitSession })`
- `runtime.dependencyUrl(assetId)`
- `runtime.assertDependency(assetId, { method, path, flowId, providerState, providerStates, response })`
- `runtime.http.get/post/postJson/request(...)`
- `runtime.runTest(name, callback, { explicitSession, onSession, drainTimeoutMs })`
- `runtime.teardownTest({ status, drainTimeoutMs })`
- `runtime.generateSchema({ output, title, version, force })`
- `runtime.report({ artifactsDir, sutName, consumerName, providerNames, collectionPath, workflowPath, htmlPath, pactDir })`
- `runtime.killServer()`
- `bindTest(runtimeOrGetter, testFn, defaults)`

`startServer` remains an alias for `startCapture`. Phrond never launches,
waits for, configures, or stops the SUT.

The `node:test` adapter requires Node 20.12+ and includes callback, subtest,
`t.after`, and timeout outcomes in capture cleanup. Keep ordinary tests and
hooks; no extra nested test is introduced. Other promise-based integrations
can use `runTest`, which observes only the callback outcome.

## Test traces and parallel execution

Each wrapped attempt creates a fresh OTel root span and capture session. Native
`fetch` and Node HTTP/HTTPS clients use standard instrumentation; no special
Phrond HTTP client is required. The default is serial capture. Set
`explicitSession: true` only when the SUT propagates W3C context or each explicit
session has a separately isolated dependency route.

`manageRuntime: false` keeps suite shutdown from stopping a shared runtime or
running suite-wide schema generation. Use one owner to generate reports/schemas
and stop Phrond after all suites/workers complete. Shutdown rejects active
sessions. Teardown drains accepted exchanges (default 5000ms, allowed 0–60000ms)
and fails on incomplete capture or schema errors. The original callback error
wins over a secondary cleanup error.

Use `@phrond/sdk/jest` with `@phrond/sdk/jest-environment` for Jest 30.
The adapter wraps async test callbacks and supports `.concurrent`, `.each`,
`.only`, `.skip`, and retries. Runner timeouts and hook/assertion failures
also finalize capture. Ordinary tests default to serial sessions; use
`--runInBand` for an uninstrumented shared SUT. Concurrent tests use explicit
sessions; set `explicitSession: true` for multiple workers sharing a traced SUT.
It always shares an externally managed runtime.
See the [adoption guide](../../docs/otel-adoption.md) for full examples, hook
boundaries, and the instrumented-client support matrix.
