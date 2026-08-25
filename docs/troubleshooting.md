# Troubleshooting

## Proxy will not start

Check port conflicts and `PHROND_HTTP_LISTEN` / `PHROND_PROXY_LISTEN`. Confirm
the process may bind local ports.

## No traffic is captured

Send test calls to `sut_url` / `runtime.sutUrl`, not directly to the SUT. Check
that `sutBaseUrl` points to the already-running application. SDK HTTP helpers
perform both steps automatically.

## Downstream dependency traffic is missing

Declare the dependency with its existing upstream URL and valid OpenAPI schema,
then configure the SUT's dependency base URL to
`http://<phrond-proxy>/__phrond/dep/<asset-id>`. Calls sent directly to the mock
server bypass capture. `runtime.dependencyUrl(assetId)` returns the configured
gateway; parallel tests must use the session-scoped address returned by setup.

## HTTPS fails

Trust Phrond's CA and use `PHROND_PROXY_MODE=mitm`; see `https-trust.md`. In
passthrough mode encrypted request and response bodies cannot be inspected.

## `setupTest` returns 409

Only one zero-configuration session may be active. Ensure teardown runs after
every test (`phrond.test` does this), run serially, or use explicit sessions.

## `setupTest` says an OpenAPI schema is required

Attach the service's schema through `schemas`. If the service does not have one,
opt in with the SDK's `generateSchema` configuration or set
`generate_schema: true` through the runtime API. Generation is intentionally
explicit and cannot be combined with a supplied schema.

## An artifact contains HTTP 502

Phrond returns 502 when it cannot reach or establish TLS with the configured
SUT or a declared external dependency. The flow's `transport_error` contains the
original upstream failure; `asset_id` identifies a failed dependency route.

## Bodies are truncated in artifacts

Complete bodies are still delivered. The artifact retains 4 MiB by default and
sets `truncated: true` plus `original_bytes`. Adjust
`PHROND_MAX_CAPTURE_BYTES`; `0` means unlimited and should be used cautiously.
