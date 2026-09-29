# Runtime API

The SDK calls this HTTP API from your existing test framework's lifecycle.
Start the control process with `wiretap [--config path]`; suite hooks own capture
startup/finalization, and test hooks own attempts. Your harness owns the Wiretap,
SUT, mock, and test processes. Any language can use this API directly.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Check that the control service is available. |
| POST | `/v1/startRun` | Start capture and create/reuse the run bundle. |
| POST | `/v1/setupTest` | Register a test attempt, trace, schemas, and redactions. |
| POST | `/v1/teardownTest` | Drain, validate, and commit an attempt. |
| POST | `/v1/finishRun` | Finalize the bundle and stop capture. |
| POST | `/v1/report` | Rebuild bundle views, including inferred mock OpenAPI drafts. |
| GET | `/v1/proxy/ca` | Read the public proxy CA certificate. |

## Suite lifecycle

`POST /v1/startRun` starts the capture proxy:

```json
{
  "proxy": {
    "listen_addr": "127.0.0.1:19000",
    "target_url": "http://127.0.0.1:18090"
  }
}
```

The response contains `sut_url`, the reverse proxy address tests should call,
and `dependency_base_url`, the reserved dependency gateway on that same proxy.
It also returns `run_id`, `bundle_path`, `index_path`, and `viewer_path`. A new run gets a
fresh bundle under `WIRETAP_OUTPUT_DIR` (default `./artifacts`). The response
also includes `defaults`, the resolved project test settings from `.wiretap.yaml`.
Repeated startup calls reuse the active run.
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
Omitted fields inherit project defaults. Supplied arrays replace the defaults.
OpenAPI specs are required and validated before the session starts; an API can
explicitly allow an absent spec with `schema_required: false`. An invalid
supplied spec always fails. `redactions` can register per-attempt redaction rules.

```json
{
  "name": "hello world happy flow",
  "api_id": "orders",
  "sut_target_kind": "service",
  "tags": ["smoke"],
  "meta": {"suite": "hello"},
  "sut_base_url": "http://127.0.0.1:18090",
  "explicit_session": false,
  "schemas": [{
    "id": "hello-openapi",
    "role": "sut",
    "type": "openapi",
    "source": {"type": "file", "path": "schemas/hello.openapi.yaml"}
  }],
  "dependencies": [{
    "asset_id": "inventory",
    "asset_name": "Inventory Service",
    "upstream_kind": "mock",
    "upstream_url": "http://127.0.0.1:9090",
    "schema": {
      "id": "inventory-openapi",
      "type": "openapi",
      "source": {"type": "file", "path": "schemas/inventory.openapi.yaml"}
    }
  }]
}
```

The response includes `session_id`, `sut_url`, `proxy_listen`, `run_id`,
`bundle_path`, `index_path`, `viewer_path`, and a
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
Every dependency requires a readable, valid OpenAPI schema unless that
dependency explicitly sets `schema_required: false`. Ordinary instrumented
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
`schema_validation`, plus `attempt_path` (canonical JSON), `attempt_page_path`
(readable Markdown), `bundle_path`, `index_path`, and `viewer_path`. These paths
are on the Wiretap server's filesystem; a remote SDK cannot open them locally
without copying or mounting that directory. OpenAPI
violations change an otherwise passing artifact to `fail`. Teardown stops new
admission and waits for accepted exchanges, up to `drain_timeout_ms` (default
5000, range 0–60000). Timeout/cancellation writes `capture_incomplete: true`,
`capture_diagnostics`, and failed status. Completion after finalization cannot
mutate the attempt or enter another session. Await background work first.

## Mock OpenAPI drafts

Dependencies with `upstream_kind: mock` automatically get a separate inferred
OpenAPI draft in the bundle, with or without a supplied schema. Without a schema,
set `schema_required: false` explicitly. Drafts update at teardown and run
finalization, describe observed traffic, and never replace supplied contracts or
affect validation and coverage. The Dependencies page and HTML dashboard link
to each draft. See [capture bundles](okf-bundles.md#inferred-mock-assumptions).

## Reports

`POST /v1/report` refreshes the current run's OKF pages and returns
`run_id`, `bundle_path`, `index_path`, `viewer_path`, `run_state`, `snapshot_revision`, and a
coverage summary. A copied bundle can be selected with `bundle_path`:

```json
{
  "bundle_path": "artifacts/<run-id>"
}
```

The bundle's `viewer/index.html` is written automatically from the same
bundle evidence. Only bundles are accepted. Unknown request fields are rejected. See [capture bundles](okf-bundles.md).

## Proxy certificate

`GET /v1/proxy/ca` returns the public CA certificate used by the capture proxy.
Set proxy mode through runtime configuration before capture starts.

Supply schemas and redactions at test setup so they are registered with the
attempt before traffic is captured. The dependency gateway forwards traffic on
the capture listener; your mock server owns response configuration and verification.

With explicitly omitted schemas, `capture.schema_validation.status` is `partial` if
other APIs were validated or `skipped` if all omitted their schemas. `skipped`
lists the omitted schema IDs. Enforced validation failures still produce `fail`.
