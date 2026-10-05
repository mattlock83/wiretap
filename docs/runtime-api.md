# Runtime API

The SDK calls this HTTP API from your existing test framework's lifecycle.
Start the control process with `wiretap [--config path]`; suite hooks own capture
startup/finalization, and test hooks own attempts. Your harness owns the Wiretap,
SUT, mock, and test processes. Any language can use this API directly.

Generated provider checks are invoked within an active test using the exploration
endpoints. Local callbacks remain in the test process; Wiretap requests callback
work and accepts its JSON result. The HTTP API never accepts shell commands or
executes callback source code. See [exploration](exploration.md).

Schema validation checks message conformance; the provider validates credentials.
Declaring OpenAPI security does not require an authentication backend inside
Wiretap and does not itself establish that authentication was tested.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Check that the control service is available. |
| POST | `/v1/startRun` | Start capture and create/reuse the run bundle. |
| POST | `/v1/setupTest` | Register a test attempt, trace, schemas, and redactions. |
| POST | `/v1/teardownTest` | Drain, validate, and commit an attempt. |
| POST | `/v1/explore` | Snapshot the active test's completed happy path and start generated checks. |
| POST | `/v1/explore/next` | Wait for a callback action or exploration result. |
| POST | `/v1/explore/respond` | Complete a requested local callback. |
| POST | `/v1/explore/cancel` | Cancel execution and continue to cleanup. |
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
For gRPC projects, `sut_url` and `dependencies` contain direct listener origins
with no path prefix. `startRun` allocates them before the SUT and mocks start;
it reports contract inventory as unresolved. The first `setupTest` resolves
all Protobuf contracts before traffic admission. See the [gRPC guide](grpc-user-guide.md).
Repeated startup calls without new boundary definitions reuse the active run.

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
the setup payload when inheriting project defaults. `redactions` can register
per-attempt redaction rules.

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
`schema_validation`, plus `attempt_path` (canonical JSON), `attempt_page_path`
(readable Markdown), `bundle_path`, `index_path`, and `viewer_path`. These paths
are on the Wiretap server's filesystem; a remote SDK cannot open them locally
without copying or mounting that directory. OpenAPI
violations change an otherwise passing artifact to `fail`. Teardown stops new
admission and waits for accepted exchanges, up to `drain_timeout_ms` (default
5000, range 0–60000). Timeout/cancellation writes `capture_incomplete: true`,
`capture_diagnostics`, and failed status. Completion after finalization cannot
mutate the attempt or enter another session. Await background work first.

## Exploration inside a test

Call exploration after happy-path traffic and assertions, before `teardownTest`.
The parent session must be active with no unfinished requests. Wiretap snapshots
completed traffic and the frozen contract, then commits each generated case to
the same run bundle. Separate invocation and attempt IDs preserve lineage when
several scoped calls occur within a test.

`POST /v1/explore` starts work and returns `exploration_id` and state:

```json
{
  "session_id": "session-...",
  "operations": ["POST /orders"],
  "expected_statuses": [400],
  "request_timeout": "15s",
  "hook_timeout": "30s",
  "hooks": [
    {"id": "fixtures", "event": "before_case"},
    {"id": "sign-order", "event": "before_request", "operations": ["POST /orders"]},
    {"id": "database-check", "event": "check_case"},
    {"id": "cleanup", "event": "after_case"}
  ],
  "bindings": [{"name": "user_id", "in": "body", "path": "/user_id"}]
}
```

Selectors use exact OpenAPI operations (`POST /orders`) or fully qualified gRPC
procedures (`/orders.v1.Orders/CreateOrder`). For gRPC, optional
`expected_grpc_statuses` defaults to `[3]` (`INVALID_ARGUMENT`); only 3 and 11
(`OUT_OF_RANGE`) are accepted. This is separate from HTTP `expected_statuses`.
Optional `flow_id` selects one captured
request. Omitted `max_cases` or zero executes all supported generated cases;
positive limits leave remaining targets not covered. Optional `extract` and
`redactions` have the same semantics described in the exploration guide.
Unsupported protocols and invalid selectors fail before execution. Each hook
registration has a unique caller-owned ID and one event. Multiple registrations
can scope the same event to different operations. gRPC callbacks use
`grpc_request` with procedure, metadata, ProtoJSON messages, base64 Protobuf
messages and receive-before-send response gates. Body bindings accept an
optional `message_index` (otherwise apply to every request message); extractions
default to response message zero. Metadata bindings and metadata/trailers
extractions use keys. See the [native gRPC rules](exploration.md#native-grpc-cases).

`POST /v1/explore/next` long-polls for up to ten seconds:

```json
{"session_id": "session-...", "exploration_id": "exploration-..."}
```

A response can contain a callback action:

```json
{
  "exploration_id": "exploration-...",
  "state": "running",
  "action": {
    "id": "action-...",
    "hook_id": "fixtures",
    "event": "before_case",
    "input": {"event": "before_case", "mode": "rejection", "operation": "POST /orders"}
  }
}
```

Invoke the corresponding local function with `input`, then call
`POST /v1/explore/respond` with the same identity and action ID:

```json
{
  "session_id": "session-...",
  "exploration_id": "exploration-...",
  "action_id": "action-...",
  "output": {"bindings": {"user_id": "fresh-user"}, "state": {"cleanup_id": "fresh-user"}}
}
```

Send `error` instead of `output` when the local callback throws. Outputs can
contain `bindings`, `state` and `headers`. Only `check_case` may return named
`checks`; other hook events reject that field. See the
[callback lifecycle](exploration.md#lifecycle-callbacks). Match replies to the
requested action, and do not repeat local side effects if a delivery is retried.
The runtime enforces action ownership and rejects conflicting responses.

Callback inputs include `trace_context`: the parent test's context for
`before_explore`/`after_explore`, or the generated case's context for case and
request callbacks. Use it to route callback HTTP requests through the correct
session. The runtime excludes traffic admitted while that owner's callback is
active from retained evidence and coverage. Await fixture/authentication/check
requests before responding; those requests must not become generated test
coverage. SDK callback wrappers activate the supplied context automatically.

Poll until state is `complete`, `failed` or `cancelled`. Terminal responses
include `result` with generated/executed/passed/failed case counts, baseline
count, generation notes and `bundle_path`. A failure must fail the enclosing
test even though the control request itself was handled successfully.

Cancellation uses `POST /v1/explore/cancel` with `session_id` and
`exploration_id`. Continue polling and dispatching cleanup callbacks until a
terminal state. A disconnected client cannot run local cleanup: hook timeouts
bound the server's wait and the bundle records failure. SDKs implement this
handoff, cancellation and aggregate failure propagation. Parent teardown and
run finalization must wait for exploration to finish.

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

The bundle's `docs/viewer.html` is written automatically from the same
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
