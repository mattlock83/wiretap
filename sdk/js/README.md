# Phrond JS SDK

This package provides a thin JavaScript client for the Phrond recorder core.

## Usage

```js
const { createHarness } = require("@phrond/sdk");

const harness = createHarness({ baseUrl: "http://127.0.0.1:18000" });
await harness.startLifecycle();
```

## API
- `createHarness({ baseUrl, timeoutMs })`
- `harness.healthz()`
- `harness.startLifecycle()`
- `harness.stopLifecycle()`
- `harness.registerStub(stub)`
- `harness.clearStubs()`
- `harness.assertField(assertion)`
- `harness.setSchema(schema)`
