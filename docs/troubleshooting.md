# Troubleshooting

## Runtime or routing problems

### The SDK cannot find the Wiretap binary

Build the local runtime as described in [installation](local-release.md), then
put it on `PATH` or set `WIRETAP_BINARY` to its absolute path. The adapters do not
download or build executables. An external runtime instead uses
`WIRETAP_BASE_URL`; use that mode only when your harness owns its lifecycle.

### The control service or proxy will not start

Managed SDKs choose available local ports. For an external runtime, check port
conflicts and `WIRETAP_HTTP_LISTEN` / `WIRETAP_PROXY_LISTEN`. The
process must be allowed to bind local ports. A healthy control API at `/healthz`
does not mean capture has started; test setup or `startRun` starts the proxy.

### No traffic is captured

Send test calls to the session's `sutUrl` (Go: `SUTURL`; HTTP API: `sut_url`), with the real
application address configured as `sut.url` or a per-test target override. Calls directly to
the application bypass Wiretap. Use ordinary HTTP clients within `runTest`, an
attempt's `run` callback, or the appropriate test adapter's trace context.

### Downstream dependency traffic is missing

Declare the dependency and configure the application's client with its returned
capture address: JavaScript uses `dependencyUrl(assetId)` and Go uses
`session.Dependencies[assetID]`. HTTP dependencies use a gateway path such as
`http://<wiretap-proxy>/__wiretap/dep/<asset-id>`; native gRPC uses a direct origin
without that path. Calls directly to the mock bypass capture. Managed runtimes
choose ports dynamically, so read the returned address.

Traced parallel sessions share their capture address and require inbound-to-outbound W3C
propagation through the application. Untraced explicit sessions need isolated,
session-scoped routes. In-process mocks that never send traffic to the proxy
cannot be captured. See [tracing](otel-adoption.md#choosing-a-supported-setup).

### HTTPS or an upstream connection fails

For decrypted HTTPS capture, trust Wiretap's CA and use
`WIRETAP_PROXY_MODE=mitm`; see [HTTPS trust](https-trust.md). Passthrough mode
cannot inspect encrypted bodies.

Wiretap returns HTTP 502 when it cannot reach or establish TLS with the SUT or
a dependency. Inspect `transport_error` for the upstream failure and `asset_id`
for the affected dependency route.

## Test lifecycle and tracing problems

### `setupTest` returns 409

Only one implicit session can be active. Ensure each attempt is torn down,
run serially, or use explicit traced sessions with suitable propagation.
Every retry needs a fresh trace. Finished trace IDs stay reserved until
successful run finalization with `finishRun`.

After a drain timeout, wait for accepted requests to finish before starting
another implicit session. Wiretap rejects it while untraced work from the old
attempt could contaminate the new one.

### Proxy traffic returns 409

The request cannot be assigned unambiguously, its trace belongs to a closed
session, or its URL/session header conflicts with its registered trace. Load
test instrumentation before the HTTP client and verify actual `traceparent`
propagation through the application. An installed OTel library alone is not
enough. Without propagation, serialize tests and requests or isolate workers.

`X-Wiretap-Session-Id` selects a session on requests to Wiretap. The application
only needs to propagate standard `traceparent`/`tracestate` for traced capture.

### A dependency has no parent flow

Inspect `test_correlation` and `parent_correlation`. Overlapping inbound requests
can share a test trace without revealing which caused a dependency call.
Wiretap keeps the test grouping and leaves ambiguous parents unset.
`trace_unique` and `timing` are inferences, not a recovered application span tree.

### Jest requests have no trace context

Wrap the Jest configuration with `withWiretap` from `@wiretap/sdk/jest`.
It installs the environment and tracing around ordinary Jest tests and fixtures.
For selective capture in a custom integration, use `createJestSuite` with
`testEnvironment: "@wiretap/sdk/jest-environment"`. Do not combine both modes in
the same test. Jest skips per-test hooks for `.concurrent`;
put concurrent fixtures inside the callback. Suite-wide hooks do not belong to
an individual attempt. See the [Jest guide](../sdk/js/docs/jest.md).

### Go tests passed but no new archive appeared

Use `go test -count=1 ./...` to produce fresh evidence instead of reusing Go's
test cache. Add `wiretap.Run(m)` in `TestMain` and call `wiretap.StartTest(t)` in
each test or shared setup helper. For parallel tests call `StartTest` after
`t.Parallel()` and enable explicit sessions. See the [Go guide](../sdk/go/README.md).

### Capture ended during a panic or timeout

Ordinary assertion failures still finalize exports. Managed Go and JavaScript
SDKs hold a stdin lifeline to their child runtime. If the owning test process
disappears, EOF requests graceful shutdown so the runtime stops and preserves
an interrupted raw archive with pending attempts.

Abrupt termination cannot finish normal exports or save the owner's completion
receipt. `last-run.json` can be absent or left from an earlier run; inspect
`bundle.json` instead of treating that receipt as proof of current completion.
Retain the full output directory and runtime log. If Wiretap itself is killed
before graceful shutdown finishes, its archive can remain open. Normal managed
shutdown waits for the child only up to its shutdown timeout, then force-kills it.

### Teardown reports incomplete capture or `finishRun` returns 409

Await expected requests and background work before teardown. Accepted requests
drain for five seconds by default; `drainTimeoutMs` can allow deliberately slow
requests. A timeout records a failed, incomplete attempt. Late completions are
never added to a later test.

One owner must finalize a shared run after every worker has torn down its
attempts. Use `manageRuntime: false` in worker integrations. `finishRun` stops
the proxy and finalizes the bundle; the control process stays available.

## Schemas and reports

### Setup requires an OpenAPI or Protobuf contract

For gRPC, reflection is the default source. If the upstream does not expose it,
configure local `.proto` files or a descriptor set as shown in the
[gRPC guide](grpc-user-guide.md). Every gRPC boundary requires a contract;
`schema_required: false` applies only to HTTP.

Set `schema` in `.wiretap.yaml`, or supply the schema through the SDK/API.
For an API with no spec, explicitly set `schema_required: false` on that API.
Supplied specs still validate, even with this setting. Omitted schemas leave
coverage unknown.

### Bodies are truncated in a bundle

Delivery still streams through the proxy. Capture retains 4 MiB per body by
default and records `truncated` plus `original_bytes` when that limit is reached.
Set `WIRETAP_MAX_CAPTURE_BYTES` to another non-negative byte count; `0` is
unlimited. Inspect the referenced payload attachment for the retained bytes.

### A report cannot read a capture artifact

Verify the artifact by generating a coverage report:

```bash
wiretap report --bundle ./artifacts/<run-id>
```

`bundle.json`, JSON under `tests/` and referenced schema/payload files are the source of
truth. If reporting finds an integrity error, restore the missing or corrupt
evidence from the original capture. Exporting cannot recover uncaptured or lost
evidence. Keep the whole directory when copying it.
