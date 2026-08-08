# Getting started with existing tests and a mock server

Use this guide when your project already has:

- tests that start or connect to the service under test (SUT);
- a mock server such as WireMock or MockServer; and
- test setup that configures the SUT and its mocked dependencies.

Keep that setup. Phrond does not start the SUT or replace the mock server. It
sits only between the tests and the SUT:

```text
tests  →  Phrond  →  SUT  →  existing mock server
```

Phrond records the test→SUT requests and responses. The SUT→mock traffic
continues to be handled and verified by the existing mock tooling.

## 1. Start the existing test environment

Start the mock server, configure its responses, and start the SUT exactly as
you do today. For example:

```text
SUT:         http://127.0.0.1:8080
Mock server: http://127.0.0.1:9090
```

Leave the SUT pointing at the mock server. Neither one should point at Phrond.

## 2. Start Phrond

```bash
PHROND_HTTP_LISTEN=127.0.0.1:18000 \
PHROND_PROXY_LISTEN=127.0.0.1:18080 \
PHROND_ARTIFACT_DIR=./artifacts \
go run ./cmd/phrond
```

Port `18000` is Phrond's control API. Port `18080` is the capture proxy that
will forward requests to the SUT.

## 3. Route test calls through Phrond

With Node's built-in test runner and `@phrond/sdk`:

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@phrond/sdk");

const phrond = createNodeTestSuite({
  baseUrl: "http://127.0.0.1:18000",
  sutBaseUrl: "http://127.0.0.1:8080",
  strictSchema: false
});

phrond.test("creates an order", async () => {
  // Keep the existing mock setup and verification.
  await existingMock.stubOrderAccepted();

  // Send only the test-to-SUT call through Phrond.
  const response = await phrond.http.postJson("/orders", {
    product_id: "product-123",
    quantity: 2
  });

  assert.equal(response.status, 201);
  await existingMock.verifyOrderLookup("product-123");
});
```

`phrond.test(...)` preserves the test name in the artifact and records whether
the test passed or failed.

If the tests already have an HTTP client, keep it and change only its base URL:

```js
phrond.test("gets an order", async () => {
  const client = createExistingClient({ baseUrl: phrond.sutUrl });
  const response = await client.get("/orders/order-123");
  assert.equal(response.status, 200);
});
```

`phrond.sutUrl` is Phrond's capture address. Phrond forwards those calls to
the already-running `sutBaseUrl`.

## 4. Optionally validate against OpenAPI

Add the SUT's OpenAPI schema when you want Phrond to validate the captured
requests and responses:

```js
const phrond = createNodeTestSuite({
  baseUrl: "http://127.0.0.1:18000",
  sutBaseUrl: "http://127.0.0.1:8080",
  schemas: [{
    id: "orders-api",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "schemas/orders.openapi.yaml" }
  }]
});
```

Without a schema, set `strictSchema: false` as shown in the first example.
Capture still works and validation is recorded as skipped.

## 5. Generate a report

Each test writes a YAML artifact under `./artifacts`. Generate an HTML report
containing the captured request and response payloads with:

```bash
go run ./cmd/phrond-coverage \
  --artifacts ./artifacts \
  --sut-name "Orders Service" \
  --format html \
  --output ./artifacts/coverage.html
```

The same command can export Postman JSON or Open Workflow YAML by changing
`--format` to `postman` or `workflow`.

## Other languages and test frameworks

Any test framework can integrate through Phrond's HTTP API:

1. Start capture with `POST /v1/startServer` and the SUT in
   `proxy.target_url`.
2. Start a named test with `POST /v1/setupTest`.
3. Send the test's HTTP calls to the returned `sut_url`.
4. Finish with `POST /v1/teardownTest` and the test's pass/fail status.

See the [runtime HTTP API](runtime-api.md) for complete request and response
examples.

## Common mistakes

- Do not ask Phrond to start or configure the SUT or mock server.
- Do not point the SUT's dependencies at Phrond.
- Do send test→SUT calls through `phrond.sutUrl`; direct calls are not captured.
- For HTTPS SUTs, follow the [HTTPS trust setup](https-trust.md).
- Run tests serially by default. Parallel tests need explicit capture sessions.
