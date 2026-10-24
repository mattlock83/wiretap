# Wiretap

Turn existing HTTP and native gRPC API tests into **portable evidence, Postman
collections and API coverage**. Keep your test framework, clients, assertions and
mocks. Wiretap captures traffic at the application boundary, measures it against
your OpenAPI or Protobuf contracts, and checks mock assumptions against provider
test evidence.

Wiretap is for **isolated test environments using synthetic data and dummy
secrets**. It retains the traffic you send through it, including credentials and
payloads, unchanged.

```text
Existing tests → Wiretap → Your application → Wiretap → Existing mocks or services
                   └──────────────┬──────────────┘
                        One portable run archive
                   Raw evidence · Postman · API coverage
```

## Try it

From this checkout, with Go 1.26+:

```sh
go build -o dist/wiretap ./cmd/wiretap
WIRETAP_BINARY="$PWD/dist/wiretap" go -C examples/go-suite test -count=1 ./...
```

The example hosts an ordinary HTTP handler and captures its standard Go tests
using `Run` and `StartTest`. It finalizes JSON/text coverage and Postman collections
automatically under `examples/go-suite/artifacts/<run-id>/`. See the
[Go example](examples/go-suite/README.md) for Windows commands and the two hooks.

For existing JavaScript tests, choose the [Jest, Playwright Test or Mocha adapter](sdk/js/README.md).
Each manages Wiretap startup and exports around your normal test command. The
[plain JavaScript API and Node harness](sdk/js/docs/javascript.md) remain available
for custom setups. [More examples](examples/README.md) cover existing external
tests, native gRPC, streaming, real clients and mocks. The
[Mocha suite](examples/mocha-runner/README.md) is a small JavaScript example with
ordinary fixture hooks and automatic exports.

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
2. Let the SDK manage Wiretap, or connect to a runtime your harness already owns.
3. Route test requests and application dependency calls through Wiretap.
4. Use an SDK adapter to record each test's final outcome.
5. Finalize once after all workers finish and retain the complete run directory.

Use the [JavaScript](sdk/js/README.md), [Go](sdk/go/README.md), or
[HTTP API](docs/runtime-api.md) integration. Your framework owns pass/fail.
Contract mismatches are recorded separately, so a test deliberately sending an
invalid request can assert its rejection and pass. Setup and capture failures
still surface through cleanup.

The main HTTP API under test requires OpenAPI by default. Consumed HTTP APIs
and mocks need only their URLs; add specs to opt into their validation and
contract coverage. Supplied specs always validate. gRPC requires Protobuf
descriptors, discovered through reflection when no file is supplied, or loaded
from `.proto` files or a descriptor set. Unary and all three streaming shapes
are supported. See the
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

To inspect the Go example's archive later from this checkout, use the built
binary and the exact run directory returned by the SDK:

```bash
./dist/wiretap report --bundle ./examples/go-suite/artifacts/<run-id> --format text
./dist/wiretap export --bundle ./examples/go-suite/artifacts/<run-id> --format all
```

Exports work offline from the complete run archive. HTTP collections use Postman
v2.1; native gRPC uses v3. See [exports and compatibility](docs/artifacts.md).
For tools consuming the results, use the versioned
[machine interface](docs/machine-interface.md).

## Check assumptions against another suite

Compare a consumer's captured mocks with the provider's captured tests:

```sh
wiretap check consumer --consumer ./orders-run --provider ./inventory-run --dependency inventory
```

Use `wiretap check provider` with the same inputs to check from the provider's
viewpoint. Both commands require matching concrete requests and compatible
responses from passing provider tests. Missing provider coverage fails the
check. Collections default to each run's `exports/postman/` directory and are
verified against the raw archive. See [assumption checks](docs/assumption-checks.md)
for dependency mapping, limits, custom collection paths and CI output.

The checker finds changed mock responses, request scenarios absent from provider
tests, drift in exercised OpenAPI 3.0 rules when both contracts are supplied,
Protobuf message/enum declaration changes, and incompatible gRPC wire examples.
Without a consumer dependency spec, the provider's frozen HTTP contract still
validates both captured exchanges. With both HTTP contracts, it catches changed
enums or limits even when the recorded values satisfy both. Its strict comparison
can also flag an extra optional response field or a relaxed rule that a running
consumer would tolerate. Read
[which issues it finds](docs/assumption-checks.md#issues-the-check-will-find),
[what it cannot establish](docs/assumption-checks.md#what-the-check-will-not-establish)
and [how to interpret failures](docs/assumption-checks.md#interpreting-results)
before using it as a CI gate. Assertions about behavior and expected dependency
calls remain part of your tests.

Keep complete runs in your shared artifact store under stable application/API
IDs, tested revisions and run IDs. The [shared snapshot workflow](docs/assumption-checks.md#share-provider-snapshots)
shows provider and consumer configuration, a folder convention and the checks
to run after downloading the selected archives.

## Product scope and documentation

Wiretap captures existing tests, exports their evidence and verifies concrete
consumer/provider examples and exercised contract rules offline. A passing
check covers those examples and supported rules; it does not prove arbitrary
business behavior. Wiretap does not
generate or execute additional tests.

Start with the [documentation index](docs/README.md) for setup, CI, formats and
troubleshooting. The [product scope](docs/roadmap.md) describes the completion
criteria. [Local packages](docs/local-release.md) can be built and installed
without publishing anything externally.
