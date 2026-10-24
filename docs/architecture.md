# Wiretap architecture

Wiretap captures HTTP and native gRPC exchanges during existing tests. The test
framework owns execution, outcomes, fixtures and assertions. Managed SDK
integrations own their Wiretap process; project-owned harnesses can use the
control API directly. Existing SUT/mock processes stay with the harness unless
an SDK adapter is configured to host an HTTP handler.
Wiretap owns forwarding, correlation, contract validation and raw capture storage.
Framework outcomes remain independent of validation findings. Configuration and
incomplete-capture errors still fail integration setup or cleanup.

## Components

| Component | Responsibility |
| --- | --- |
| `cmd/wiretap` | Host the control service; offline coverage/Postman exports and CA utilities. |
| `sdk/js`, `sdk/go` | Runner integration, owned or external runtime lifecycle, standard trace context and artifact locations. |
| `internal/app` | Run/test sessions, dependency routes, draining and schema validation. |
| `internal/proxy`, `internal/capture` | Forward traffic and retain bounded evidence. |
| `internal/tracecontext` | W3C context parsing and propagation. |
| `internal/artifact` | Shared in-memory capture model. |
| `internal/bundle` | Run inventory, immutable attempt JSON, frozen contracts, payloads and integrity checks. |
| `internal/coverage` | Operation/contract-condition coverage from verified evidence. |
| `internal/postman` | HTTP v2.1 and native gRPC v3 collection exports. |
| `internal/project` | Discover and resolve `.wiretap.yaml` for the runtime. |
| `internal/core` | Runtime configuration and component lifecycle. |

Capture has one session map, admission path and drain procedure. The runtime
exposes a control listener and capture listeners. It forwards inbound calls to
one configured SUT and dependency calls to declared upstreams. The
[HTTP API](runtime-api.md) works directly; SDKs integrate with existing frameworks
without replacing their HTTP/gRPC clients.

## A run

1. The managed SDK adapter or project harness starts Wiretap. The harness keeps
   its SUT/mock startup, or an SDK hosts the supplied HTTP handler. Capture
   listeners provide the addresses used by test and application clients.
2. Test setup registers a named attempt, contracts and a fresh trace context.
3. Proxies forward requests and responses while retaining bounded evidence.
4. Teardown stops admission, drains accepted exchanges, validates messages and
   commits the immutable attempt JSON with its outcome and capture diagnostics.
5. After all attempts and their cleanup finish, one suite owner finalizes the run
   and configured exports. A managed owner then stops its process. Interrupted
   and pending work remains distinguishable from completed capture.

The default drain timeout is five seconds. HTTP request/response delivery streams
through the proxy; the default retained prefix is 4 MiB per body. gRPC observation
is incremental with byte/message limits. Retained payloads are unchanged; the
runtime is for isolated tests with synthetic data and dummy secrets.
These are evidence limits rather than limits on delivered traffic.

## Correlation and adoption

A test attempt is the root of a trace. W3C `traceparent`/`tracestate` carry context
across the SUT when its server and outbound clients propagate them. Wiretap
session IDs select capture sessions at its boundary; the application does not
need to propagate a special identifier.

Parallel tests can share a SUT and dependency routes with working trace
propagation. The harness still owns state/mock isolation. Without propagation,
use sequential capture with one active test or isolated workers/runtimes. Drain
or isolate late background work. A trace identifies the owning test but may not
establish which inbound request caused an outbound call; inferred relationships
are labelled and ambiguous relationships remain unset. See
[tracing adoption](otel-adoption.md).

## Artifacts and reports

A run directory contains versioned JSON evidence, frozen OpenAPI documents or
Protobuf descriptor sets, and retained payload attachments. `bundle.json` records
attempt references, inventory, source provenance, run state and integrity hashes.
Readers verify these inputs before calculating coverage or exporting Postman
collections. Copy the complete directory to preserve the evidence graph.

`wiretap report` and `wiretap export` default to JSON coverage and support YAML
and Postman output; `--format text` gives a compact coverage summary. They run
offline without the original workspace or servers.
`POST /v1/report` exposes JSON coverage through the control API. See the
[format reference](bundle-evidence.md) and [artifact/export guide](artifacts.md).

OpenAPI is required for the main HTTP API under test by default; its explicit
`schema_required: false` opt-out remains available. HTTP dependencies require no
spec unless `schema_required: true` is set. Adding one opts into validation and
contract coverage; an omitted one records skipped validation and an unknown
denominator. Supplied specs always validate. gRPC requires descriptors from
reflection, local sources or descriptor sets and cannot opt out.

## Meaning and limits

Coverage records which declared operations and finite supported conditions had
qualifying evidence. Conditions observed together remain linked per message;
this does not claim exhaustive combinations. Failed/incomplete captures and
unsupported semantics remain visible. See [coverage rules](contract-coverage-rules.md).

Dependency records retain mock/service provenance. They show what the target
returned during a test, without proving provider agreement or identifying which
fields the SUT consumed. Provider mappings retain the intended cross-project
identity without claiming agreement. The offline
[assumption checker](assumption-checks.md) verifies exported collections against
their archives and compares selected mock scenarios with complete passing
provider service tests. It accepts explicit pairs or discovers mapped peers
from a local artifact root grouped by application ID, requiring unique selected
snapshots rather than merging run history. It requires request and response agreement within the
same exchange and reports missing evidence as a failure. The provider's frozen
OpenAPI 3.0 contract can validate both exchanges without a consumer dependency
spec. With both contracts present, it additionally compares exercised rules
and validates each exchange under its own schema. Native gRPC cross-decodes
observed messages with opposite descriptors and compares the selected method's
reachable message and enum declarations. HTTP with both schemas absent can
only earn a marked example-only match.
Wiretap does not execute or generate tests.

HTTP and native gRPC support capture, validation and coverage, including all four
RPC shapes. Connect wire format, gRPC-Web, messaging and GraphQL capture adapters
are not supported. See the [gRPC support record](grpc-support-plan.md) and
[HTTPS trust guide](https-trust.md).
