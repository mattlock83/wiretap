# Wiretap documentation

Your test framework owns execution and assertions. SDK or HTTP lifecycle calls
identify the running test and its outcome. Wiretap captures HTTP/native gRPC API
boundaries, validates traffic against supplied contracts, and writes portable
JSON artifacts. JSON/YAML reports calculate operation and contract coverage; a
text summary gives the first view of results. Offline assumption checks compare
captured mocks with provider test examples. Use isolated tests with synthetic
data and dummy secrets: retained traffic is unchanged.

## Set up capture

| Guide | Use it to |
| --- | --- |
| [Capture existing tests](getting-started-existing-tests.md) | Configure boundaries, connect lifecycle hooks and run capture. |
| [Go suite](../examples/go-suite/README.md) | Add `Run` and `StartTest` to an ordinary HTTP test suite. |
| [Plain Node suite](../examples/node-suite/README.md) | Use the managed runtime helper with two test files and one suite owner. |
| [Runnable examples](../examples/README.md) | Try HTTP/gRPC capture, database fixtures or Microsvc Transactions. |
| [Configuration and commands](configuration.md) | Configure `.wiretap.yaml`, ownership, specs and CLI commands. |
| [Local installation and release preparation](local-release.md) | Build versioned binaries, pack the SDK and test installation in an external project. |
| [JavaScript SDK](../sdk/js/README.md) | Choose Jest, Playwright Test, Mocha or the plain JavaScript lifecycle API. |
| [Go SDK](../sdk/go/README.md) | Add managed capture to standard `testing` with `Run` and `StartTest`. |
| [Runtime HTTP API](runtime-api.md) | Integrate another language or test framework directly. |
| [Tracing and concurrency](otel-adoption.md) | Reuse OTel and choose shared, sequential or isolated capture. |
| [HTTPS trust](https-trust.md) | Configure the proxy CA for decrypted HTTPS capture. |
| [CI example](ci-example.md) | Run tests and preserve complete artifacts in CI. |
| [Troubleshooting](troubleshooting.md) | Resolve routing, tracing, schema and capture problems. |

## Read and export results

| Guide | Use it to |
| --- | --- |
| [Capture artifacts and exports](artifacts.md) | Read raw artifacts, a text summary, JSON/YAML coverage and Postman collections. |
| [Assumption checks](assumption-checks.md) | Verify consumer mocks against provider tests, or check whether provider tests still support consumers. |
| [Contract coverage rules](contract-coverage-rules.md) | Understand condition keys, counts, evidence quality and measurement limits. |
| [Bundle format](bundle-evidence.md) | Parse API inventories, test recordings, contracts and payloads. |
| [Machine-readable evidence and coverage](machine-interface.md) | Consume versioned reports, stable identities, provenance, provider mappings and payload availability. |
| [Architecture](architecture.md) | Understand runtime, SDK, proxy, storage and reporting responsibilities. |
| [gRPC user guide](grpc-user-guide.md) | Use reflection/local contracts and existing clients for unary and streaming capture. |
| [gRPC support](grpc-support-plan.md) | Review supported scope, verification and protocol limits. |

## Product scope

Wiretap captures and exports evidence from existing tests and checks concrete
consumer/provider examples offline. It does not generate or execute tests, or
claim agreement beyond the supplied examples. See the
[product scope and completion criteria](roadmap.md).

A run contains test attempts. Each attempt contains exchanges or RPC messages.
A contract condition is a specified case, such as an enum value or an omitted
optional field; several conditions can be covered by one message.
