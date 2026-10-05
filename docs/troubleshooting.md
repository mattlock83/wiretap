# Troubleshooting

## Runtime or routing problems

### The control service or proxy will not start

Check port conflicts and `WIRETAP_HTTP_LISTEN` / `WIRETAP_PROXY_LISTEN`. The
process must be allowed to bind local ports. A healthy control API at `/healthz`
does not mean capture has started; test setup or `startRun` starts the proxy.

### No traffic is captured

Send test calls to the session's `sutUrl` (HTTP API: `sut_url`), with the real
application address configured as `sut.url` or `sutBaseUrl`. Calls directly to
the application bypass Wiretap. Use ordinary HTTP clients within `runTest`, an
attempt's `run` callback, or the appropriate test adapter's trace context.

### Downstream dependency traffic is missing

Declare the dependency and configure the application's dependency base URL as
`http://<wiretap-proxy>/__wiretap/dep/<asset-id>`. Calls directly to the mock
bypass capture. The JavaScript SDK's `dependencyUrl(assetId)` returns its gateway.

Traced parallel sessions share this route and require inbound-to-outbound W3C
propagation through the application. Untraced explicit sessions need isolated,
session-scoped routes. In-process mocks that never send traffic to the proxy
cannot be captured. See [tracing](otel-adoption.md#choosing-a-supported-setup).

Exploration records provider traffic only. Its output does not include a fresh
capture of downstream dependency calls.

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

Use `createJestSuite` with `testEnvironment: "@wiretap/sdk/jest-environment"`.
The environment bridges Jest's VM and native clients. The adapter activates
context around async test callbacks, including `.each`/`.concurrent`, and
`testDefaults.onSession`. Separate `beforeEach`/`afterEach` requests are outside
that trace. See the [Jest guide](otel-adoption.md#optional-jest-adapter).

### Teardown reports incomplete capture or `finishRun` returns 409

Await expected requests and background work before teardown. Accepted requests
drain for five seconds by default; `drainTimeoutMs` can allow deliberately slow
requests. A timeout records a failed, incomplete attempt. Late completions are
never added to a later test.

One owner must finalize a shared run after every worker has torn down its
attempts. Use `manageRuntime: false` in worker integrations. `finishRun` stops
the proxy and finalizes the bundle; the control process stays available.

## Schemas, exploration and reports

### Setup requires an OpenAPI or Protobuf contract

For gRPC, reflection is the default source. If the upstream does not expose it,
configure local `.proto` files or a descriptor set as shown in the
[gRPC guide](grpc-user-guide.md). Every gRPC boundary requires a contract;
`schema_required: false` applies only to HTTP.

Set `schema` in `.wiretap.yaml`, or supply the schema through the SDK/API.
For an API with no spec, explicitly set `schema_required: false` on that API.
Supplied specs still validate, even with this setting. Inferred mock drafts
document observations and cannot replace a supplied contract.

### Generated tests all return 401 or 403

Exploration clears recorded credentials. Supply fresh authentication in
`before_case` or `before_request`, and prepare the corresponding user/fixtures.
These statuses never count as successful input rejection. If the fresh baseline
passes but invalid requests fail authentication, inspect the application's error
handling as well as the hook. The [Microsvc example](../examples/microsvc-transactions/README.md#authentication-and-spring-error-dispatch)
documents one such case.

### Exploration succeeds but some targets remain uncovered

`max_cases` limits executed cases. A successful exit means the baseline and
executed checks passed, not that every generated target ran. Check
**Contract coverage → Invalid-input coverage** and increase the budget if needed.
The generator supports representative isolated mutations; it does not cover
every payload permutation. See [exploration limits](exploration.md#generated-cases-and-coverage).

### Bodies are truncated in a bundle

Delivery still streams through the proxy. Capture retains 4 MiB per body by
default and records `truncated` plus `original_bytes` when that limit is reached.
Set `WIRETAP_MAX_CAPTURE_BYTES` to another non-negative byte count; `0` is
unlimited. Viewer previews have a separate 4 KiB display limit: open the linked
attachment to inspect all retained bytes.

### A bundle page is missing or has a broken link

Rebuild the generated views:

```bash
wiretap report --bundle ./artifacts/<run-id>
```

`bundle.json`, JSON under `tests/` and referenced schema/payload files are the source of
truth. If rebuilding reports an integrity error, restore the missing or corrupt
evidence from the original bundle. Regeneration cannot recover uncaptured or
lost evidence. Keep the whole bundle when copying it.
