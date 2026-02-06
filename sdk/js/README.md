# Phrond JS SDK

This package provides a thin JavaScript client for the Phrond recorder core.

## Recommended Usage (Phased API)

```js
const { setupSuite } = require("@phrond/sdk");

// 1) Suite setup
const suite = await setupSuite({
  baseUrl: "http://127.0.0.1:18000",
  proxy: true,
  mockServer: true
});

// 2) Test setup
const test = await suite.setupTest({
  name: "basic",
  meta: { suite: "examples" },
  sutBaseUrl: "http://127.0.0.1:18090",
  schemas: [
    {
      id: "sut-openapi",
      role: "sut",
      type: "openapi",
      source: { type: "file", path: "specs/sut-openapi.yaml" }
    },
    {
      id: "mock-openapi",
      role: "fake",
      type: "openapi",
      source: { type: "file", path: "specs/mock-openapi.yaml" }
    }
  ]
});

// 3) Mocking
await test.mockHttp({
  id: "alpha",
  match: { method: "POST", path: "/alpha" },
  response: { status: 200, body: "{\"result\":\"ok\"}" }
});

// Send traffic to the SUT through the suite proxy.
const response = await test.postJson("/process", { id: "123", action: "process" });
console.log(response.status, response.body);

// 4) Test teardown
await test.teardown({
  status: "pass",
  downloadArtifactTo: "artifacts/basic.yaml"
});

// 5) Suite teardown
await suite.teardown();
```

## High-level API
- `setupSuite({ baseUrl, timeoutMs, proxy, mockServer, grpcProxy, grpcMockServer })`
- `harness.setupSuite({ proxy, mockServer, grpcProxy, grpcMockServer })`

Component startup options can be:
- `true` to start with default config
- `false` or omitted to skip startup
- object to pass startup config

If a component fails to start during suite setup, lifecycle is automatically stopped.

Returned suite session:
- `suite.runtime` (startup responses keyed by component name)
- `suite.setupTest({ name, meta, tags, sutBaseUrl, schemas })` (schemas required)
- `suite.teardown()`

Returned test session:
- `test.mockHttp(stub)`
- `test.mockGrpc(stub)`
- `test.clearMocks({ http, grpc })` (defaults to clearing both)
- `test.expectCall(expectation)`
- `test.assertField(assertion)`
- `test.request({ method, path, headers, body, sutBaseUrl })`
- `test.get(path, { headers, sutBaseUrl })`
- `test.post(path, body, { headers, sutBaseUrl })`
- `test.postJson(path, payload, { headers, sutBaseUrl })`
- `test.teardown({ status, downloadArtifactTo })` (`status` defaults to `"pass"`)

For request helpers, start the suite with `proxy: true` and pass `sutBaseUrl` in `setupTest` (or per-request).

## Low-level API
- `createHarness({ baseUrl, timeoutMs })`
- `harness.healthz()`
- `harness.startLifecycle()`
- `harness.stopLifecycle()`
- `harness.startTestCase(name, meta, { tags, sutBaseUrl, schemas })` (schemas required)
- `harness.finishTestCase(status)` (response includes `schemaValidation` summary)
- `harness.getArtifact()`
- `harness.downloadArtifact(filePath)`
- `harness.waitForStatus({ intervalMs, timeoutMs })`
- `harness.startProxy(config)`
- `harness.startMockServer(config)`
- `harness.startGrpcProxy(config)`
- `harness.startGrpcMockServer(config)`
- `harness.registerStub(stub)`
- `harness.clearStubs()`
- `harness.registerGrpcStub(stub)`
- `harness.clearGrpcStubs()`
- `harness.assertField(assertion)`
- `harness.expectCall(expectation)`
- `harness.setSchema(schema)`
- `harness.addRedaction(rule)`
- `harness.setProxyMode(mode)`
- `harness.getCaCertificate()`
