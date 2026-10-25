# SDK lifecycle contract

An SDK connects the test framework to Wiretap's control API. The runtime owns
capture, schema validation, archive storage, coverage and Postman export. Keep
new adapters thin: they should manage lifecycle, test identity and final outcomes
using the runner's own hooks. Client instrumentation and service setup belong
to the application harness.

The [shared conformance scenarios](../sdk/conformance/scenarios.json) define
ownership, invalid receipts, cleanup, identity and configuration semantics.
Go, JavaScript and Python exercise the receipt vectors in their native test
suites. See [conformance details](../sdk/conformance/README.md) and the
[runtime API](runtime-api.md) for request/response fields.

| Responsibility | Managed suite owner | Test adapter |
| --- | --- | --- |
| Startup | Spawn the selected local executable; wait for private readiness from that child; validate its loopback address and run identity. | Connect to the owner's runtime. |
| Attempt | Keep the runtime alive through all worker and fixture cleanup. | Start before the work being captured; use stable test identity, variant and retry ordinal with a fresh trace. |
| Outcome | Preserve the test command's unsuccessful exit. | Record the final framework outcome after teardown, including fixture errors and dynamic skips. |
| Completion | Finalize once; validate matching run ID, archive and complete status; retain partial export paths on error. | Await teardown; surface capture errors without replacing an existing assertion failure. |
| Shutdown | Close the parent lifeline, wait for exit, fail on an unsuccessful child or forced kill, and retain bounded logs. | Await spawned work before ending the attempt. |

Use one owner per independent runtime. A shared runtime needs one outer owner
that waits for all workers. Go packages and pytest-xdist workers use independent
owners; explicit [run sets](run-sets.md) can group their completed archives.
No adapter should silently combine historical captures or choose the latest
archive from a shared output directory.

## Configuration and error behavior

Project YAML defines application boundaries and contracts. Runtime environment
variables override documented process settings. Omitted per-test schema
requirements and dependency lists inherit their project defaults; an explicit
empty dependency list clears that list, and an explicit schema requirement of
`false` remains false. gRPC boundaries/contracts are fixed
by the project run definition. Avoid a second language-specific configuration
language or independent schema resolver.

A supplied spec is validated even when a schema requirement is disabled.
Negative contract validation alone does not fail a framework test; missing
capture, malformed setup, failed teardown and failed export must surface.
Retain the original exception or framework failure and attach/report cleanup
errors separately. Static skipped tests that never start need no capture attempt.

Each run's `last-run.json` and `wiretap.log` belong inside its archive. A legacy
root-level latest receipt is advisory and cannot be used to identify a specific
concurrent run. Receipts and exported reports are derived outputs; immutable raw
evidence and its frozen contracts remain authoritative.

## Versions and capabilities

`GET /v1/capabilities` reports `api_version: "1.0"`, producer metadata and named
capabilities. It starts no run. Existing clients may keep using their established
endpoints. New adapters may probe it, tolerate a 404 from an older runtime, and
reject unsupported versions or missing required capabilities before setup.
Python uses this optional probe; Go and JavaScript retain their existing flow.

Control API, archive format, report envelope and measurement rules have separate
versions. Read the [compatibility policy](support.md#format-compatibility) before
building parsers. An SDK must not claim new capture capabilities merely because
it can start a newer executable.
