# Getting started with existing tests and a mock server

Use this guide when your project already has:

- tests that start or connect to the service under test (SUT);
- a mock server such as WireMock or MockServer; and
- test setup that configures the SUT and its mocked dependencies.

Keep that setup. Phrond does not start the SUT or replace the mock server. The
same capture proxy sits on both sides of the SUT:

```text
tests  →  Phrond capture proxy  →  SUT
                                     ↓
                        Phrond capture proxy  →  existing mock server
```

One artifact records and correlates the test→SUT and SUT→mock requests and
responses. Your existing mock tooling still owns stubs, responses,
verification, configuration, and lifecycle.

## 1. Start the existing test environment

Start the mock server and configure its responses exactly as you do today. Use
your existing orchestration to start the SUT, setting its dependency base URL
to the capture route instead of directly to the mock server. For example:

```text
SUT:                     http://127.0.0.1:8080
Actual mock server:      http://127.0.0.1:9090
Phrond capture proxy:    http://127.0.0.1:18080
SUT inventory base URL:  http://127.0.0.1:18080/__phrond/dep/inventory
```

Phrond will forward the captured inventory requests to the existing mock
server. The mock server itself does not need to know that Phrond exists.

## 2. Start Phrond

```bash
PHROND_HTTP_LISTEN=127.0.0.1:18000 \
PHROND_PROXY_LISTEN=127.0.0.1:18080 \
PHROND_ARTIFACT_DIR=./artifacts \
go run ./cmd/phrond
```

Port `18000` is Phrond's control API. Port `18080` is the single capture proxy
that forwards inbound requests to the SUT and outbound requests to the declared
mock server.

## 3. Route test calls through Phrond

With Node's built-in test runner and `@phrond/sdk`, install SDK dependencies
and preload standard instrumentation before HTTP client modules:

```bash
node --require @phrond/sdk/tracing --test test/*.test.js
```

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@phrond/sdk");

const phrond = createNodeTestSuite({
  baseUrl: "http://127.0.0.1:18000",
  sutBaseUrl: "http://127.0.0.1:8080",
  schemas: [{
    id: "orders-api",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "schemas/orders.openapi.yaml" }
  }],
  dependencies: {
    inventory: {
      upstreamUrl: "http://127.0.0.1:9090",
      schema: {
        id: "inventory-api",
        type: "openapi",
        source: { type: "file", path: "schemas/inventory.openapi.yaml" }
      }
    }
  }
});

phrond.test("creates an order", async () => {
  // Keep the existing mock setup and verification.
  await existingMock.stubOrderAccepted();

  // Phrond captures this inbound call and the SUT's downstream mock request.
  const response = await phrond.http.postJson("/orders", {
    product_id: "product-123",
    quantity: 2
  });

  assert.equal(response.status, 201);
  await existingMock.verifyOrderLookup("product-123");
});
```

`phrond.test(...)` preserves the test name in the artifact and records whether
the test passed or failed. The artifact includes:

```yaml
traffic:
  - id: flow-1
    channel: sut_to_dependency
    asset_id: inventory
    parent_flow_id: flow-2
  - id: flow-2
    channel: test_to_sut
```

Both entries also contain their complete bounded request/response evidence and
shared W3C trace ID when the SUT propagates `traceparent` through its server and
outbound clients. If it does not, run tests sequentially: Phrond records
`test_correlation: active_session` and uses timing for an inferred parent. If
several requests overlap, the parent can be ambiguous and is left unset. The downstream exchange
normally finishes first, so its flow ID may precede the parent ID.

If the tests already have an HTTP client, keep it and change only its base URL:

```js
phrond.test("gets an order", async () => {
  const client = createExistingClient({ baseUrl: phrond.sutUrl });
  const response = await client.get("/orders/order-123");
  assert.equal(response.status, 200);
});
```

`phrond.sutUrl` is Phrond's capture address. Phrond forwards those calls to
the already-running `sutBaseUrl`. Once the suite is running,
`phrond.dependencyUrl("inventory")` returns the dependency capture address if
your existing harness needs to discover it dynamically.

## 4. Generate a schema if you do not have one

OpenAPI validation is strict by default. If the service does not have a schema,
explicitly ask Phrond to generate a standard OpenAPI 3.1 draft:

```js
const phrond = createNodeTestSuite({
  baseUrl: "http://127.0.0.1:18000",
  sutBaseUrl: "http://127.0.0.1:8080",
  generateSchema: {
    output: "artifacts/orders.generated.openapi.yaml",
    title: "Orders API",
    version: "1.0.0"
  }
});
```

The SDK combines the suite's captures and writes the draft during suite
teardown. Captured values are not included as examples. Review the generated
contract, move it into the project's schemas directory, then replace
`generateSchema` with the normal `schemas` configuration shown above.

Schema generation models the SUT's provided API. An existing external
dependency must still supply its own OpenAPI schema through `dependencies`, so
downstream contract validation remains strict during SUT onboarding.

Supplying neither option fails setup with a useful error. Supplying both is
rejected so Phrond never silently changes an enforced contract.

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

The same command can export generated OpenAPI, Postman JSON, or Open Workflow
YAML by changing `--format` to `openapi`, `postman`, or `workflow`.

## Other languages and test frameworks

Any test framework can integrate through Phrond's HTTP API:

1. Start capture with `POST /v1/startServer` and the SUT in
   `proxy.target_url`.
2. Start a named test with `POST /v1/setupTest`, supplying `schemas` or
   explicitly setting `generate_schema: true`, plus any external dependencies.
3. Send the test's HTTP calls to the returned `sut_url`; configure the SUT's
   downstream clients to use the returned `dependencies` capture URLs.
4. Finish with `POST /v1/teardownTest` and the test's pass/fail status.
5. In generation mode, call `POST /v1/generateSchema` after the final test.

See the [runtime HTTP API](runtime-api.md) for complete request and response
examples.

## Common mistakes

- Do not ask Phrond to start or configure the SUT or mock server.
- Do point the SUT's captured dependency base URL at its Phrond dependency
  route; direct calls to the mock server cannot be observed.
- Do continue stubbing and verifying with your existing mock tooling.
- Do provide an OpenAPI schema for every declared dependency.
- Do send test→SUT calls through `phrond.sutUrl`; direct calls are not captured.
- For HTTPS SUTs, follow the [HTTPS trust setup](https-trust.md).
- Run tests serially by default. Shared-SUT parallel tests need independent test
  traces, `explicitSession: true`, and working inbound-to-outbound W3C propagation.
  See [test tracing, Jest, Go, and isolation options](otel-adoption.md).
