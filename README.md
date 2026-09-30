# Wiretap

Wiretap turns existing API tests into documentation of the behaviour they
exercise and the dependency assumptions they rely on. It captures request and
response sequences at your system's boundaries and writes a portable OKF bundle
for humans and agents to inspect.

```text
tests → Wiretap → your application
                    ↓
                 Wiretap → existing mocks or services
                    ↓
               OKF run bundle
         API · dependencies · tests · coverage
```

Keep your test runner, HTTP client, assertions, and mock tooling. Route traffic
through Wiretap and connect test setup and teardown using the JavaScript or Go
SDK, or the HTTP API. Your existing harness starts and configures the application
and its dependencies, and starts Wiretap as a separate service. The SDK is a
library called by your test framework. An optional `wiretap explore` runner uses
a captured happy flow to generate invalid-input provider tests, with project-owned
commands supplying fixtures, authentication and property checks.

## Try it

From this repository, with Go and Node installed:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The example uses `.wiretap.yaml`, native test hooks, and ordinary `fetch` calls.
It captures a happy flow, then runs `wiretap explore` to check generated invalid
inputs. It prints separate capture and exploration bundle paths. Open either
bundle's `viewer/index.html` for the offline HTML dashboard or `index.md` for
linked OKF pages.

For a richer example using a real application image and an HTTP dependency mock,
try [Microsvc Transactions](examples/microsvc-transactions/README.md). It captures
withdrawal tests and explores invalid inputs with authentication and fixture hooks.

## Add it to existing tests

Check in a `.wiretap.yaml`:

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
`http://127.0.0.1:18080/__wiretap/dep/inventory`. The proxy forwards those calls
to the declared upstream. Omit `dependencies` to capture only inbound traffic.

Call the SDK from your existing test framework. For a simple Node suite:

```js
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("@wiretap/sdk");
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
go build -o /tmp/wiretap ./cmd/wiretap
/tmp/wiretap --config .wiretap.yaml
```

Run tests separately using their existing command, with test-side instrumentation:

```bash
node --require @wiretap/sdk/tracing --test test/orders.test.js
```

The SDK connects to `http://127.0.0.1:18000` by default; set `WIRETAP_BASE_URL`
for another address. Suite hooks start and finalize capture over HTTP. The
runtime automatically writes the bundle and its views; no report command is
needed. SDK/API responses provide the exact output paths.

`runTest` captures the supplied callback's outcome. Framework integrations can
use `createTestAttempt`, bind each fixture/test callback to its context, and
finish it from their completion hook with the framework's final result. Optional
Node/Jest adapters implement runner-specific timeout and outcome handling. See
the [SDK guide](sdk/js/README.md) for these choices and shared-worker ownership.
Shared-SUT parallel tests require inbound-to-outbound W3C propagation; otherwise
serialize or isolate workers. No Wiretap-specific propagation code is needed in the SUT.

Specs are required by default. An individual API may opt out with
`schema_required: false`, making skipped validation and unknown coverage visible.
Each dependency marked as a mock also gets an inferred OpenAPI draft in the
bundle, whether or not a spec was supplied. It describes observed mock assumptions;
supplied specs still govern validation.
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
- Per-spec contract condition keys: covered, not covered, unknown, unsupported,
  or excluded, with test/flow evidence and separate provider and mock results.
  Open the HTML **Contract coverage** tab or `contract-coverage.md` / `.json` in the bundle.
- For exploration runs, which generated invalid inputs produced the expected
  rejection, with separate rejection coverage and explicit check results.
- A system overview (`system.md`) and verification queue (`verification.md`)
  separating unverified dependency assumptions, contract mismatches, coverage
  gaps and evidence limits. `knowledge.json` exposes the same identities and
  evidence references for knowledge-base ingestion and agent follow-up.

A mock response records an assumption made by the test. It does not establish
which fields the application used or prove that the real provider behaves the
same way. Cross-repository agreement checking is future work.
The [knowledge index](docs/bundle-knowledge.md) helps select that verification
work; it does not perform comparisons or execute its suggested tasks.

JSON evidence, frozen schemas, and retained payloads are authoritative. The
OKF pages and offline HTML dashboard are rebuildable views:

```bash
wiretap report --bundle ./artifacts/<run-id>
wiretap export --bundle ./artifacts/<run-id> --format json --output coverage.json
```

Diagnostic JSON/YAML exports are also available. Retained bodies
are limited to 4 MiB each by default while delivery streams through the proxy.

## Explore provider validation

Keep lifecycle scripts in your project, configure them under `explore.hooks` in
`.wiretap.yaml`, and run against a live test provider:

```bash
wiretap explore --bundle ./artifacts/<source-run> --test "creates an order" --config .wiretap.yaml
```

Wiretap first checks a fresh happy-path baseline, then executes bounded invalid
variations with fresh fixtures. Expected validation errors default to 400/422;
authentication failures and server errors do not count. The source stays unchanged;
a new OKF bundle contains the attempts and updated coverage. Hooks receive JSON
over standard input and can be written in any language. See the
[exploration guide](docs/exploration.md), including the runnable SQLite example.

## Documentation

- [Configuration and runtime](docs/configuration.md)
- [Getting started with existing tests](docs/getting-started-existing-tests.md)
- [JavaScript SDK](sdk/js/README.md) · [Go SDK](sdk/go/README.md)
- [Tracing and parallel tests](docs/otel-adoption.md)
- [Capture bundles and reports](docs/okf-bundles.md)
- [Contract coverage rules](docs/contract-coverage-rules.md)
- [Provider exploration and external hooks](docs/exploration.md)
- [Runtime HTTP API](docs/runtime-api.md) · [Architecture](docs/architecture.md)
- [JSON evidence contract](docs/bundle-evidence.md) · [OKF profile](docs/okf-profile.md)
- [CI example](docs/ci-example.md) · [HTTPS trust](docs/https-trust.md)
- [Troubleshooting](docs/troubleshooting.md)
