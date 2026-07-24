# JS SDK

The JS SDK is session-based and stack-based. Ergonomics are designed so teams
change nothing about how they write tests: declare dependencies once, configure
the SUT once with stable URLs, and use any HTTP client.

## Quick start

```js
const { test, before, after } = require("node:test");
const { startServer, bindTest } = require("@phrond/sdk");

let runtime;
const phrondTest = bindTest(() => runtime, test);

before(async () => {
  runtime = await startServer({
    baseUrl: "http://127.0.0.1:18000",
    proxy: true,
    mockServer: true,
    sutBaseUrl: "http://127.0.0.1:18090",
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "file", path: "specs/sut-openapi.yaml" }
      }
    ],
    dependencies: [
      {
        assetId: "account-service",
        assetName: "Account Service",
        schema: {
          id: "account-openapi",
          type: "openapi",
          source: { type: "file", path: "specs/account-openapi.yaml" }
        }
      }
    ]
  });

  // Stable across all tests: configure the SUT once with this URL.
  const accountUrl = runtime.dependencyUrl("account-service");
  // ... start your SUT with ACCOUNT_BASE_URL=accountUrl
});

after(async () => {
  await runtime.killServer();
});

phrondTest("submit payment", async () => {
  await runtime.queueResponse({
    id: "account-lookup",
    match: { method: "GET", path: "/accounts/acct-100" },
    response: { status: 200, body: JSON.stringify({ id: "acct-100", balance: 1000 }) }
  });

  // runtime.http.* calls the capture proxy in reverse mode — or use any HTTP
  // client pointed at runtime.sutUrl.
  await runtime.http.postJson("/payments/submit", {
    from_account: "acct-100",
    to_account: "acct-200",
    amount: 120,
    currency: "USD"
  });

  const outbound = await runtime.getResponse("account-lookup");
  // Declare which mocked fields the SUT depends on.
  await runtime.verify("account-lookup", ["body.balance"]);
});
```

## Key concepts

- **Dependencies**: declare each downstream service with a stable `assetId`
  (the cross-repo key a broker later uses to join consumer assumptions to the
  provider). Each dependency gets its own stable mock base URL via
  `runtime.dependencyUrl(assetId)` — inject it into the SUT once at startup.
- **Reverse capture**: with `sutBaseUrl` set, tests call `runtime.sutUrl` (the
  proxy address) with any HTTP client; requests are captured and forwarded. No
  proxy configuration or CA trust needed for HTTP.
- **Zero-config correlation**: session URLs are stable — no per-test rewiring.
  Run tests serially (one session at a time), or pass
  `setupTest({ explicitSession: true })` for parallel suites.
- **`bindTest(runtimeOrGetter, test)`** wraps a test function so each test gets
  an auto-named session and a truthful pass/fail artifact status.
- **Schemas are optional**: without any, artifact validation is `skipped`, not
  failed. Attach OpenAPI specs when you have them for contract validation.

## API

- `startServer({ baseUrl, timeoutMs, proxy, mockServer, stackSize, recordResponses, schemas, dependencies, sutBaseUrl, strictSchema })`
- `runtime.setupTest({ name, tags, meta, schemas, sutSchemas, fakeSchemas, dependencies, sutBaseUrl, recordTraffic, explicitSession })`
- `runtime.dependencyUrl(assetId)`
- `runtime.queueResponse({ id, assetId, default, match, response })`
- `runtime.verify(id, fields)`
- `runtime.listQueued()`
- `runtime.listResponded()`
- `runtime.getResponse(id)`
- `runtime.teardownTest({ status })`
- `runtime.report({ artifactsDir, sutName, includeArtifacts, collectionPath, workflowPath, htmlPath })`
- `runtime.killServer()`
- `bindTest(runtimeOrGetter, testFn, defaults)`

## Convenience methods

- `runtime.scenarioName(name)` sets the next test name (optional if `setupTest({ name })` is used).
- `runtime.http.get/post/postJson/request(...)` sends test traffic through the capture proxy (reverse mode by default; pass `sutBaseUrl` per request to target another service via forward proxy).
- `default: true` responses are fallback stubs: they are matched only if no non-default stub matches and they persist until `teardownTest()`.
- `recordTraffic: false` disables artifact capture and schema validation for mock-only/performance sessions.
- `assetId` on `queueResponse` is optional with a single declared dependency; required with several.
- Responses are literal: `body` is an exact string and `bodyFile` is read verbatim from disk. There is no templating — compute the exact bytes in test code.

## Related Docs

- Runtime HTTP endpoints: `docs/runtime-api.md`
