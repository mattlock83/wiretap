# Implementation handoff: standard test traces

Status: **historical handoff; tracing has been implemented.** The Postman
references below describe an export that has since been removed.
Prepared: 2026-09-27  
Repository root: the `phrond/` repository containing `go.mod`, `cmd/`, and `sdk/`
(in this workspace: `/Users/matt/Source/phrond/phrond`, not its parent directory).

Read [the decision](otel-test-tracing.md) for the target contract and
[adoption notes](otel-adoption.md) for the user experience. This checklist is
intended to let an implementation model work one bounded step at a time without
reconstructing the conversation. Check repository status/instructions first;
preserve unrelated changes. No runtime code was changed when these docs were added.

## Task brief to give an implementation model

```text
Implement the next unchecked step in docs/otel-implementation-plan.md, following
docs/otel-test-tracing.md. Keep the user's existing HTTP clients, SUT, and mocks.
Remove X-Phrond-Trace-Id; use standard W3C context and one root trace per test
attempt. Keep payload capture and existing validation/exports. Do not require
application-specific propagation or a collector. Separate test ownership from
request parentage; never fabricate a parent from a shared trace ID. Do not claim
parallel support until session routing and SDK per-test context are tested.
Use the file/function map and acceptance cases below. Do not start unrelated
refactors, an OTLP collector, a tracing UI product, or SUT/mock orchestration.
Update the checklist only for verified completed work, report tests and remaining
work, and leave public docs explicit about any unfinished adapter support.
```

For a complete implementation, work through steps 1–7 in order. Prefer one step
per model task with the previous changes available. Steps 4–6 finish the adoption
experience; they are not optional merely because core Go tests pass.

## Non-negotiable requirements

1. Each test attempt/retry gets a unique root trace and an independent session.
2. Existing HTTP clients remain supported through test-side instrumentation or
   transport configuration. `phrond.http` is optional.
3. Preserve valid standard context and sampling flags. Use standard OTel parsers,
   context managers, and instrumentation instead of another propagation format.
4. Tests supply standard context; SUT propagation is reused when already present.
   Mocks need no instrumentation. No production Phrond-specific header handling.
5. Basic operation works without span export or a collector and with sampled=0.
6. Resolve trace ownership before selecting the dependency upstream; bind ownership
   at request ingress. Never resolve a completed request against the next test.
7. Trace equality identifies test membership, not an arbitrary inbound parent.
8. Serial/isolated fallback, existing schema rules, full forwarded bytes, bounded
   capture, errors, and all current exports continue working.
9. Remove live use of the custom trace header; keep historical artifacts readable.
10. Mark incomplete capture and ambiguity honestly; do not silently guess.

## Current implementation map

| Area | Files and entry points | Current limitation |
| --- | --- | --- |
| Proxy | `internal/proxy/proxy.go`: `HTTPExchange`, `nextTraceID`, `handleHTTP`, `proxyTLSRequests` | Generates custom IDs; MITM has separate logic. |
| Sessions | `internal/app/app.go`, `runtime_sessions.go`: `runtimeSession`, `resolveDependencyRoute`, `recordProxyForSession` | Header/path/default ownership only; default can be selected after response completion. |
| Runtime API | `internal/app/api.go`: `setupTestRequest`, `handleSetupTest`, `handleTeardownTest`, `handleKillServer` | No root context; teardown removes immediately; setup changes a shared reverse target. |
| Proxy wiring | `internal/app/components.go`, `proxy_recorders.go` | Resolver/recorder contracts need ingress ownership and drain coordination. |
| Recorder | `internal/app/recorder.go`: case state, `FinishCase`, `RecordHTTP`, `linkCausality` | `map[traceID]oneInbound` overwrites candidates; timing picks latest overlapping request. |
| Artifact | `internal/artifact/types.go`, `docs/artifact-openapi.yaml` | Flow trace/parent fields exist; test root and correlation evidence do not. |
| JS SDK | `sdk/js/src/index.js`: `Runtime`, `setupTest`, `teardownTest`, `_httpRequest`, `bindTest`, `createNodeTestSuite` | One `activeSession`; no OTel; built-in Node runner only. |
| Reports | `internal/coverage/report.go`, `workflow.go`, `postman.go` | Carry new metadata; distinguish evidence from executable replay headers. |
| Dependencies | `go.mod`, `sdk/js/package.json` | Neither currently includes OTel dependencies. |
| Examples | `examples/basic-rest/`, `examples/demo-apps/` | Some demo services manually copy the custom trace header. |

