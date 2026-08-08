# JavaScript SDK

The SDK makes Phrond a small capture fixture. Your existing test harness owns
the SUT lifecycle and configuration; Phrond only proxies test calls, validates
them strictly against supplied OpenAPI schemas, and writes reports.

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

- `startCapture({ baseUrl, timeoutMs, proxy, schemas, generateSchema, sutBaseUrl })`
- `runtime.setupTest({ name, tags, meta, schemas, generateSchema, sutBaseUrl, explicitSession })`
- `runtime.http.get/post/postJson/request(...)`
- `runtime.teardownTest({ status })`
- `runtime.generateSchema({ output, title, version, force })`
- `runtime.report({ artifactsDir, sutName, collectionPath, workflowPath, htmlPath })`
- `runtime.killServer()`
- `bindTest(runtimeOrGetter, testFn, defaults)`

`startServer` remains an alias for `startCapture`. Phrond never launches,
waits for, configures, or stops the SUT.
