# Wiretap architecture

Wiretap captures HTTP exchanges during existing Jest tests. Jest owns execution, fixtures, assertions and outcomes. Its adapter manages the local runtime and optionally hosts an HTTP handler inside Jest. Wiretap forwards traffic, correlates attempts, validates contracts and stores evidence. Validation findings stay separate from test outcomes; capture/setup/cleanup failures still fail integration.

## Components

| Component | Responsibility |
| --- | --- |
| `cmd/wiretap` | Control service, setup checks, reports, exports and offline checks. |
| `sdk/js` | Jest config/environment, lifecycle, final outcomes and trace context. |
| `internal/app` | Run/test sessions, dependency routes, drain and validation. |
| `internal/proxy`, `internal/capture` | HTTP/HTTPS forwarding and bounded retained bodies. |
| `internal/tracecontext` | W3C context parsing and propagation. |
| `internal/artifact` | HTTP capture model and validation. |
| `internal/bundle` | Inventories, immutable attempts, contracts, payloads and integrity. |
| `internal/coverage` | Operation and contract-condition measurement. |
| `internal/htmlreport` | Offline dashboard derived from coverage JSON, specs and Arazzo. |
| `internal/arazzo` | JSON/REST workflow export with portable OpenAPI sources. |
| `internal/assumptions` | Verify Arazzo projections and compare consumer/provider evidence. |
| `internal/runset` | Pin and verify same-build archive groups. |
| `internal/project`, `internal/core` | Project and runtime configuration. |

## A run

1. The Jest suite owner starts Wiretap on allocated local control/capture ports. The adapter hosts its configured app handler, or the project harness hosts the API.
2. Test setup registers an attempt, contracts and fresh trace context before per-test work.
3. The proxy forwards requests/responses and retains a bounded prefix (4 MiB per body by default). Delivery continues beyond that retained limit.
4. Teardown stops new admission, drains accepted exchanges, validates them and commits immutable attempt JSON with final framework outcome.
5. After test files and cleanup finish, one owner finalizes the archive and outputs, then stops its runtime. Coverage, HTML and Arazzo generation share the loaded archive snapshot.

Interrupted runs and pending attempts remain distinguishable. The parent stdin lifeline requests graceful shutdown after owner death; abrupt process termination can still leave unfinished exports or an open archive.

## Correlation

W3C `traceparent`/`tracestate` identify test ownership through the application's server and outbound clients. Session IDs select capture at Wiretap's boundary. Concurrent tests sharing a SUT need trace propagation and fixture isolation. Otherwise use sequential capture or isolated runtimes. A shared trace alone may not establish which inbound request caused a dependency call: inferred relationships are labelled, and ambiguity stays visible. See [tracing](otel-adoption.md).

## Evidence and derived outputs

A run contains `bundle.json`, immutable `tests/`, frozen `contracts/` and retained `payloads/`. Readers verify hashes, capabilities and inventory before coverage, Arazzo export or checks. Preserve the complete directory. Only HTTP/OpenAPI evidence is supported.

The main HTTP API requires OpenAPI unless explicitly omitted. Dependencies can capture without a schema; supplying one enables validation and coverage. Missing contracts produce unknown denominators rather than invented rules. Arazzo export supports eligible JSON/REST traffic and can fail when required capture evidence is incomplete or unrepresentable.

The [assumption checker](assumption-checks.md) verifies Arazzo documents against archives, then compares each selected mock request and paired response with complete passing provider service tests. It uses explicit pairs or same-build run sets. The provider's frozen OpenAPI 3.0 contract can validate schema-free consumer examples. Both contracts additionally permit exercised-rule comparison. Both omitted contracts yield example-only assurance.

Coverage measures declared cases exercised by eligible observations. Checks establish agreement for selected examples and supported rules. Neither establishes arbitrary business behavior or which mock response fields the application consumed. See [coverage rules](contract-coverage-rules.md), [check limits](assumption-checks.md#what-the-check-will-not-establish) and [support](support.md).

## Jest lifecycle ownership

`withWiretap` manages one runtime per Jest project. The owner waits for every
worker and cleanup hook before finalizing. The [conformance scenarios](../sdk/conformance/scenarios.json)
and [SDK tests](../sdk/js/test/) cover receipt validation and lifecycle errors.

| Phase | Suite owner | Jest environment |
| --- | --- | --- |
| Startup | Start the local executable; validate private readiness from that child, its loopback address and run identity. | Connect to the owner's runtime. |
| Attempt | Keep the runtime alive through worker and fixture cleanup. | Start before test hooks; preserve stable identity across retries and use a fresh trace. |
| Outcome | Preserve Jest's unsuccessful exit. | Record the final Jest outcome after test cleanup, including hook failures, retries and timeouts. |
| Completion | Finalize once; validate matching run ID, archive and complete status; retain partial export paths on error. | Await teardown and report capture errors alongside any existing test failure. |
| Shutdown | Close the parent lifeline, wait for exit, fail on unsuccessful shutdown or forced kill, and retain bounded logs. | Await spawned work before ending the attempt. |

Go resolves project defaults; Jest sends explicit overrides. Omitted settings
inherit, Jest `dependencies: {}` and `schemas: []` clear their defaults, and
`schemaRequired: false` permits an omitted SUT contract. Invalid supplied
contracts fail setup. Contract violations remain separate from framework outcomes; capture, teardown and
export failures surface as integration errors. When cleanup also fails, Jest
keeps the original assertion failure and reports both errors. A skipped test
that never starts creates no attempt.

Canonical receipts and bounded logs belong inside each archive. The latest
receipt in the output parent is advisory when runs overlap. Runtime/version
fields are documented in the [runtime API](runtime-api.md) and
[compatibility policy](support.md#format-compatibility).
