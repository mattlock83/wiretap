# Wiretap

Turn existing HTTP and native gRPC API tests into **portable evidence, Postman
collections and API coverage**. Keep your test framework, clients, assertions and
mocks. Wiretap captures traffic at the application boundary and measures it
against your OpenAPI or Protobuf contracts.

Wiretap is for **isolated test environments using synthetic data and dummy
secrets**. It retains the traffic you send through it, including credentials and
payloads. There is no built-in redaction.

```text
Existing tests → Wiretap → Your application → Wiretap → Existing mocks or services
                   └──────────────┬──────────────┘
                        One portable run archive
                   Raw evidence · Postman · API coverage
```

## Try it

From this checkout, with Go 1.26+, Node.js 22+, Bash and curl available:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The example starts its services, runs a test, finalizes the capture and generates
JSON coverage and Postman collections automatically. It prints a short coverage
summary and exact output paths. Everything stays under `artifacts/<run-id>/`.

For existing JavaScript tests, choose the [Jest, Playwright Test or Mocha adapter](sdk/js/README.md).
Each manages Wiretap startup and exports around your normal test command. The
[plain JavaScript API and Node harness](sdk/js/docs/javascript.md) remain available
for custom setups. [More examples](examples/README.md) cover existing external
tests, native gRPC, streaming, real clients and mocks.

## Add it to your suite

For Jest, Playwright Test and Mocha, follow the [SDK setup guide](sdk/js/README.md):
configure the contract, add the runner wrapper and route your shared client
through capture. The adapter handles startup and finalization.

For standard Go tests, add `wiretap.Run(m)` in `TestMain` and
`wiretap.StartTest(t)` in a test or shared setup helper. The [Go SDK](sdk/go/README.md)
manages startup, cleanup and exports; the [Go example](examples/go-suite/README.md)
shows a complete suite with an existing HTTP handler and client.

For a custom harness:

1. Put application/dependency URLs and contracts in `.wiretap.yaml`.
2. Start Wiretap alongside your application and mocks in your existing harness.
3. Route test requests and application dependency calls through Wiretap.
4. Use an SDK adapter to record each test's final outcome.
5. Finalize once after all workers finish and retain the complete run directory.

Use the [JavaScript](sdk/js/README.md), [Go](sdk/go/README.md), or
[HTTP API](docs/runtime-api.md) integration. Your framework owns pass/fail.
Contract mismatches are recorded separately, so a test deliberately sending an
invalid request can assert its rejection and pass. Setup and capture failures
still surface through cleanup.

HTTP requires an OpenAPI spec by default; individual APIs can explicitly opt
out. gRPC requires Protobuf contracts from reflection, `.proto` files or a
descriptor set. Unary and all three streaming shapes are supported. See the
[gRPC guide](docs/grpc-user-guide.md).

For parallel tests sharing one application, propagate W3C trace context through
its server and outbound clients. Otherwise run sequentially or isolate workers.
See [tracing and concurrency](docs/otel-adoption.md).

## Understand the result

| Output | What it tells you |
| --- | --- |
| `bundle.json`, `tests/`, `contracts/`, `payloads/` | What each test sent and received, its outcome, validation diagnostics and frozen contracts. |
| `exports/coverage.txt` | A compact first view of outcomes, exercised operations, condition gaps and unknowns. |
| `exports/coverage.json` | Which declared operations and supported contract conditions were exercised, with gaps and unknowns. |
| `exports/postman/` | Captured HTTP and gRPC interactions grouped by test, for the application and mocked dependencies. |
| `exports/result.json` | Exact export paths and any export errors. |

Start with the summary, then follow the JSON report's test references when you
need to understand a gap. One test can exercise many conditions; an unknown
measurement is not a pass. A declared API with no captured tests still appears
as untested when its contract is available.

The demo builds a temporary binary. To inspect its archive later from this
checkout, use `go run`, or [build a persistent binary](docs/local-release.md):

```bash
go run ./cmd/wiretap report --bundle ./artifacts/<run-id> --format text
go run ./cmd/wiretap export --bundle ./artifacts/<run-id> --format all
```

Exports work offline from the complete run archive. HTTP collections use Postman
v2.1; native gRPC uses v3. See [exports and compatibility](docs/artifacts.md).
For tools consuming the results, use the versioned
[machine interface](docs/machine-interface.md).

## Product scope and documentation

Wiretap's job ends at capture and export. Mock responses record consumer
assumptions; comparing them against provider evidence belongs in a separate
future diffing tool. Wiretap does not generate or execute additional tests.

Start with the [documentation index](docs/README.md) for setup, CI, formats and
troubleshooting. The [product scope](docs/roadmap.md) describes the completion
criteria. [Local packages](docs/local-release.md) can be built and installed
without publishing anything externally.
