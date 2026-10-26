# Runtime API

The [Jest adapter](../sdk/js/docs/jest.md) calls this HTTP API from the framework lifecycle. This reference describes the underlying endpoints and an explicitly hosted runtime. Jest is the supported SDK integration on this branch.

For direct integration, start the control process with
`wiretap [--config path]`. Suite hooks own capture startup/finalization, test
hooks own attempts, and your harness owns the processes. Choose one suite owner
for each runtime.

Schema validation checks message conformance; the provider validates credentials.
Declaring OpenAPI security does not require an authentication backend inside
Wiretap and does not itself establish that authentication was tested.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Check that the control service is available. |
| GET | `/v1/capabilities` | Read the API version, producer and supported capabilities. |
| POST | `/v1/startRun` | Start capture and create/reuse the run bundle. |
| POST | `/v1/setupTest` | Register a test attempt, trace and schemas. |
| POST | `/v1/teardownTest` | Drain, validate, and commit an attempt. |
| POST | `/v1/finishRun` | Finalize the bundle and stop capture. |
| GET | `/v1/proxy/ca` | Read the public proxy CA certificate. |

## Suite lifecycle

`POST /v1/startRun` starts the capture proxy:

```json
{
  "application": {"name": "Orders", "id": "orders-app"},
  "proxy": {
    "listen_addr": "127.0.0.1:19000",
    "target_url": "http://127.0.0.1:18090"
  }
}
```

The response contains `sut_url`, the reverse proxy address tests should call,
and `dependency_base_url`, the reserved dependency gateway on that same proxy.
It also returns `run_id` and `bundle_path`. A new run gets a
fresh bundle under `WIRETAP_OUTPUT_DIR` (default `./artifacts`).
Configured HTTP contracts freeze during startup, even when no attempts run, so
coverage retains the declared operations as untested. SDK-only schema overrides
are registered at test setup; declare contracts in `.wiretap.yaml` for empty-run
coverage. Repeated startup calls reuse the active run.

Optional `application: {name, id, description?}` identifies the SUT independently
of its API boundaries. It inherits `.wiretap.yaml` when omitted, and at least a
name or ID is required when supplied. It is retained in `bundle.json`. Application identity stays fixed
for a run; later requests can omit it or repeat the same object.

`target_url` is only a routing destination; Wiretap never starts, configures,
waits for, or stops that application.

`POST /v1/finishRun` stops the proxy after all tests are torn down. It returns
409 while any session is active. One orchestration
owner must control finalization when suites/workers share a runtime. This stops
capture and completes the bundle; it leaves the control process running. The reverse SUT
target cannot be changed while sessions are active.

## Test lifecycle

`POST /v1/setupTest` creates a named capture session. It also starts the proxy
if needed, so lower-level consumers do not have to call `startRun` first.

Go applies project defaults to omitted fields. Supplied `schemas` and
`dependencies` arrays replace their defaults; empty arrays clear them. The
Jest SDK accepts dependencies as a keyed object and converts them to this HTTP
array.

The main HTTP API's OpenAPI spec is required by default; its explicit
`schema_required: false` opt-out remains available. HTTP dependencies need no
spec unless `schema_required: true` is set. Supplying one opts into validation
and contract coverage; an invalid supplied spec always fails. Removed protocol-specific setup fields are rejected. Capture uses synthetic data
and dummy secrets; retained traffic is unchanged.

```json
{
  "name": "hello world happy flow",
  "application": {"name": "Orders", "id": "orders-app"},
  "api_id": "orders",
  "sut_target_kind": "service",
  "tags": ["smoke"],
  "meta": {"suite": "hello"},
  "sut_base_url": "http://127.0.0.1:18090",
  "explicit_session": false,
  "schemas": [{
    "id": "hello-openapi",
    "type": "openapi",
    "source": {"type": "file", "path": "schemas/hello.openapi.yaml"}
  }],
  "dependencies": [{
    "asset_id": "inventory",
    "asset_name": "Inventory Service",
    "upstream_kind": "mock",
    "upstream_url": "http://127.0.0.1:9090"
  }]
}
```

`application` may also be supplied on `setupTest` when the SDK starts a run
lazily. Omitted application metadata reuses the active run's identity.
Dependency `asset_name` supplies a human-readable display name in the inventory.

The response includes `session_id`, `sut_url`, `proxy_listen`, `run_id`,
`bundle_path`, and a
`dependencies` object mapping asset IDs to their capture URLs. Send inbound
calls to `sut_url`. Configure the SUT's existing dependency base URL to the
returned inventory URL, for example:

```text
http://127.0.0.1:19000/__wiretap/dep/inventory
```

