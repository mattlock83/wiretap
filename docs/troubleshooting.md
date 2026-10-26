# Troubleshooting

## Runtime or routing problems

### The SDK cannot find the Wiretap binary

Build the local runtime as described in [installation](local-release.md), then
put it on `PATH` or set `WIRETAP_BINARY` to its absolute path. The Jest adapter does not
download or build executables.

### The control service or proxy will not start

Managed Jest chooses available local ports. For an external runtime, check port
conflicts and `WIRETAP_HTTP_LISTEN` / `WIRETAP_PROXY_LISTEN`. The
process must be allowed to bind local ports. A healthy control API at `/healthz`
does not mean capture has started; test setup or `startRun` starts the proxy.

### No traffic is captured

Point the shared client at `process.env.WIRETAP_SUT_URL`, for example `supertest(process.env.WIRETAP_SUT_URL || app)`. Configure `sut.url` for an already running API, or `app` in the Jest wrapper for an in-process handler. Calls directly to the application bypass Wiretap. Capture includes per-test hooks and test callbacks; suite-wide hooks are outside individual attempts.

### Downstream dependency traffic is missing

Declare the dependency and configure the application's client with the session's returned capture URL, such as `http://<wiretap-proxy>/__wiretap/dep/<asset-id>`. Calls directly to the mock bypass capture. Managed Jest chooses ports dynamically, so read the returned address through the adapter's session hook.

Traced parallel sessions share their capture address and require inbound-to-outbound W3C
propagation through the application. Untraced explicit sessions need isolated,
session-scoped routes. In-process mocks that never send traffic to the proxy
cannot be captured. See [tracing](otel-adoption.md#choose-a-capture-model).

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
Jest skips per-test hooks for `.concurrent`;
put concurrent fixtures inside the callback. Suite-wide hooks do not belong to
an individual attempt. See the [Jest guide](../sdk/js/docs/jest.md).

### Capture ended during a crash or timeout

Ordinary assertion failures still finalize exports. The Jest adapter holds a stdin lifeline to its child runtime. If the owning test process
disappears, EOF requests graceful shutdown so the runtime stops and preserves
an interrupted raw archive with pending attempts.

Abrupt termination cannot finish normal exports or save the owner's completion
receipt. The archive's `last-run.json` can be absent; inspect its `bundle.json`
to determine whether capture completed or was interrupted.
Retain the full output directory and runtime log. If Wiretap itself is killed
before graceful shutdown finishes, its archive can remain open. Normal managed
shutdown waits for the child only up to its shutdown timeout, then force-kills it.

### Teardown reports incomplete capture or `finishRun` returns 409

Await expected requests and background work before teardown. Accepted requests
drain for five seconds by default; `drainTimeoutMs` can allow deliberately slow
requests. A timeout records a failed, incomplete attempt. Late completions are
never added to a later test.

One owner must finalize a shared run after every worker has torn down its
attempts. The Jest global teardown owns shared finalization. `finishRun` stops
the proxy and finalizes the bundle; the control process stays available.

## Schemas and reports

### Setup requires an OpenAPI contract

Set the main HTTP API's `schema` in `.wiretap.yaml`, or supply it through the
SDK/API. Its explicit `schema_required: false` opt-out remains available. HTTP
dependencies need no spec by default: add one to opt into validation and
coverage, or set `schema_required: true` to enforce availability. Supplied specs
still validate even with `schema_required: false`. Omitted schemas leave
contract coverage unknown while calls remain captured. Arazzo export still requires eligible complete JSON/REST evidence.

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

### Provider identity or version does not agree

Pass the intended complete archives using `--consumer` and `--provider`, or
both run-set flags. A declared consumer `provider_mappings` entry must name the
supplied provider application's ID and API ID. A version pin must match the
provider's captured `sut_source.version`. Missing version metadata cannot
establish agreement.

Restore the intended build's complete archive and Arazzo exports, including
`tests/`, `contracts/` and `payloads/`. Extract ZIP/TAR files before checking.
Wiretap does not choose snapshots or fetch missing evidence. See
[snapshot selection](assumption-checks.md#share-provider-snapshots).

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
body values and response headers may also differ.

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

This declaration comparison applies when both sides supplied HTTP contracts.
Inspect the `contract/request/...` or `contract/response/...` difference and the
frozen contracts. This is conservative rule equality: an added enum value,
relaxed bound or equivalent schema expressed differently can fail without
proving a runtime break. Align the intended contracts and recapture after the
change is resolved. Descriptions, examples, defaults and enum/required ordering
are ignored; unused operations, statuses and media alternatives are not diffed.

`http_contract_violation` instead means a checked message violates the
applicable schema. With only a provider schema, both exchanges use that
contract; with both schemas, each exchange uses its own. Each response is
validated, and requests are validated when their response status is below
`400`. Intentional negative request examples with
status `400` or higher skip request-value validation, while their request rules
are compared when both contracts are supplied. Response values remain checked.
A passing framework test does not override
these contract findings.

### The HTTP contract check is unavailable

`http_contract_unavailable` means a required contract or rule cannot support
assurance, for example a missing provider schema while the consumer supplies
one, OpenAPI 3.1, or numeric rules that cannot be evaluated reliably. Supply a
supported frozen OpenAPI 3.0 provider contract; a consumer dependency spec is
optional. Recapture if necessary. Restore corrupt or missing archive
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

A consumer HTTP dependency needs no schema by default. If the provider supplies
OpenAPI 3.0, a verified match uses `openapi_3_0_provider_contract`: both concrete
exchanges validate under the provider's schema, with no independent consumer
declaration comparison. The original consumer archive still has unknown
contract coverage. A provider spec without a matching passing provider test
cannot turn an uncovered request into a verified one.

### An Arazzo workflow is stale or does not match its archive

The checker regenerates workflow content from the complete archive and verifies the selected document and source companions. A document from another run, hand-edited request, altered source or missing eligible workflow cannot replace the evidence. Regenerate from the exact intended archives:

```sh
wiretap report --bundle ./consumer-run --format arazzo --output-dir ./consumer-run/exports/arazzo
wiretap report --bundle ./provider-run --format arazzo --output-dir ./provider-run/exports/arazzo
```

Keep contracts and payloads. Regeneration cannot recover missing evidence; restore complete archives when integrity checks fail. Explicit pairs accept `--consumer-arazzo` and `--provider-arazzo`; run sets use each run's `exports/arazzo/arazzo.json`. Strict whole-run export can fail for incomplete or unrepresentable eligible traffic even on another boundary or an excluded provider attempt. Verification uses the archives for actual response evidence and does not execute workflow expressions. See [Arazzo limits](assumption-checks.md#arazzo-check-inputs).

### The check failed and no JSON report appeared

Invalid flags, missing archives, corrupt evidence and missing or stale
Arazzo inputs return exit `2` with a diagnostic on stderr. They may prevent the
checker from constructing a report, even with `--format json`. An output write
failure can also prevent a new report. Preserve the command's exit status and
stderr; use a fresh output path so an old file cannot be confused with this run.
See the [CI gate example](ci-example.md#gate-consumerprovider-assumptions-offline).

The output's parent directory must exist. `--output` must be outside both
archives and the selected Arazzo export trees, and cannot replace a run-set manifest. It cannot replace a workflow or source companion. Once a valid pair is selected, incomplete
runs, unverifiable provider identities and zero captured calls to the selected
mock instead produce an unverified result before Arazzo verification.

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
