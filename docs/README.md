# Wiretap documentation

Your test framework owns execution and assertions. SDK or HTTP lifecycle calls
identify the running test and its outcome. Wiretap captures HTTP/native gRPC API
boundaries, validates traffic against supplied contracts, and writes portable
JSON artifacts. JSON/YAML reports calculate operation and contract coverage.

## Set up capture

| Guide | Use it to |
| --- | --- |
| [Capture existing tests](getting-started-existing-tests.md) | Configure boundaries, connect lifecycle hooks and run capture. |
| [Runnable examples](../examples/README.md) | Try HTTP/gRPC capture, database fixtures or Microsvc Transactions. |
| [Configuration and commands](configuration.md) | Configure `.wiretap.yaml`, ownership, specs and CLI commands. |
| [Local installation and release preparation](local-release.md) | Build versioned binaries, pack the SDK and test installation in an external project. |
| [JavaScript SDK](../sdk/js/README.md) | Use native hooks or the optional Node/Jest adapters. |
| [Go SDK](../sdk/go/README.md) | Integrate `testing` and existing clients. |
| [Runtime HTTP API](runtime-api.md) | Integrate another language or test framework directly. |
| [Tracing and concurrency](otel-adoption.md) | Reuse OTel and choose shared, sequential or isolated capture. |
| [HTTPS trust](https-trust.md) | Configure the proxy CA for decrypted HTTPS capture. |
| [CI example](ci-example.md) | Run tests and preserve complete artifacts in CI. |
| [Troubleshooting](troubleshooting.md) | Resolve routing, tracing, schema and capture problems. |

## Read and export results

| Guide | Use it to |
| --- | --- |
| [Capture artifacts and exports](artifacts.md) | Read raw artifacts, generate JSON/YAML coverage and export Postman collections. |
| [Contract coverage rules](contract-coverage-rules.md) | Understand condition keys, counts, evidence quality and measurement limits. |
| [Bundle format](bundle-evidence.md) | Parse API inventories, test recordings, contracts and payloads. |
| [Architecture](architecture.md) | Understand runtime, SDK, proxy, storage and reporting responsibilities. |
| [gRPC user guide](grpc-user-guide.md) | Use reflection/local contracts and existing clients for unary and streaming capture. |
| [gRPC support record](grpc-support-plan.md) | Review verified scope and protocol follow-ups. |

## Product direction

The [roadmap](roadmap.md) describes future cross-repository comparison, assumption
verification and Hub registry/execution services. Local Wiretap focuses on
proxies, raw capture artifacts and spec coverage. The
[contract package plan](contract-package-plan.md) proposes richer application/API
declarations, source locking and consumer variants; its syntax is not implemented.

A run contains test attempts. Each attempt contains exchanges or RPC messages.
A contract condition is a specified case, such as an enum value or an omitted
optional field; several conditions can be covered by one message.

[Machine-readable evidence and coverage](machine-interface.md) specifies report
versions, producer/build provenance, logical retry identities, provider mappings
and payload-availability metadata for external analysis tools.
