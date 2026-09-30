# Wiretap architecture

Wiretap captures HTTP exchanges during existing tests and turns them into a
portable record of tested API behaviour and observed dependency assumptions.
In capture mode, the test harness owns the application, mocks, test execution,
and assertions. Wiretap owns capture, correlation, schema validation, and the
resulting bundle. The optional exploration framework owns generated provider
cases and invokes external project commands for fixtures and business checks.

## Components

| Component | Responsibility |
| --- | --- |
| `cmd/wiretap` | Host the HTTP runtime; run exploration; offline report/export and CA utilities. |
| `internal/explore`, `internal/hooks` | Generate isolated invalid provider inputs, replay sequences, execute external lifecycle commands and persist checks. |
| `sdk/js`, `sdk/go` | Test lifecycle helpers, standard trace context, and capture locations. JavaScript includes Node and Jest adapters. |
| `internal/app` | Run and test sessions, dependency routes, draining, and schema validation. |
| `internal/proxy`, `internal/capture` | Forward traffic and retain bounded request/response evidence. |
| `internal/tracecontext` | W3C context parsing and propagation. |
| `internal/redact` | Apply configured redaction before persistence. |
| `internal/artifact` | In-memory capture model shared by recording, storage, and reporting. |
| `internal/bundle` | Authoritative run inventory, immutable attempt JSON, frozen schemas, payload attachments, and integrity checks. |
| `internal/coverage` | Calculate operation and contract-condition coverage from verified evidence; generate diagnostic exports. |
| `internal/bundlereport` | Render OKF Markdown and the offline HTML dashboard from verified bundle evidence. |
| `internal/project` | Discover and resolve `.wiretap.yaml` once for the runtime. |
| `internal/core` | Runtime configuration and component lifecycle. |

Capture start/finalization is controlled through the HTTP API. SDKs are libraries
called from existing test frameworks. Capture has one session map, admission
path and drain procedure. Exploration is a separate CLI runner using the same
bundle writer, coverage calculation and report renderer.

The runtime exposes one control listener and one capture listener. The capture
listener forwards inbound calls to the configured SUT and dependency calls to
their declared upstreams. One runtime has one reverse SUT target. The
[HTTP API](runtime-api.md) is usable directly; SDKs make lifecycle integration
convenient without requiring a special HTTP client.

## A run

1. The existing harness starts Wiretap, the SUT, and mocks or dependencies.
   Test framework hooks call the SDK/HTTP API to start capture. Test calls and
   selected dependency endpoints are routed through Wiretap.
2. Capture startup creates a run bundle. Each test setup registers a named
   attempt, its schemas, and a fresh standard trace context.
3. The proxy forwards requests and responses while retaining bounded evidence.
   Test-side instrumentation propagates the attempt context through ordinary
   HTTP clients. The application can continue using its existing OTel setup.
4. Teardown stops admitting new work for the attempt and drains accepted
   exchanges. Wiretap records the test outcome, validation results, and any
   incomplete-capture diagnostics, then commits an immutable JSON attempt.
5. The runtime refreshes the OKF pages and HTML dashboard. After all attempts
   finish, the suite-level owner closes the run through the HTTP API. Interrupted and pending work
   remains distinguishable from completed capture.

The default drain timeout is five seconds. Request and response delivery is
streamed; the default retained prefix is 4 MiB per body. Payloads are redacted
before persistence. Generated views preview at most 4 KiB and link to retained
attachments. These limits describe evidence completeness, not delivery limits.

## Correlation and adoption

A test attempt is the root of a trace. W3C `traceparent`/`tracestate` provide
context across the SUT when its server and outbound clients propagate them.
Wiretap's session IDs select capture sessions at the control/proxy boundary;
they do not require application-specific propagation.

With working propagation, parallel tests can share the SUT and dependency
routes. Application state and mock isolation remain the harness's responsibility.
Without it, sequential capture uses the one active test session. Independent
worker runtimes and SUTs provide another isolation option. Late background work
must be drained or isolated even when tests run sequentially.

A trace establishes test ownership, but may not establish which inbound request
caused a downstream call. Unique-candidate and timing relationships are marked
as inferences; ambiguous relationships are left unset. See
[tracing adoption](otel-adoption.md) for supported runners and client setup.

## Evidence and views

The bundle contains versioned JSON evidence, frozen OpenAPI documents and their
references, and retained payload attachments. Its inventory records hashes and
attempt identity. Readers verify evidence before calculating coverage or
rebuilding views. Copy the complete bundle to preserve that evidence graph.

OKF pages are browsable documentation for humans and agents. The HTML dashboard
is a static, offline view over the same evidence and coverage calculation.
The same package generates one observed OpenAPI draft per mock dependency,
linked from both views. These drafts are derived from verified captures, never
from supplied contracts or previous drafts, and are not enforcement inputs.
None of these views is an input to coverage or exports; all can be rebuilt without the
original workspace or running application. There is one HTML renderer, owned by
`internal/bundlereport`.

OpenAPI schemas are required by default. Each API may explicitly permit an
absent spec with `schema_required: false`; omission is persisted and displayed
as skipped validation with unknown coverage. Supplied specs always validate.
Reports and exports read only verified bundle evidence.

The maintained format contracts are [bundle evidence](bundle-evidence.md) and
the [OKF profile](okf-profile.md). The [bundle guide](okf-bundles.md) describes
viewing, rebuilding, and exports.

## Meaning and limits

Observed operation coverage describes which declared operations were exercised,
including whether tests passed and schema validation succeeded. It is not code
coverage or proof that every behaviour has been tested. Dependency observations
show what the upstream returned in a test, with declared mock/service provenance.
They do not prove provider agreement or identify which response fields the SUT
consumed. Stable API identities and source revisions prepare the evidence for a
future cross-repository comparator; that comparison is not implemented.

Contract-condition coverage additionally derives a finite catalogue from each
supplied contract, records individual observations and per-message combinations,
and separates provider from mock/service provenance. The versioned rules and
current support limits are in [contract coverage rules](contract-coverage-rules.md).
Its generated JSON catalogue, Markdown page and HTML tab use the same calculation.
Ordinary capture validation remains strict. Exploration explicitly records the
intended invalid request and asserted rejection in a separate coverage catalogue.

`wiretap explore` verifies a source bundle, selects a passing provider attempt,
runs a fresh baseline, then replays prefixes with one invalid request at a time.
It records provider exchanges directly and does not capture downstream traffic.
Project-owned hooks exchange JSON through fresh command processes; code, runtime
bindings and fixture state are never copied into the output bundle. See
[exploration](exploration.md) for lifecycle, checks and current limits.

Broader transport support (streaming, WebSocket, HTTP/2, and RPC-specific
capture) remains future work. Current guarantees centre on bounded HTTP
request/response evidence, with HTTPS trust configured as described in the
[trust guide](https-trust.md).
