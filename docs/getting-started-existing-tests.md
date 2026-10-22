# Capture existing tests

This walkthrough uses HTTP. For native gRPC with existing generated clients and
reflection or local Protobuf contracts, follow the [gRPC guide](grpc-user-guide.md).

Keep your application, test framework, HTTP client, assertions and mock server.
Wiretap captures the HTTP traffic you route through its proxy and groups it by
test attempt.

```text
tests → Wiretap → application → Wiretap → existing mock or dependency
```

For a working example first, see [examples](../examples/README.md).

## 1. Build the runtime and choose an integration

From the Wiretap repository, install the binary with Go 1.24+:

```bash
go install ./cmd/wiretap
```

The commands below assume Go's install directory is on `PATH`. You can also
build a local binary with `go build -o /tmp/wiretap ./cmd/wiretap` and use that
path directly.

Choose the [JavaScript SDK](../sdk/js/README.md), [Go SDK](../sdk/go/README.md),
or [HTTP API](runtime-api.md). The SDKs call the runtime over HTTP; they do not
start it or launch your tests. The example below uses the JavaScript library
from native Node test hooks.

When working from this checkout, install its JavaScript dependencies with
`npm ci --prefix sdk/js`. To use the checkout in another Node project, add it
there with `npm install --save-dev /absolute/path/to/wiretap/sdk/js`.

## 2. Configure the boundaries

Put `.wiretap.yaml` at your test project's root:

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

Here, `sut` is the system under test. Schema paths resolve relative to this
file. OpenAPI specs are required for the SUT and each dependency. If an API has
no spec, explicitly set `schema_required: false` on that API; its validation and
coverage will show the omission. A supplied invalid spec still fails setup.

Omit `dependencies` when capturing inbound traffic only. Use stable API IDs
when bundles from different repositories will describe the same API. See
[configuration](configuration.md) for environment variables and overrides.

## 3. Start the services and route their traffic

Use your existing harness to start Wiretap, the application and its mocks:

```bash
wiretap --config .wiretap.yaml
```

Configure the application's inventory base URL as
`http://127.0.0.1:18080/__wiretap/dep/inventory`. Wiretap forwards that route to
`http://127.0.0.1:9090`. Your mock tooling still owns stubs and verification.

The default control API is `http://127.0.0.1:18000`; the capture proxy listens
on port `18080`. Set `WIRETAP_BASE_URL` in the test process if the control API
uses a different address. The application and mock must be running before
requests are sent through the proxy.

## 4. Connect the test lifecycle

Call the library from your existing framework and use the session's `sutUrl`
as your HTTP client's base URL:

```js
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("@wiretap/sdk");
let capture;

before(async () => { capture = await startCapture(); });
after(async () => { if (capture) await capture.finishRun(); });

test("gets an order", () => capture.runTest("gets an order", async session => {
  // Keep existing fixture setup and mock configuration here.
  const response = await fetch(`${session.sutUrl}/orders/order-123`);
  assert.equal(response.status, 200);
  // Keep existing assertions and mock verification here.
}));
```

Load tracing before HTTP client modules, then run the tests:

```bash
node --require @wiretap/sdk/tracing --test test/orders.test.js
```

`runTest` records the supplied callback's outcome. To include separate framework
hooks or runner timeouts, use `createTestAttempt` or an optional Node/Jest adapter;
see the [JavaScript guide](../sdk/js/README.md). Go's `StartTest` uses `t.Cleanup`
to observe the final result.

One suite owner finalizes capture after every worker finishes. Workers finish
their own attempts; they must not stop a shared run early. Await captured HTTP
calls and expected dependency work before finishing an attempt.

## 5. Choose concurrency and inspect the artifacts

A shared application must propagate W3C `traceparent` from its server to its
outbound clients for parallel test correlation. The mock needs no instrumentation.
Without application propagation, run tests and requests sequentially or give
each worker its own application and Wiretap runtime. See
[tracing and concurrency](otel-adoption.md).

SDK/API responses give the exact `bundle_path`. Read `bundle.json` and its
referenced `tests/*.json` recordings to inspect requests, responses and outcomes.
Keep the complete directory for CI or external analysis.

Generate coverage with `wiretap report --bundle <path> --output coverage.json`.
A mock reply records a test assumption, not proof of real provider behaviour.
See the [artifact guide](artifacts.md) for exports and the
[coverage rules](contract-coverage-rules.md) for supported cases and limits.
