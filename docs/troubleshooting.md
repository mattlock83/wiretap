# Troubleshooting

## Proxy will not start

Check port conflicts and `PHROND_HTTP_LISTEN` / `PHROND_PROXY_LISTEN`. Confirm
the process may bind local ports.

## No traffic is captured

Send test calls to `sut_url` / `runtime.sutUrl`, not directly to the SUT. Check
that `sutBaseUrl` points to the already-running application. Use ordinary HTTP clients within the test adapter or `runTest` trace context.

## Downstream dependency traffic is missing

Declare the dependency with its existing upstream URL and valid OpenAPI schema,
then configure the SUT's dependency base URL to
`http://<phrond-proxy>/__phrond/dep/<asset-id>`. Calls sent directly to the mock
server bypass capture. `runtime.dependencyUrl(assetId)` returns the configured
gateway. Traced parallel sessions use this stable URL and require the SUT to
propagate W3C context on its server and outbound client path. Untraced explicit
sessions use separate session-scoped addresses.

## HTTPS fails

Trust Phrond's CA and use `PHROND_PROXY_MODE=mitm`; see `https-trust.md`. In
passthrough mode encrypted request and response bodies cannot be inspected.

## `setupTest` returns 409

Only one zero-configuration session may be active. Ensure teardown runs after
every test (`phrond.test` does this), run serially, or use explicit sessions.
Also check for a reused trace ID or another implicit session still active. Each retry
needs a fresh root trace. Finished trace IDs remain reserved until runtime shutdown. After a drain timeout,
wait for accepted requests to finish before starting another implicit session;
Phrond returns 409 while old untraced work could contaminate it.

## `setupTest` says an OpenAPI schema is required

Attach the service's schema through `schemas`. If the service does not have one,
configure `generate_schema: true` for the SUT, or explicitly set
`schema_required: false` for the particular API missing a spec. Generation is intentionally
explicit and cannot be combined with a supplied schema.

## An attempt contains HTTP 502

Phrond returns 502 when it cannot reach or establish TLS with the configured
SUT or a declared external dependency. The flow's `transport_error` contains the
original upstream failure; `asset_id` identifies a failed dependency route.

## Bodies are truncated in a bundle

Complete bodies are still delivered. The bundle retains 4 MiB by default and
sets `truncated: true` plus `original_bytes`. Adjust
`PHROND_MAX_CAPTURE_BYTES`; `0` means unlimited and should be used cautiously.

## Proxy traffic returns 409

Phrond cannot assign the request unambiguously, the trace belongs to a closed
session, or the URL/session header conflicts with its registered trace. Check
that test instrumentation loads before the client and that the SUT propagates
`traceparent` end to end. An OTel library merely being installed is insufficient.
Without propagation, serialize tests and SUT requests or isolate each worker.
The SUT propagates standard `traceparent`/`tracestate`; `X-Phrond-Session-Id` is
only for test-to-Phrond session selection.

## A dependency has no parent flow

Look at `test_correlation` and `parent_correlation`. Several overlapping inbound
requests in one test can share a trace. Phrond preserves that test grouping but
leaves ambiguous request parents unset. `trace_unique` and `timing` are inferences,
not a recovered SUT span tree. An untraced dependency remains untraced in evidence.

## Jest requests have no trace context

Set `testEnvironment: "@phrond/sdk/jest-environment"` and use
`createJestSuite` from `@phrond/sdk/jest`. Jest's VM loader needs the environment
bridge for native clients. The adapter covers async test callbacks, including
`.each`/`.concurrent`, rather than separate `beforeEach`/`afterEach` hooks. See
[the adoption guide](otel-adoption.md) for initialization and existing OTel setup.

## Teardown reports incomplete capture or shutdown returns 409

Await expected requests and background activity before teardown. Accepted
requests drain for five seconds by default. A timeout produces a failed attempt
with `capture_incomplete` and diagnostics; increasing `drainTimeoutMs` can help
with deliberately slow requests. Late completions are never added to a later
test. Use one lifecycle owner for shared suites and `manageRuntime: false` in
workers; stop Phrond only after every test is torn down.

## A bundle page is missing or has a broken link

The JSON files under `evidence/` and the referenced schema/payload files are
the source of truth. Regenerate Phrond's pages with
`go run ./cmd/phrond report --bundle <run-directory>`. A missing Markdown
page does not make a successful evidence read or diagnostic export empty. If the
command reports an integrity error, restore the missing or corrupted evidence
file from the original bundle; page regeneration cannot invent it.
