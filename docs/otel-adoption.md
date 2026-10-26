# Tracing and concurrency

Wiretap uses W3C trace context to associate captured HTTP exchanges with Jest test attempts. Begin with the [Jest adapter](../sdk/js/docs/jest.md). It manages test roots and sends standard context through supported test callbacks; the application must still propagate that context through its server and outbound clients.

## Choose a capture model

| Model | Requirement |
| --- | --- |
| Sequential | One active attempt; existing clients use the returned capture URLs. |
| Shared concurrent application | Explicit sessions and W3C propagation through every relevant boundary, plus isolated mock/application state. |
| Isolated workers | Separate application/runtime state per owner, retaining independent archives. |

Jest uses one worker by default. For concurrent files or `.concurrent` tests configure `withWiretap(existingConfig, { explicitSession: true })` and instrument the application's existing HTTP server/client stack. Passing a trace header to the test's first request does not automatically make an uninstrumented application propagate it to dependencies.

## Route the boundaries

Point the shared test client at `process.env.WIRETAP_SUT_URL`, for example `supertest(process.env.WIRETAP_SUT_URL || app)`. Configure application dependency clients to use the session's dependency capture URLs. HTTP mock dependencies require their existing URL and kind; optional OpenAPI schemas enable validation and coverage. Wiretap forwards calls to the existing mock and does not create or verify mock expectations.

A traced session uses `/__wiretap/dep/<asset-id>`. Wiretap extracts the incoming trace before selecting that session's dependency upstream and schema. An explicit session without tracing uses `/__wiretap/session/<session-id>/dep/<asset-id>` and needs independent routing. The optional `X-Wiretap-Session-Id` selector is stripped before forwarding and need not pass through the SUT.

Configure OTel in your application before it creates HTTP clients, following the instrumentation library's setup. Existing instrumented applications can reuse their provider. A collector is not required for Wiretap correlation. Use a fresh trace per attempt and retain valid W3C `traceparent` and optional `tracestate`; sampling flags do not control capture.

## Ownership and ambiguity

Conflicting trace/header/URL selectors return `409` before forwarding. An unknown valid trace requires another unambiguous selector or the sole active implicit session. Closed traces stay reserved until successful run finalization. Missing inbound context can be replaced by a registered test root only when ownership is independently known; Wiretap never injects a test root into an untraced dependency call.

A trace associates an outbound call with a test, but may not identify its exact inbound parent request. Flow metadata distinguishes received/injected context and trace/explicit/active-session correlation. Parent links indicate inference strength; ambiguous parents remain unset. Trace propagation does not isolate shared datastore or mock state.

## Evidence and completion

Await all application/background work before the test ends. Teardown stops admission and drains accepted exchanges for five seconds by default. Timeout or cancellation records `capture_incomplete`, diagnostics and failed capture status. The finalized attempt cannot be mutated by later responses or reused by a later test.

While retired requests remain active, a new implicit attempt and unlabeled fallback requests are rejected. Explicit traced sessions can continue if ownership is clear. Managed Jest waits through ordinary per-test cleanup, finalizes once after all workers and stops the owned runtime. Forced termination cannot guarantee normal completion receipts or exports.

Retain the complete archive, inspect `test_correlation`, `trace_context_source` and `capture_diagnostics`, and use [runtime API](runtime-api.md) details when debugging selectors. See [troubleshooting](troubleshooting.md) for missing/ambiguous capture and [HTTPS trust](https-trust.md) for certificate routing.
