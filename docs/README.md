# Wiretap documentation

Your test framework owns execution and assertions. SDK or HTTP lifecycle calls tell
Wiretap which test is running and its outcome. The proxies capture system boundaries,
validate traffic against supplied specs, and produce a portable OKF bundle of API
evidence, dependency assumptions and coverage.

## Set up capture

| Guide | Use it to |
| --- | --- |
| [Capture existing tests](getting-started-existing-tests.md) | Configure boundaries, connect lifecycle hooks and run your first capture. |
| [Runnable examples](../examples/README.md) | Try HTTP and gRPC capture, database fixtures or Microsvc Transactions. |
| [Configuration and commands](configuration.md) | Configure `.wiretap.yaml`, runtime ownership, specs and CLI commands. |
| [JavaScript SDK](../sdk/js/README.md) | Integrate native hooks or the optional Node/Jest adapters. |
| [Go SDK](../sdk/go/README.md) | Integrate `testing`, existing HTTP clients and suite cleanup. |
| [Runtime HTTP API](runtime-api.md) | Integrate another language or test framework directly. |
| [Tracing and concurrency](otel-adoption.md) | Reuse OTel and choose shared, sequential or isolated capture. |
| [HTTPS trust](https-trust.md) | Configure the proxy CA for decrypted HTTPS capture. |
| [CI example](ci-example.md) | Run the examples and preserve complete bundles in CI. |
| [Troubleshooting](troubleshooting.md) | Resolve routing, tracing, schema and report problems. |

## Read and extend the results

| Guide | Use it to |
| --- | --- |
| [Bundles and reports](okf-bundles.md) | Read the viewer, distinguish coverage from test runs, rebuild views and export results. |
| [Contract coverage rules](contract-coverage-rules.md) | Understand condition keys, counts, evidence quality and measurement limits. |
| [Bundle knowledge](bundle-knowledge.md) | Load API evidence and verification tasks into a knowledge base or agent. |

## Format and implementation references

| Reference | Scope |
| --- | --- |
| [Application knowledge bundle specification](spec/README.md) | Draft requirements, schemas and examples for stable repository bundles and independent producers/consumers; not yet implemented by Wiretap. |
| [Bundle format](bundle-evidence.md) | Parse API dependencies, recorded tests, contracts and payloads; understand recording structure and integrity. |
| [OKF profile](okf-profile.md) | Generated Markdown, frontmatter and ownership of bundle paths. |
| [Architecture](architecture.md) | Runtime, SDK, proxy, storage and reporting responsibilities. |

## gRPC

| Plan | Scope |
| --- | --- |
| [gRPC implementation plan](grpc-support-plan.md) | Native gRPC support, verified scope and remaining work. |
| [gRPC user guide](grpc-user-guide.md) | Reflection by default, local contracts, existing clients and test lifecycle. |

## Product direction

The [product roadmap](roadmap.md) describes future assumption verification,
a public bundle registry and private registries. Future execution, generated
checks and interactive workflows belong to the Hub; local Wiretap focuses on
proxies, portable bundles and spec-based reports.

The [contract package plan](contract-package-plan.md) proposes application and API
declarations in `.wiretap.yaml`, local/remote spec sources, versioning, consumer
aliases and codegen variants. It includes provider-only, consumer-only and mixed
projects. Its example syntax is not implemented yet.

A **run bundle** contains **test attempts** (shown as *Test runs* in the viewer).
Each attempt contains request/response **exchanges**. A contract **condition** is
one specified case, such as an enum value or an omitted optional field; several
conditions can be covered by the same exchange.
