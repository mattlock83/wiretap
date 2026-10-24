# Wiretap documentation

Your test framework owns execution and assertions. SDK or HTTP lifecycle calls
identify the running test and its outcome. Wiretap captures HTTP/native gRPC API
boundaries, validates traffic against supplied contracts, and writes portable
JSON artifacts. JSON/YAML reports calculate operation and contract coverage; a
text summary gives the first view of results. Offline assumption checks compare
captured mocks with provider test examples. Use isolated tests with synthetic
data and dummy secrets: retained traffic is unchanged.

## Which specs do I need?

| Boundary | Default | Where its contract comes from |
| --- | --- | --- |
| Main HTTP API under test | OpenAPI required | The SUT's own test project. |
| Consumed HTTP API or mock | OpenAPI optional | Supply a dependency spec for capture-time validation and coverage, or use the provider's frozen spec during a later assumption check. |
| Native gRPC, provided or consumed | Protobuf descriptors required | Server reflection, local `.proto` files or a descriptor set. |

A consumer can capture HTTP mock assumptions without copying provider specs.
The later check still needs matching complete passing provider tests; a spec
alone cannot establish coverage. Supplying both HTTP contracts additionally
checks their declared rules for drift. See [schema defaults](configuration.md#schema-defaults-by-boundary)
and the [shared snapshot workflow](assumption-checks.md#share-provider-snapshots).

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
| [Assumption checks](assumption-checks.md) | Verify consumer examples and exercised HTTP/Protobuf declarations against provider tests, or check whether provider tests still support consumers. |
| [Share provider snapshots](assumption-checks.md#share-provider-snapshots) | Organize complete runs by application, declare provider mappings and discover comparisons with `--artifacts`. |
| [Contract coverage rules](contract-coverage-rules.md) | Understand condition keys, counts, evidence quality and measurement limits. |
| [Bundle format](bundle-evidence.md) | Parse API inventories, test recordings, contracts and payloads. |
| [Machine-readable evidence and coverage](machine-interface.md) | Consume versioned reports, stable identities, provenance, provider mappings and payload availability. |
| [Architecture](architecture.md) | Understand runtime, SDK, proxy, storage and reporting responsibilities. |
| [gRPC user guide](grpc-user-guide.md) | Use reflection/local contracts and existing clients for unary and streaming capture. |
| [gRPC support](grpc-support-plan.md) | Review supported scope, verification and protocol limits. |

For assumption checks, start with the
[shared application workflow](assumption-checks.md#check-against-shared-application-artifacts),
then read [issues the check will find](assumption-checks.md#issues-the-check-will-find),
[what the check will not establish](assumption-checks.md#what-the-check-will-not-establish)
and [interpreting results](assumption-checks.md#interpreting-results). The
[CI guide](ci-example.md#gate-consumerprovider-assumptions-offline) shows how to
gate on the exit status while preserving reports and diagnostics;
[troubleshooting](troubleshooting.md#assumption-checks) explains common fixture,
collection and evidence failures.

## Product scope

Wiretap captures and exports evidence from existing tests and checks concrete
consumer/provider examples and exercised contract rules offline. It does
not generate or execute tests, or claim arbitrary behavioral agreement. See the
[product scope and completion criteria](roadmap.md).

A run contains test attempts. Each attempt contains exchanges or RPC messages.
A contract condition is a specified case, such as an enum value or an omitted
optional field; several conditions can be covered by one message.
