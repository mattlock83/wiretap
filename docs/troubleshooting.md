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

## Assumption checks

Use the [checking guide](assumption-checks.md) for setup and examples. Its
[issue catalogue](assumption-checks.md#issues-the-check-will-find),
[limits](assumption-checks.md#what-the-check-will-not-establish) and
[result interpretation](assumption-checks.md#interpreting-results) define what
the verdict means.

### Both suites test the endpoint, but the request is uncovered

`provider_request_uncovered` means no complete passing provider example matches
the concrete request. A consumer calling `GET /items/consumer-42` and a provider
testing `GET /items/provider-17` exercise the same route but different scenarios.
Query values, body fields, array order and application headers also participate.
Equal operation or contract coverage does not establish a match.

Follow the consumer and candidate provider evidence references and compare
their actual values. Align shared synthetic fixtures or add the missing
provider test, then recapture both suites. Do not rename a collection item to
make the requests appear related. Explicit header exclusions can handle a
header proven irrelevant to the assumption; payload wildcards and ignored body
fields are not supported.

### An optional response field causes a mismatch

`provider_response_mismatch` checks the whole captured response. A mock returning
`{"id":"42"}` and a provider returning `{"id":"42","label":"demo"}` differ,
even if `label` is optional and the running consumer ignores it. Status, other
body values, response headers and gRPC status details may also differ.

Inspect `differences` and the referenced exchanges. Decide whether the mock
should reflect the provider example, or whether the provider tested a different
intended response scenario. Update the appropriate tests or fixtures and
recapture. This strict mismatch alone does not prove a runtime compatibility
break: Wiretap cannot infer which fields the consumer used or which assertions
its test made. There is no response-field subset mode.

### Provider examples are excluded even though some tests passed

Inspect `excluded_provider_evidence` and its `reason`. A failed test, incomplete
capture, transport error or unsupported selected exchange cannot establish
provider agreement. A different complete passing provider exchange can still
support the same mock, so unrelated failed examples do not automatically poison
an otherwise verified check.

If no qualifying provider exchange matches the request, the assumption remains
uncovered. Fix the provider test or capture problem and generate a fresh
complete archive. An interrupted run itself is unverified even when a few
completed attempts remain available. A selected consumer example from a failed
or incomplete attempt is also unverified.

### The captured JSON is equal, but HTTP contract rules differ

`http_contract_mismatch` reports declared-rule drift on an exercised operation.
Both suites can return `{"status":"ready"}` while one schema permits only
`ready` and the other permits `ready` plus `pending`. Likewise, the same short
string can satisfy two different `maxLength` rules. Requiredness, nullability,
nested optional properties, response-header rules and composition also
participate, even when the specific property is absent from the example.

Inspect the `contract/request/...` or `contract/response/...` difference and the
frozen contracts. This is conservative rule equality: an added enum value,
relaxed bound or equivalent schema expressed differently can fail without
proving a runtime break. Align the intended contracts and recapture after the
change is resolved. Descriptions, examples, defaults and enum/required ordering
are ignored; unused operations, statuses and media alternatives are not diffed.

`http_contract_violation` instead means a checked message violates its own
schema. Each response is validated, and requests are validated when their
response status is below `400`. Intentional negative request examples with
status `400` or higher skip request-value validation, while their request rules
and response values remain checked. A passing framework test does not override
these contract findings.

### The HTTP contract check is unavailable

`http_contract_unavailable` means a required contract or rule cannot support
assurance, for example one missing schema, OpenAPI 3.1, or numeric rules that
cannot be evaluated reliably. Supply supported frozen OpenAPI 3.0 evidence on
both sides and recapture if necessary. Restore corrupt or missing archive
attachments from the original run when archive loading reports an input error.
Other unsupported examples include media-type `encoding`, OpenAPI
`content`-encoded parameters or response headers, complex numeric object
parameter/header serialization, and path/operation-level server overrides.
Nullable schemas combined with enum, `not` or composition are also unverified
because the validator cannot establish those null cases reliably.
The loader checks the full frozen document graph, so malformed, unsupported or
precision-unsafe rules on an unexercised operation can also prevent loading.
Those unused rules are not statically diffed between the two contracts.

When both HTTP schemas are absent or explicitly omitted, matching examples can
pass with `contract_check: "unavailable"` in `matches`. That marker describes
example-only assurance rather than a successful schema check; the text report
states the example-only count.

### The gRPC examples agree, but Protobuf declarations differ

`protobuf_contract_mismatch` means observed wire checks passed but the selected
method's input/output declarations disagree. Both examples can use `READY = 1`
while one enum also declares `PENDING = 2`. An unused reachable field can also
change its kind, number, cardinality, presence, oneof, map or default without
changing the captured JSON. Full reachable enum sets and field rules are
compared, so those changes can fail conservatively.

Inspect the selected method's frozen request/response graphs. Unrelated
services, methods and messages are not diffed; declaration order is normalized.
Changing the preferred enum label among aliases for one numeric value remains
drift, as do syntax or enum open/closed semantics.
A safe addition may still require investigation under this equality policy.
`provider_wire_mismatch` instead identifies an observed cross-decoding failure;
`protobuf_contract_unavailable` identifies a declaration comparison that cannot
be completed reliably.

A verified gRPC match has
`contract_check: "protobuf_rules_and_observed_wire"`. Custom validation options
are compared as opaque declaration bytes, not executed. Equal custom max-length
annotations do not prove the observed string satisfies them, and plain Protobuf
does not declare string or collection length bounds. Keep validation assertions
in the test suites; captured length/value differences still fail the ordinary
example comparison.

### A collection is stale or does not match its archive

The checker verifies collection attempt IDs and saved interactions against the
raw archive. A collection from another run, a hand-edited response, a missing
attempt or an incomplete file set cannot replace the source evidence. Point
both inputs to the intended versions, then regenerate from those exact archives:

```sh
wiretap export --bundle ./consumer-run --format postman --output-dir ./consumer-run/exports/postman
wiretap export --bundle ./provider-run --format postman --output-dir ./provider-run/exports/postman
```

Keep the original contracts and payloads. Regeneration cannot recover missing
raw evidence; restore the complete archive when integrity checks fail. Directory
inputs discover `*.postman_collection.json` recursively. Supply
`--consumer-collection` or `--provider-collection` explicitly when the files
live elsewhere. Native gRPC replay URLs and source-schema settings may differ;
HTTP collection URLs must still agree with their own archive. Request/response
evidence must agree with the archive in both cases.

### The check failed and no JSON report appeared

Invalid flags, missing archives, corrupt evidence and missing or stale
collections return exit `2` with a diagnostic on stderr. They may prevent the
checker from constructing a report, even with `--format json`. An output write
failure can also prevent a new report. Preserve the command's exit status and
stderr; use a fresh output path so an old file cannot be confused with this run.
See the [CI gate example](ci-example.md#gate-consumerprovider-assumptions-offline).

The output's parent directory must exist. `--output` must be outside both input
archives and supplied Postman directories, and cannot replace a collection
file. Incomplete runs, unverifiable provider identities and an empty consumer
selection instead produce an unverified result before collections are required.

### The check passed, but an assertion or expected mock call is missing

The checker compares recorded examples; it does not inspect assertion code or
infer which calls an application should make. A test can capture a response
without asserting its contents. A missing dependency call produces no exchange
to compare, and an otherwise nonempty selection can still pass. An entirely
empty selection is rejected, but that is not an expected-call assertion.

Keep behavior assertions and expected mock-call counts in your test framework.
Capture those scenarios and rerun the check after the assertions pass. A green
comparison supports the observed examples, not unrecorded execution paths,
state transitions or all behavior of the tested application.
