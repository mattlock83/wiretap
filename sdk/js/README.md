# Phrond JS SDK

This package provides a thin JavaScript client for the Phrond recorder core.

## Usage

```js
const { createHarness } = require("@phrond/sdk");

const harness = createHarness({ baseUrl: "http://127.0.0.1:18000" });
await harness.startLifecycle();

await harness.startTestCase("basic", { suite: "examples" }, {
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
```

## API
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
