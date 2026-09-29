# Capture existing tests and mocks

Keep the application, test runner, HTTP client, assertions, and mock server you
already use. Phrond captures the HTTP traffic routed through its proxy:

```text
tests → Phrond → SUT → Phrond → existing mock or dependency
```

## Configure the boundaries

Put shared settings in `.phrond.yaml` at the project root:

```yaml
version: 1
sut:
  id: orders
  url: http://127.0.0.1:8080
  kind: service
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
    schema: schemas/inventory.openapi.yaml
```

Use the existing harness to start Phrond, the SUT, and the mock.
Start the runtime with `phrond --config .phrond.yaml`. Configure the SUT's inventory
base URL to `http://127.0.0.1:18080/__phrond/dep/inventory`. Phrond forwards this
route to `http://127.0.0.1:9090`. Your mock tooling still owns stubs and verification.
Schema paths resolve relative to `.phrond.yaml`; environment substitution and
CI overrides are covered in [configuration and runtime](configuration.md).

## Connect test lifecycle

Call the SDK from the framework you already use. A simple Node suite can use
native setup/cleanup hooks and keep its existing `test` function:

```js
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("@phrond/sdk");
let capture;
before(async () => { capture = await startCapture(); });
after(async () => { if (capture) await capture.finishRun(); });

test("creates an order", () => capture.runTest("creates an order", async session => {
  await existingMock.stubOrderAccepted();
  const response = await fetch(`${session.sutUrl}/orders`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ product_id: "product-123", quantity: 2 })
  });
  assert.equal(response.status, 201);
  await existingMock.verifyOrderLookup("product-123");
}));
```

Keep your existing HTTP client if you have one, pointing its base URL at the
session's `sutUrl`. Run tests normally; load tracing before client modules:

```bash
node --require @phrond/sdk/tracing --test test/orders.test.js
```

`runTest` observes the supplied callback's outcome. It does not observe separate
framework hooks or runner timeouts. For those, use the library's
`createTestAttempt` handle from your framework's lifecycle: call `run` for each
captured fixture/test callback and `finish` from the final completion hook.
The optional Node/Jest adapters already implement runner-specific outcome
handling. Go's `StartTest` uses `t.Cleanup` to observe the test's final result.

The suite owns capture finalization. Shared workers finish their individual
attempts; one global suite hook finalizes the run after all workers finish.
The SDK only calls the running Phrond service over HTTP. See
[JavaScript](../sdk/js/README.md), [Go](../sdk/go/README.md), and
[Jest/tracing](otel-adoption.md) for integration details.

## Choose concurrency deliberately

A shared SUT must propagate W3C `traceparent` through its server and outbound
clients for parallel test correlation. The mock does not need instrumentation.
Without SUT propagation, run tests sequentially or use separate SUT/Phrond
runtimes per worker. Await all captured requests and dependency work before the
test finishes. Several overlapping inbound requests can leave the downstream
parent ambiguous even when the owning test is known; Phrond records that uncertainty.

## Supply specs

Schemas are required and enforced by default for the SUT and each dependency.
For an API without a spec, explicitly set `schema_required: false`. Such exceptions keep
capturing traffic but show skipped validation and unknown schema coverage.
A provided invalid spec never becomes an exception automatically.

## Read the output

SDK/API responses return the exact paths to `index.md` and
`viewer/index.html`, which are written automatically during capture/finalization. Both views describe tested API behaviour, test attempts,
operation coverage, and observed dependency assumptions. Preserve the complete
bundle when uploading it to CI or giving it to an agent.

A mock response records what the test assumed, not proof of provider behaviour
or of which fields the SUT consumed. See [bundle details](okf-bundles.md).

For a custom runner, use the [HTTP API](runtime-api.md): start the run, set up
each named test with a fresh trace context, route its calls through `sut_url`,
and tear it down with the outcome. Finish the run after all workers are done.
