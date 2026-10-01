# Wiretap documentation

Wiretap has two workflows:

- **Capture existing tests.** Your test framework owns execution. SDK or HTTP
  lifecycle calls tell the Wiretap proxy which test is running and its outcome.
- **Explore a captured scenario.** The optional `wiretap explore` runner executes
  generated invalid-input checks, using project-owned hooks for fixtures and
  authentication. It writes a new bundle.

Both produce the same portable bundle of API evidence, dependency assumptions
and coverage. Start with capture, then add exploration when you have a suitable
happy-path scenario.

## Set up capture

| Guide | Use it to |
| --- | --- |
| [Capture existing tests](getting-started-existing-tests.md) | Configure boundaries, connect lifecycle hooks and run your first capture. |
| [Runnable examples](../examples/README.md) | Try HTTP and gRPC capture, database hooks or Microsvc Transactions. |
| [Configuration and commands](configuration.md) | Configure `.wiretap.yaml`, runtime ownership, specs and CLI commands. |
| [JavaScript SDK](../sdk/js/README.md) | Integrate native hooks or the optional Node/Jest adapters. |
| [Go SDK](../sdk/go/README.md) | Integrate `testing`, existing HTTP clients and suite cleanup. |
| [Runtime HTTP API](runtime-api.md) | Integrate another language or test framework directly. |
| [Tracing and concurrency](otel-adoption.md) | Reuse OTel and choose shared, sequential or isolated capture. |
| [HTTPS trust](https-trust.md) | Configure the proxy CA for decrypted HTTPS capture. |
| [CI example](ci-example.md) | Run the examples and preserve complete bundles in CI. |
| [Troubleshooting](troubleshooting.md) | Resolve routing, tracing, schema, replay and report problems. |

## Read and extend the results

| Guide | Use it to |
| --- | --- |
| [Bundles and reports](okf-bundles.md) | Read the viewer, distinguish coverage from test runs, rebuild views and export results. |
| [Contract coverage rules](contract-coverage-rules.md) | Understand condition keys, counts, evidence quality and measurement limits. |
| [Provider exploration and hooks](exploration.md) | Generate rejection tests and supply fixtures, authentication and assertions. |
| [Bundle knowledge](bundle-knowledge.md) | Load API evidence and verification tasks into a knowledge base or agent. |

## Format and implementation references

| Reference | Scope |
| --- | --- |
| [Bundle evidence](bundle-evidence.md) | Authoritative JSON records, identity, integrity and attachments. |
| [OKF profile](okf-profile.md) | Generated Markdown, frontmatter and ownership of bundle paths. |
| [Architecture](architecture.md) | Runtime, SDK, proxy, exploration, storage and reporting responsibilities. |

## gRPC

| Plan | Scope |
| --- | --- |
| [gRPC implementation plan](grpc-support-plan.md) | Design, unary scope and remaining streaming/interoperability work. |
| [gRPC user guide](grpc-user-guide.md) | Reflection by default, local contracts, existing clients and test lifecycle. |

A **run bundle** contains **test attempts** (shown as *Test runs* in the viewer).
Each attempt contains request/response **exchanges**. A contract **condition** is
one specified case, such as an enum value or an omitted optional field; several
conditions can be covered by the same exchange.