Search symbols instead of relying on line numbers. Do not interpret the directory
`zigzap/` beside this repository as part of this change.

## Step 1 — standard context and additive artifact fields

- [ ] Add a small shared Go context helper using compatible OTel trace/propagation
  packages. Choose versions compatible with the repository's Go toolchain; do
  not upgrade the toolchain or add a collector dependency just for this change.
- [ ] Implement the optional root `trace_context` registration and artifact fields
  specified in the decision. Validate real nonzero W3C context; keep old artifact
  `trace_id` strings opaque when loading history.
- [ ] Replace `nextTraceID` and custom header reads/writes on all inspected proxy
  paths. Extract existing context first; inject registered root context only when
  a test-to-SUT request's owner is known and context is missing/invalid. Preserve
  valid context. Do not inject a test trace on the dependency leg to manufacture
  propagation. Ownership evidence is determined before any header injection.
- [ ] Capture sender span ID accurately as `request_span_id`; do not manufacture
  missing SUT ancestry or call this the root test span for every request.
- [ ] Keep optional session selection, stripping its header before forwarding.
  The removed trace header must never be generated or used for correlation.
- [ ] Test valid/missing/malformed/all-zero context, tracestate preservation,
  sampled=0, legacy artifact loading, and absence of custom-header behavior.

Finish session-dependent integration in step 2; avoid claiming usable shared-SUT
parallel tracing at this intermediate point.

## Step 2 — route and retain traffic under the correct session

- [ ] Store root context in `runtimeSession`; register a synchronized trace-to-session
  index in setup before forwarding any test traffic. Duplicate/reused trace IDs
  in this runtime return 409; bad registration returns 400.
- [ ] Keep serial implicit sessions and explicit isolation semantics. Shared-runtime
  parallel adapters use explicit sessions. Traced sessions return stable dependency
  gateway URLs; untraced explicit sessions keep session-scoped URLs.
- [ ] Resolve header/path/trace selectors before dependency upstream lookup. Conflicts
  and ambiguous ownership return 409 before contacting any upstream. A default
  fallback is allowed only in the single-session serial/isolated case.
- [ ] Bind ownership for every captured request at ingress, including unlabeled
  serial requests. Completion must use that binding, not current default state.
- [ ] Audit the second recorder in `ProxyFanoutRecorder`: legacy `a.store` currently
  receives every exchange independently. Reject mixing `/v1/testcases/start` with
  runtime sessions; bind legacy case ownership at ingress as well. Preserve legacy
  isolated capture without duplicate runtime writes or cross-case late traffic.
- [ ] Use the same context/ownership logic on HTTP and decrypted MITM paths without
  changing unrelated transport behavior. HTTPS through a reverse/dependency route
  remains supported; do not claim inspection of opaque CONNECT passthrough.
- [ ] Reject reverse-target changes while sessions are active, including through
  alternative configuration endpoints. Parallel sessions on one runtime must use
  the same SUT target; separate targets use separate runtimes.
- [ ] On teardown, close admission and drain already accepted exchanges. Recommended
  contract: optional `drain_timeout_ms`, default 5000, nonnegative; 0 means no wait.
  On timeout, write/return an explicit incomplete-capture diagnostic and fail the
  attempt. Late completion must not mutate a finalized artifact or another test.
- [ ] Retire trace IDs at close until runtime shutdown so stale trace traffic cannot
  fall into a new implicit session. Shutdown clears live and retired indices and
  waits/cleans up consistently. Do not retain completed payloads in the index.
- [ ] Test two overlapping sessions with one SUT and one stable dependency URL.
  Give each session distinct dependency upstreams/responses to prove routing occurs
  before forwarding, not just when grouping artifacts.
