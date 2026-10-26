# Jest SDK lifecycle contract

The Jest SDK connects Jest's own lifecycle to Wiretap's control API. The runtime
owns HTTP capture, schema validation, archive storage, coverage and Arazzo
export. The SDK owns process startup, test identity, tracing context and final
Jest outcomes.

The [conformance scenarios](../sdk/conformance/scenarios.json) define ownership,
invalid receipts, cleanup, identity and configuration semantics. The Jest SDK's
tests exercise these receipt vectors. See [conformance details](../sdk/conformance/README.md)
and the [runtime API](runtime-api.md) for request and response fields.

| Phase | Suite owner | Jest environment |
| --- | --- | --- |
| Startup | Start the local executable; validate private readiness from that child, its loopback address and run identity. | Connect to the owner's runtime. |
| Attempt | Keep the runtime alive through worker and fixture cleanup. | Start before test hooks; use stable test identity and retry ordinal with a fresh trace. |
| Outcome | Preserve Jest's unsuccessful exit. | Record the final Jest outcome after test cleanup, including hook failures, retries and timeouts. |
| Completion | Finalize once; validate matching run ID, archive and complete status; retain partial export paths on error. | Await teardown and report capture errors alongside any existing test failure. |
| Shutdown | Close the parent lifeline, wait for exit, fail on unsuccessful shutdown or forced kill, and retain bounded logs. | Await spawned work before ending the attempt. |

`withWiretap` manages one runtime for a Jest project. Files use one worker by
default. Parallel workers and concurrent tests require explicit sessions and
trace-context propagation through captured application boundaries. The suite
owner waits for all workers before finalizing. Historical captures remain
separate; the adapter does not select a run by searching for the newest archive.

## Configuration and errors

Project YAML defines application boundaries and OpenAPI contracts. Runtime
environment variables override documented process settings. Omitted per-test
schema requirements and dependency lists inherit project defaults; an explicit
empty list clears it, and an explicit schema requirement of `false` remains
false. The SDK uses the runtime's contract resolution and validation.

Every supplied spec is validated. Negative contract validation alone does not
fail a Jest test. Missing capture, malformed setup, failed teardown and failed
export surface as errors. If cleanup also fails after an assertion failure,
Jest retains the original failure and reports the cleanup error separately.
Static skipped tests that never start create no capture attempt.

Each run's `last-run.json` and bounded `wiretap.log` belong inside its archive.
The output-root latest receipt is advisory and cannot identify a specific
concurrent run. Receipts and exported reports are derived outputs; captured raw
evidence and frozen contracts remain authoritative.

## Public package and runtime versions

`@wiretap/sdk` and `@wiretap/sdk/jest` expose `withWiretap`, `captureJest` and
`createJestSuite`. The package also exports its Jest environment and tracing
initialization module. Process ownership and the control client are internal
implementation details.

`GET /v1/capabilities` reports the runtime's API version, producer identity and
named capabilities without starting capture. The control API, archive format,
report envelope and measurement rules have separate versions. See the
[format compatibility policy](support.md#format-compatibility) when interpreting
saved evidence.
