# JS SDK

The JS SDK is session-based and stack-based.

## Quick start

```js
const { startServer } = require("@phrond/sdk");

let runtime;

before(async () => {
  runtime = await startServer({
    baseUrl: "http://127.0.0.1:18000",
    proxy: true,
    mockServer: true,
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "file", path: "specs/sut-openapi.yaml" }
      },
      {
        id: "fake-openapi",
        role: "fake",
        type: "openapi",
        source: { type: "file", path: "specs/fake-openapi.yaml" }
      }
    ],
    sutBaseUrl: "http://127.0.0.1:18090"
  });
});

beforeEach(async (t) => {
  const session = await runtime.setupTest({ name: t.name });
  // session.mockBaseUrl is the dependency URL to inject into your SUT process.
});

afterEach(async () => {
  await runtime.teardownTest();
});

after(async () => {
  await runtime.killServer();
});

test("submit payment", async () => {
  await runtime.queueResponse({
    id: "account-lookup",
    match: { method: "GET", path: "/accounts/acct-100" },
    response: { status: 200, body: "{\"id\":\"acct-100\"}" }
  });

  await runtime.http.postJson("/payments/submit", {
    from_account: "acct-100",
    to_account: "acct-200",
    amount: 120,
    currency: "USD"
  });

  const outbound = await runtime.getResponse("account-lookup");
  console.log(outbound.exchange.Request.Method, outbound.exchange.Request.Path);
});
```

## API

- `startServer({ baseUrl, timeoutMs, proxy, mockServer, stackSize, recordResponses, schemas, sutBaseUrl, strictSchema })`
- `runtime.setupTest({ name, tags, meta, schemas, sutSchemas, fakeSchemas, sutBaseUrl })`
- `runtime.queueResponse({ id, match, response })`
- `runtime.listQueued()`
- `runtime.listResponded()`
- `runtime.getResponse(id)`
- `runtime.teardownTest({ status })`
- `runtime.report({ artifactsDir, sutName, includeArtifacts, yamlPath, htmlPath })`
- `runtime.killServer()`

## Convenience methods

- `runtime.scenarioName(name)` sets the next test name (optional if `setupTest({ name })` is used).
- `runtime.stop({ status })` tears down active session (if any) and kills runtime.
- `runtime.http.get/post/postJson/request(...)` sends test traffic through the proxy and tags it with the active session id.

## Related Docs

- Runtime HTTP endpoints: `docs/runtime-api.md`
- Response template format (`body` / `body_file`): `docs/mock-template-format.md`
