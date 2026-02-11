# Phrond JS SDK

The SDK now exposes a single high-level entry point with scenario lifecycle built in.

## Quick start

```js
const { start } = require("@phrond/sdk");

let runtime;

beforeEach(async () => {
  runtime = await start({
    baseUrl: "http://127.0.0.1:18000",
    sutBaseUrl: "http://127.0.0.1:18090",
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
    ]
  });
});

afterEach(async () => {
  await runtime.stop();
});

test("submit payment", async () => {
  await runtime.scenarioName("submit-payment");

  await runtime.mock.http({
    id: "account-1",
    match: { method: "GET", path: "/accounts/acct-100" },
    response: { status: 200, body: "{\"id\":\"acct-100\"}" }
  });

  const submit = await runtime.http.postJson("/payments/submit", {
    from_account: "acct-100",
    to_account: "acct-200",
    amount: 120,
    currency: "USD"
  });

  const outbound = await runtime.expectCall({ method: "GET", path: "/accounts/acct-100" });
  console.log(submit.status, outbound.path);
});
```

## API

- `start({ baseUrl, timeoutMs, sutBaseUrl, schemas, proxy, mockServer, tags, meta, strictSchema, scenarioPrefix })`
- `runtime.scenarioName(name)`
- `runtime.reset({ status, strictSchema, clearMocks, nextScenarioName })`
- `runtime.stop({ status, strictSchema, writeArtifact })`
- `runtime.mock.http(stub)`
- `runtime.mock.clear({ http })`
- `runtime.http.request({ method, path, headers, body, sutBaseUrl, proxyBaseUrl })`
- `runtime.http.get(path, options)`
- `runtime.http.post(path, body, options)`
- `runtime.http.postJson(path, payload, options)`
- `runtime.expectCall(pathOrExpectation)`

## Notes

- `schemas` are required and attached to each scenario artifact.
- If `scenarioName(...)` is not called, a unique scenario ID is generated automatically.
- By default, `stop()` and `reset()` throw when schema validation fails.
- `expectCall(...)` returns the matching outbound SUT request:
  - `{ method, path, url, headers, query, body, matchedStubId, startedAt, finishedAt }`
- You can pass explicit listen addresses in startup config objects, for example:
  - `proxy: { listen_addr: "127.0.0.1:19000" }`
  - `mockServer: { listen_addr: "127.0.0.1:19001" }`