Requests to `/__wiretap/dep/inventory/products/123` are forwarded to
`http://127.0.0.1:9090/products/123`. Request methods, headers, bodies, query
parameters, responses, and trailers retain the existing proxy's bounded-capture
semantics. Wiretap does not start, configure, stub, or verify the upstream.
HTTP dependencies can capture without a schema or opt-out flag. Add a `schemas`
array to enable validation and contract coverage, or `schema_required: true`
to require a spec. An omitted dependency schema produces skipped validation
and unknown contract coverage. Ordinary instrumented
clients use W3C trace context; custom clients can select an explicit session
with `X-Wiretap-Session-Id` on requests to Wiretap.

Only one implicit session can be active. Unlabeled traffic falls back to it
only when it is the sole active session. Optional standard trace registration:

```json
{
  "trace_context": {
    "traceparent": "00-11111111111111111111111111111111-2222222222222222-00",
    "tracestate": "vendor=value"
  }
}
```

This is the serialized **test root span** context. Supply a fresh trace for every
attempt; the fixed value above is illustrative. Setup returns the accepted
`trace_context`. Invalid/zero IDs or invalid tracestate return 400; reusing an
active or finished test trace returns 409. Sampling flags do not affect capture.

For parallel tests, set `explicit_session: true` and supply `trace_context`.
Traced sessions return stable `/__wiretap/dep/<asset-id>` dependency URLs, selected
by propagated trace **before** resolving the session's upstream and schema.
Untraced explicit sessions retain `/__wiretap/session/<session-id>/dep/<asset-id>`
URLs and require independent routing/isolation; they cannot make a shared SUT
propagate context. `X-Wiretap-Session-Id` remains an optional selector on incoming
requests and is stripped before forwarding. It never needs to pass through the SUT.

Conflicting header, URL, or registered trace selectors return 409 before
forwarding. Unknown valid traces are preserved and require another unambiguous
selector or the single implicit fallback; they are not automatically registered.
A root mismatch is recorded in `capture_diagnostics`. Closed trace IDs are
rejected until a successful `finishRun` clears the routing
state, even if a newer default test is active. While requests from a timed-out
attempt remain in flight, new implicit sessions and unlabeled fallback traffic
return 409; explicit traced sessions can still run.

Missing/invalid inbound context may be replaced by the registered root when
ownership is independently known. The flow says `trace_context_source: injected`
and keeps its original `test_correlation` method. Wiretap never injects test
context into an untraced dependency request. Ambiguous unlabeled traffic returns
409.

`POST /v1/teardownTest` finalizes and commits the attempt to the bundle:

```json
{"session_id": "session-...", "status": "pass", "drain_timeout_ms": 5000}
```

The response contains `capture`, the completed test capture including
`schema_validation`, plus `attempt_path` (canonical JSON) and `bundle_path`. These paths
are on the Wiretap server's filesystem; a remote SDK cannot open them locally
without copying or mounting that directory. The framework-supplied test outcome
is independent of `schema_validation`: a deliberate invalid request can fail
contract validation while its rejection test passes. Setup and capture failures
still surface as errors. Teardown stops new
admission and waits for accepted exchanges, up to `drain_timeout_ms` (default
5000, range 0–60000). Timeout/cancellation writes `capture_incomplete: true`,
`capture_diagnostics`, and failed status. Completion after finalization cannot
mutate the attempt or enter another session. Await background work first.

## Reports

Run finalization generates configured JSON/text/HTML coverage and eligible Arazzo
workflows. Their paths are returned under `outputs`. To regenerate outputs from
a saved archive, use `wiretap report`; see [capture artifacts and exports](artifacts.md).

## Proxy certificate

`GET /v1/proxy/ca` returns the public CA certificate used by the capture proxy.
Set proxy mode through runtime configuration before capture starts.

Supply schemas at test setup so they are registered with the
attempt before traffic is captured. The dependency gateway forwards traffic on
the capture listener; your mock server owns response configuration and verification.

With omitted schemas, `capture.schema_validation.status` is `partial` if
other APIs were validated or `skipped` if all omitted their schemas. `skipped`
lists the omitted schema IDs. Contract violations produce validation status
`fail` without overwriting the recorded framework outcome.

## Discover capabilities

`GET /v1/capabilities` returns `api_version: "1.0"`, `producer` and a
`capabilities` array containing `capture.http`, `outputs.coverage`,
`outputs.arazzo`, `test.identity` and `run.finalize`. This read starts no run and
does not wait for capture control operations. Other methods return 405. Existing
clients can continue without probing; adapters supporting older binaries may
treat 404 as unavailable discovery.

Successful finish responses and export receipts include `report_version: "1.0"`
and `producer`. Fields are additive; existing lifecycle behavior is unchanged.
See the [control response schemas](control-api.schema.json),
[Jest lifecycle ownership](architecture.md#jest-lifecycle-ownership) and [format compatibility](support.md#format-compatibility).
