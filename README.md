# Phrond

Phrond turns existing API tests into documentation of the behaviour they
exercise and the dependency assumptions they rely on. It captures request and
response sequences at your system's boundaries and writes a portable OKF bundle
for humans and agents to inspect.

```text
tests → Phrond → your application
                    ↓
                 Phrond → existing mocks or services
                    ↓
               OKF run bundle
         API · dependencies · tests · coverage
```

Keep your test runner, HTTP client, assertions, and mock tooling. Route traffic
through Phrond and connect test setup and teardown using the JavaScript or Go
SDK, or the HTTP API. Your existing harness starts and configures the application
and its dependencies, and starts Phrond as a separate service. The SDK is a
library called by your test framework; Phrond never launches your tests.

## Try it

From this repository, with Go and Node installed:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The example uses `.phrond.yaml`, native test hooks, and ordinary `fetch` calls.
Open the resulting bundle's `viewer/index.html` for the offline HTML dashboard
or `index.md` for linked OKF pages.

## Add it to existing tests

Check in a `.phrond.yaml`:

```yaml
version: 1
sut:
  id: orders
  url: http://127.0.0.1:8080
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
    schema: schemas/inventory.openapi.yaml
```

Configure your application's inventory URL to
`http://127.0.0.1:18080/__phrond/dep/inventory`. The proxy forwards those calls
to the declared upstream. Omit `dependencies` to capture only inbound traffic.

Call the SDK from your existing test framework. For a simple Node suite:

```js
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("@phrond/sdk");
let capture;

before(async () => { capture = await startCapture(); });
after(async () => { if (capture) await capture.finishRun(); });

test("gets an order", () => capture.runTest("gets an order", async session => {
  const response = await fetch(`${session.sutUrl}/orders/order-123`);
  assert.equal(response.status, 200);
}));
```

Build and start the runtime alongside your application and mocks:

```bash
go build -o /tmp/phrond ./cmd/phrond
/tmp/phrond --config .phrond.yaml
```

Run tests separately using their existing command, with test-side instrumentation:

```bash
node --require @phrond/sdk/tracing --test test/orders.test.js
```

The SDK connects to `http://127.0.0.1:18000` by default; set `PHROND_BASE_URL`
for another address. Suite hooks start and finalize capture over HTTP. The
runtime automatically writes the bundle and its views; no report command is
needed. SDK/API responses provide the exact output paths.

`runTest` captures the supplied callback's outcome. Framework integrations can
use `createTestAttempt`, bind each fixture/test callback to its context, and
finish it from their completion hook with the framework's final result. Optional
Node/Jest adapters implement runner-specific timeout and outcome handling. See
the [SDK guide](sdk/js/README.md) for these choices and shared-worker ownership.
Shared-SUT parallel tests require inbound-to-outbound W3C propagation; otherwise
serialize or isolate workers. No Phrond-specific propagation code is needed in the SUT.

Specs are required by default. Explicit SUT schema generation can draft an
OpenAPI contract from passing tests. An individual API may opt out with
`schema_required: false`, making skipped validation and unknown coverage visible.
See [configuration and runtime](docs/configuration.md) and the
[existing-tests guide](docs/getting-started-existing-tests.md).

## What the bundle tells you

- Which API operations your tests exercised, with requests, responses, outcomes,
  and schema validation results.
- What dependencies returned during those tests, including whether the upstream
  was declared as a mock or a service.
- Which exchanges belong to each test attempt, with trace correlation and
  explicitly labelled inferred request relationships.
- Observed operation coverage against supplied schemas, with incomplete captures
  and unknown provenance kept visible.

A mock response records an assumption made by the test. It does not establish
which fields the application used or prove that the real provider behaves the
same way. Cross-repository agreement checking is future work.

JSON evidence, frozen schemas, and retained payloads are authoritative. The
OKF pages and offline HTML dashboard are rebuildable views:

```bash
phrond report --bundle ./artifacts/<run-id>
phrond export --bundle ./artifacts/<run-id> --format openapi --output api.yaml
```

Workflow and diagnostic JSON/YAML exports are also available. Retained bodies
are limited to 4 MiB each by default while delivery streams through the proxy.

## Documentation

- [Configuration and runtime](docs/configuration.md)
- [Getting started with existing tests](docs/getting-started-existing-tests.md)
- [JavaScript SDK](sdk/js/README.md) · [Go SDK](sdk/go/README.md)
- [Tracing and parallel tests](docs/otel-adoption.md)
- [Capture bundles and reports](docs/okf-bundles.md)
- [Runtime HTTP API](docs/runtime-api.md) · [Architecture](docs/architecture.md)
- [JSON evidence contract](docs/bundle-evidence.md) · [OKF profile](docs/okf-profile.md)
- [CI example](docs/ci-example.md) · [HTTPS trust](docs/https-trust.md)
- [Troubleshooting](docs/troubleshooting.md)
