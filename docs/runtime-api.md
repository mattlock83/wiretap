# Runtime API

The SDK calls this HTTP API from your existing test framework's lifecycle.
The [JavaScript runner adapters](../sdk/js/README.md) and
[Go `Run`/`StartTest` hooks](../sdk/go/README.md) manage the lifecycle for ordinary
suites. Use this reference for another language or an externally managed runtime.

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
| POST | `/v1/startRun` | Start capture and create/reuse the run bundle. |
| POST | `/v1/setupTest` | Register a test attempt, trace and schemas. |
| POST | `/v1/teardownTest` | Drain, validate, and commit an attempt. |
| POST | `/v1/finishRun` | Finalize the bundle and stop capture. |
| POST | `/v1/report` | Calculate JSON coverage from the current or selected run artifact. |
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
fresh bundle under `WIRETAP_OUTPUT_DIR` (default `./artifacts`). The response
also includes `defaults`, the resolved project test settings from `.wiretap.yaml`.
For gRPC projects, `sut_url` and `dependencies` contain direct listener origins
with no path prefix. `startRun` allocates them before the SUT and mocks start;
it reports contract inventory as unresolved. The first `setupTest` resolves
all Protobuf contracts before traffic admission. See the [gRPC guide](grpc-user-guide.md).
Configured HTTP contracts freeze during startup, even when no attempts run, so
coverage retains the declared operations as untested. SDK-only schema overrides
are registered at test setup; declare contracts in `.wiretap.yaml` for empty-run
coverage. Repeated startup calls without new boundary definitions reuse the active run.

Optional `application: {name, id, description?}` identifies the SUT independently
of its API boundaries. It inherits `.wiretap.yaml` when omitted, and at least a
name or ID is required when supplied. It is retained in `bundle.json` and names
Postman collections as `Orders - orders-app`. Application identity stays fixed
for a run; later requests can omit it or repeat the same object.

Without a project file, register the same boundary definitions through HTTP:

```json
{
  "boundaries": {
    "sut": {
      "id": "orders",
      "protocol": "grpc",
      "url": "http://127.0.0.1:50051",
      "services": ["orders.v1.OrdersService"]
    },
    "dependencies": {
      "inventory": {
        "protocol": "grpc",
        "url": "http://127.0.0.1:50052",
        "kind": "mock",
        "services": ["inventory.v1.InventoryService"]
      }
    }
  }
}
```

`boundaries` accepts only `sut` and `dependencies`, using the same API fields,
source objects and TLS options as `.wiretap.yaml`. Local paths are relative to
the Wiretap process's working directory. Environment expansion follows project
configuration. Project-file boundaries cannot be overridden by HTTP. An active
run rejects new boundary registrations; finish it before changing targets or
contracts. Omitted gRPC `schema` selects reflection.

The response and `bundle.json` include `contracts`, recording each gRPC
boundary as `pending`, `failed` or `resolved`. Acquisition runs at most four
boundaries concurrently, publishes the whole snapshot before admission, and is
retryable after failure. Reflection is bounded to one minute per boundary;
control-client timeouts must allow for all batches. Credentials stay outside
this inventory and the returned defaults.
`target_url` is only a routing destination; Wiretap never starts, configures,
waits for, or stops that application.

`POST /v1/finishRun` stops the proxy after all tests are torn down. It returns
409 while any session is active. Retired gRPC calls are cancelled after their
bounded drain snapshot has been committed. Unresolved contracts must succeed
before finalization, including runs with zero tests. One orchestration
owner must control finalization when suites/workers share a runtime. This stops
capture and completes the bundle; it leaves the control process running. The reverse SUT
target cannot be changed while sessions are active.

## Test lifecycle

`POST /v1/setupTest` creates a named capture session. It also starts the proxy
if needed, so lower-level consumers do not have to call `startRun` first.

Omitted fields inherit project defaults. Supplied arrays replace the defaults.
OpenAPI specs are required and validated for HTTP before the session starts; an API can
explicitly allow an absent spec with `schema_required: false`. An invalid
supplied spec always fails. gRPC boundaries require a Protobuf contract,
configured with `protocol: grpc` and normally discovered through reflection.
The `sut_protocol` and dependency `protocol` fields can be carried through
the setup payload when inheriting project defaults. Capture uses synthetic data
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

`application` may also be supplied on `setupTest` when the SDK starts a run
lazily. Omitted application metadata reuses the active run's identity.
Dependency `asset_name` supplies its display name in Postman mock collection
exports, for example `Inventory Service - inventory`.

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
Every HTTP dependency requires a readable, valid OpenAPI schema unless that
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

`POST /v1/report` verifies the current run artifact and returns coverage in
`report`, alongside `run_id`, `bundle_path`, `run_state` and `snapshot_revision`.
A copied artifact directory can be selected with `bundle_path`:

```json
{
  "bundle_path": "artifacts/<run-id>"
}
```

Only run artifact directories are accepted. Unknown request fields are rejected.
The response includes detailed condition results under `report.contract_coverage`. The CLI
can also export YAML and Postman collections; see [capture artifacts and exports](artifacts.md).

## Proxy certificate

`GET /v1/proxy/ca` returns the public CA certificate used by the capture proxy.
Set proxy mode through runtime configuration before capture starts.

Supply schemas at test setup so they are registered with the
attempt before traffic is captured. The dependency gateway forwards traffic on
the capture listener; your mock server owns response configuration and verification.

With explicitly omitted schemas, `capture.schema_validation.status` is `partial` if
other APIs were validated or `skipped` if all omitted their schemas. `skipped`
lists the omitted schema IDs. Contract violations produce validation status
`fail` without overwriting the recorded framework outcome.