- [ ] Test conflicting selectors, duplicate roots, unknown/closed traces, target
  conflicts, slow response versus teardown, drain timeout, and shutdown cleanup.

## Step 3 — truthful correlation and reports

- [ ] Replace the single-parent trace map with candidate sets. Apply the decision's
  `trace_unique` / `timing` / `ambiguous` / `unmatched` rules; omit `parent_flow_id`
  for ambiguous/unmatched cases. Keep test ownership separately recorded from
  original ingress evidence. A parent cannot start after the dependency call,
  and injected context must never masquerade as SUT propagation.
- [ ] Carry test trace/root and flow context/correlation through artifact readers,
  report samples, HTML details, Postman metadata, and workflow metadata. Use
  descriptive labels; a shared trace must not be presented as a proven causal tree.
- [ ] Preserve raw captured headers subject to redaction. Remove stale trace/session
  headers from executable Postman/workflow request headers, not evidence metadata.
- [ ] Ensure trace/span IDs do not affect flow fingerprints, operation coverage,
  or grouping of schema operations. Preserve test attempt IDs in samples even
  if aggregate reports group by test name.
- [ ] Test multiple concurrent inbound requests in the same test trace, a unique
  timing candidate, multiple timing candidates, no candidate, a dependency that
  predates the only inbound request, injected-context evidence, and legacy round trips.
- [ ] Check existing schema validation/assertions on both channels still report
  their owning flow and test correctly.

## Step 4 — existing Node SDK and ordinary client integration

- [ ] Add standard OTel context/trace dependencies and a setup entry point. Initialize
  a valid provider/context manager without requiring export; reuse an existing
  configured provider when supported. Do not replace a user's provider or double
  instrument clients. Verify actual versions instead of copying old API examples.
- [ ] Replace singleton per-test state (`activeSession`, pending scenario/session
  selection) with asynchronous per-test context. Suite configuration stays shared.
  Audit all uses: HTTP helpers, assertions, dependency URLs, teardown and generation.
  Retain unambiguous serial compatibility for manual lifecycle callers.
- [ ] Extend `bindTest`: new root for each attempt, setup/register, callback in active
  context, final outcome including teardown validation, finally close/end. Test
  errors retain priority over cleanup errors. A retry gets a fresh trace/session.
- [ ] Document and implement serial versus shared-parallel adapter configuration.
  Default to serial/isolated capture; parallel mode must not create an implicit
  default session. Keep a root trace even in serial fallback mode.
- [ ] Support the existing helper and ordinary Node `http`/`https` clients plus
  Node `fetch` via the appropriate standard instrumentation. Allow supported
  higher-level clients to use those stacks. Test and publish the actual matrix;
  don't assume all libraries, loaders, or versions are instrumented automatically.
- [ ] Initialize instrumentation at the required time, before affected modules load.
  Do not trace Phrond control API calls as SUT exchanges or instrument proxy-to-SUT
  forwarding twice. Keep tracing client setup separate from routing configuration.
- [ ] Test overlapping async callbacks and requests with a shared ordinary client,
  assertions in each session, missing active context, failures, teardown failure,
  sampled=0, and no exporter. Prove no cross-test state leakage.
- [ ] Ensure one suite's close cannot kill sessions belonging to another active suite
  sharing a runtime. Either manage ownership/reference counting or require a single
  shared lifecycle owner and enforce/document that arrangement.

## Step 5 — Jest adoption adapter

- [ ] Add a small tested Jest adapter/setup entry point built on step 4. Retain
  ordinary client calls and assertion libraries. A thin test wrapper is acceptable;
  do not promise an untested zero-edit runner hook.
- [ ] Support normal and concurrent tests, table-driven test names, failures, and
  retries with one context/session per attempt. Skipped tests create no live session.
  Specify lifecycle ownership across Jest workers; one worker finishing must not
  stop Phrond while others are active.
- [ ] Add an integration example using an existing client (not `phrond.http`) and
  existing mock server. Exercise at least two concurrent tests on a shared SUT
  with standard propagation and separate assertions/artifacts.
