# JavaScript SDK

The SDK makes Phrond a small capture fixture. Your existing test harness owns
the SUT and mock-server lifecycles and configuration; Phrond proxies inbound
test calls and optional outbound dependency calls, validates both against their
OpenAPI schemas, and writes correlated reports.

For a step-by-step migration that leaves an existing mock server unchanged,
see [Getting started with existing tests and a mock server](../../docs/getting-started-existing-tests.md).

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
sessions receive isolated session-scoped addresses. Phrond forwards requests
and responses but never creates stubs, verifies mock calls, owns the mock
server, or changes its responses. Every dependency requires its own valid
OpenAPI schema.

Tests can assert the dependency response fields they actually consume without
asking Phrond to manage or verify the external mock server:

```js
await phrond.assertDependency("inventory", {
  method: "GET",
  path: "/inventory/product-123",
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
- `runtime.assertDependency(assetId, { method, path, flowId, response })`
- `runtime.http.get/post/postJson/request(...)`
- `runtime.teardownTest({ status })`
- `runtime.generateSchema({ output, title, version, force })`
- `runtime.report({ artifactsDir, sutName, collectionPath, workflowPath, htmlPath })`
- `runtime.killServer()`
- `bindTest(runtimeOrGetter, testFn, defaults)`

`startServer` remains an alias for `startCapture`. Phrond never launches,
waits for, configures, or stops the SUT.
