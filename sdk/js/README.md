# JS SDK

The JS SDK is session-based and stack-based. Phrond owns its capture proxy,
mock gateway, named test sessions, and artifacts. It does **not** start, stop,
or configure the system under test (SUT); Docker Compose, Testcontainers,
Spring Boot, Kubernetes, CI, or another application harness owns that work.

## Quick start

Start and configure the SUT before the test process. Its dependency URL is
deterministic from the configured Phrond mock address and asset id:

```bash
export PHROND_BASE_URL=http://127.0.0.1:18000
export PHROND_PROXY_LISTEN=127.0.0.1:19000
export PHROND_MOCK_LISTEN=127.0.0.1:19001
export SUT_BASE_URL=http://127.0.0.1:18090

# Configure this in the SUT's own launcher, not through Phrond:
export ACCOUNT_BASE_URL=http://127.0.0.1:19001/__phrond/dep/account-service
```

The consumer test contains only capture and mock intent:

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@phrond/sdk");

const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  proxy: { listenAddr: process.env.PHROND_PROXY_LISTEN },
  mockServer: { listenAddr: process.env.PHROND_MOCK_LISTEN },
  sutBaseUrl: process.env.SUT_BASE_URL,
  dependencies: [
    {
      assetId: "account-service",
      assetName: "Account Service"
    }
  ]
});

phrond.test("hello world happy flow", async () => {
  await phrond.queueResponse({
    id: "account-lookup",
    match: { method: "GET", path: "/accounts/acct-100" },
    response: { status: 200, json: { id: "acct-100", name: "Matt" } }
  });

  const response = await fetch(`${phrond.sutUrl}/hello?account=acct-100`);
  assert.equal(response.status, 200);

  await phrond.verify("account-lookup", ["body.name"]);
});
```

`createNodeTestSuite` registers `node:test` suite hooks for Phrond only. It
starts Phrond's proxy/mock components, creates one session per
`phrond.test(...)`, records the exact test name (`hello world happy flow`),
sets pass/fail from the test outcome, and closes Phrond after the suite.

## Key concepts

- **Consumer-owned SUT lifecycle**: `sutBaseUrl` tells the reverse capture
  proxy where an already-running SUT is listening. It does not launch the
  application, inject environment variables, wait for readiness, or stop it.
- **Dependencies**: declare each downstream service with a stable `assetId`
  (the cross-repo key a broker later uses to join consumer assumptions to the
  provider). Each dependency gets its own stable mock base URL via
  `<mock-base>/__phrond/dep/<assetId>`. External orchestration can configure
  this before tests start. `phrond.dependencyUrl(assetId)` returns the same URL
  after Phrond has started.
- **External mocks**: add
  `mock: { mode: "external", upstreamUrl: wireMockUrl }` to a dependency to
  keep WireMock or another HTTP mock. The SUT still uses
  `runtime.dependencyUrl(assetId)`, so Phrond captures and attributes the
  exchange. Built-in and external dependencies can coexist.
- **Reverse capture**: with `sutBaseUrl` set, tests call `runtime.sutUrl` (the
  proxy address) with any HTTP client; requests are captured and forwarded. No
  proxy configuration or CA trust needed for HTTP.
- **Zero-config correlation**: session URLs are stable — no per-test rewiring.
  Run tests serially (one session at a time), or pass
  `setupTest({ explicitSession: true })` for parallel suites.
- **Named tests**: `phrond.test("hello world happy flow", ...)` records that
  exact name in `artifact.testcase.name`. The lower-level
  `bindTest(runtimeOrGetter, test)` adapter behaves the same way.
- **Schemas are optional**: without any, artifact validation is `skipped`, not
  failed. Attach OpenAPI specs when you have them for contract validation.
- **JSON shorthand**: `match.json` and `response.json` are JSON-encoded by the
  SDK; response JSON also gets `Content-Type: application/json` unless
  supplied explicitly.

## API

- `createNodeTestSuite({ ...startCaptureOptions, runtimeConfig, testApi, testDefaults, autoHooks })`
- `phrond.test(name, options?, fn)`
- `phrond.sutUrl` / `phrond.http` / `phrond.dependencyUrl(assetId)`
- `phrond.queueResponse(...)` / `phrond.verify(...)` / inspection and report methods
- `startCapture(options)` — preferred lower-level name; starts only Phrond components
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

`startServer` remains supported for backwards compatibility with the original API.
Use `startCapture` in new lower-level integrations because its ownership
boundary is clearer.

## Convenience methods

- `runtime.scenarioName(name)` sets the next test name (optional if `setupTest({ name })` is used).
- `runtime.http.get/post/postJson/request(...)` sends test traffic through the capture proxy (reverse mode by default; pass `sutBaseUrl` per request to target another service via forward proxy).
- `default: true` responses are fallback stubs: they are matched only if no non-default stub matches and they persist until `teardownTest()`.
- `recordTraffic: false` disables artifact capture and schema validation for mock-only/performance sessions.
- `assetId` on `queueResponse` is optional with a single declared dependency; required with several.
- `queueResponse` is rejected for external dependencies; configure and verify
  those stubs through the external mock's own API.
- Responses are literal: `body` is an exact string, `bodyFile` is read verbatim
  from disk, and `json` is SDK sugar for `JSON.stringify`. There is no
  templating.

## Related Docs

- Runtime HTTP endpoints: `docs/runtime-api.md`