- [ ] Publish verified loading/setup instructions and the tested CommonJS/ESM/Jest
  configurations. Do not imply support for untested module-loading modes.

## Step 6 — small Go test helper

- [ ] Add a small Go-facing test package under `sdk/go/` using the existing module.
  Reuse the control API; do not expose/import backend internals to the consumer.
- [ ] Start one root/session per attempt and return its `context.Context`. Keep the
  user's standard `http.Client`: wrap its existing `RoundTripper` with standard
  `otelhttp` propagation, preserving timeouts, TLS, cookies and other configuration.
- [ ] Provide/document requests carrying the test context; if adding a per-test
  bound transport, clone requests/context safely and never mutate a shared client
  or transport while tests are running. No global current-test variable.
- [ ] Cleanup via `t.Cleanup` must record the final test outcome, surface teardown
  validation failures, and end the root. Keep the runtime alive across concurrent
  tests and stop it only from the owner after all tests finish.
- [ ] Add an `httptest.NewServer` example and a `t.Parallel` integration test using
  a normal client. Clearly document that direct `NewRecorder` handler calls don't
  traverse the proxy. Prove uninstrumented SUTs still work in isolated/serial mode.

## Step 7 — migration, examples, and release verification

- [ ] Replace demo service custom-header copying with standard SUT instrumentation
  where tracing is demonstrated. Include an uninstrumented fallback example.
  Do not add a new Phrond-specific propagation hook to the demos.
- [ ] Update README, SDK README, runtime API, getting-started, troubleshooting,
  examples and artifact schema. Turn planned adoption instructions into runnable,
  tested instructions only when the corresponding feature exists.
- [ ] Announce removal of the custom header and explain fallback for integrations
  that used it. Remove stale live custom-header instructions; retain only migration,
  historical-artifact fixtures, and negative tests where appropriate.
- [ ] Run the relevant checks below. Update this checklist and the documents' status
  truthfully; list any unfinished adapters or limitations in the handoff summary.

## Acceptance scenarios that must not be omitted

| Scenario | Required result |
| --- | --- |
| Two traced tests share SUT and stable dependency URL | Correct upstream, payloads, assertions and artifact for each test; no custom SUT code. |
| One test issues two overlapping SUT requests | One trace/test grouping; no last-request-wins parent assignment. |
| Ordinary client, existing mock, no collector/exporter | Both directions captured with configured body limits and trace association. |
| Valid unsampled context with tracestate | Correlation/capture works and incoming context is preserved. |
| No SUT propagation | Serial/isolated fallback works; ambiguous parallel traffic is diagnosed, not guessed. |
| A request ends while A tears down and B starts | No traffic is recorded under B; drain/incomplete outcome is explicit. |
| Session URL/header disagrees with trace | Fail before upstream selection/forwarding. |
| Retry or repeated test name | Fresh trace/session; distinct artifacts with the correct outcomes. |
| Old YAML and new tracing metadata | Old artifacts load; new metadata survives report conversion. |
| Exported contracts/replay requests | No ephemeral tracing IDs become contract requirements or stale replay context. |

## Verification commands

Run from the repository root. Use focused new tests while implementing; after
the relevant steps, run these checks once unless changes/failures justify a rerun:

```sh
go test ./internal/proxy ./internal/app ./internal/artifact ./internal/coverage
npm --prefix sdk/js test
go test -race ./internal/proxy ./internal/app
go test ./...
go vet ./...
./examples/run-basic-rest-e2e.sh
```

Wire Jest tests into the SDK test scripts so `npm --prefix sdk/js test` covers the
shipped adapter, and include new Go SDK packages in their own race-enabled checks.
The basic example starts its own local processes; check port/runtime prerequisites.
Also use `git diff --check` and check all new Markdown links.

Existing regression anchors: `internal/app/dependency_capture_test.go`,
`internal/proxy/proxy_test.go`, `internal/app/runtime_report_test.go`,
`internal/artifact/yaml_test.go`, `internal/coverage/*_test.go`, and
`sdk/js/test/harness.test.js`. Extend meaningful behavior tests; avoid tests that
only assert the presence of a new field without proving attribution or forwarding.
