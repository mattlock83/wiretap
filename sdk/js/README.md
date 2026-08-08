# JavaScript SDK

The SDK makes Phrond a small capture fixture. Your existing test harness owns
the SUT lifecycle and configuration; Phrond only proxies test calls, validates
them against optional OpenAPI schemas, and writes reports.

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

Lower-level API:

- `startCapture({ baseUrl, timeoutMs, proxy, schemas, sutBaseUrl, strictSchema })`
- `runtime.setupTest({ name, tags, meta, schemas, sutBaseUrl, explicitSession })`
- `runtime.http.get/post/postJson/request(...)`
- `runtime.teardownTest({ status })`
- `runtime.report({ artifactsDir, sutName, collectionPath, workflowPath, htmlPath })`
- `runtime.killServer()`
- `bindTest(runtimeOrGetter, testFn, defaults)`

`startServer` remains an alias for `startCapture`. Phrond never launches,
waits for, configures, or stops the SUT.
