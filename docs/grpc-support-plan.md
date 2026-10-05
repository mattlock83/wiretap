# gRPC support implementation plan

Status: native gRPC unary and streaming implementation reviewed and completed
against the A–C handoff on 2026-10-05. The original design is retained below as
an audit checklist; current scope and verification are summarized here.

### Review status

All four native RPC shapes use incremental bounded observation, exact frame
counts, per-message timing, explicit omissions and immutable lifecycle snapshots.
Provider, consumer and mixed paths support h2c and TLS. Parallel capture uses
ordinary W3C trace propagation; sequential capture needs no application tracing.
The review fixed cancellation of an open bidirectional request during run shutdown.

Reflection v1/v1alpha remains the default, with local proto sources and descriptor
sets available when reflection is absent. Frozen contracts support offline bundle
verification and report rebuilding. Native grpc-go, Connect in gRPC mode and Node
`@grpc/grpc-js` clients are exercised without a mandatory Wiretap RPC client.

Rich `google.rpc.Status` errors retain a verified envelope and ordered typed or
opaque details. Contract-local types and separately frozen standard error types
resolve offline. Redaction removes containing wire/envelope copies. Decode,
retention and recursion budgets produce explicit unavailable evidence.

The `wiretap-protobuf-2` measurement profile covers declared methods and supported
field conditions in every complete message, including streams. It preserves
presence, oneofs, enums, booleans, collections and nested message conditions.
Incomplete, omitted, redacted or undecodable evidence receives no successful
coverage credit. Empty streams do not invent payload conditions. Protobuf does
not declare allowed status codes, stream lengths or cross-message business rules;
those do not become artificial coverage denominators. Recursive scopes, dynamic
`Any` values and arbitrary custom validation semantics remain qualified limits.
Descriptor normalization stays `wiretap-protobuf-1`.

Bundle evidence is **3.1**, with mandatory `grpc-observation.v1` and
`grpc-status-details.v1` capabilities when those structures are present. Earlier
3.x evidence remains readable without acquiring guarantees it never recorded;
readers that do not understand a required capability reject it. The generic
interaction envelope remains available for future protocol adapters; native gRPC
uses its typed message evidence, not duplicate generic payloads.

### Implementation and verification record

A, B and C below are implemented. Their checklists describe the delivered design;
the later original work packages remain historical audit material. Regression
coverage includes all four RPC shapes across provider/consumer/mixed boundaries,
TLS, reflection/local contracts, framing fragmentation, empty messages, gzip,
limits, cancellation, parallel ownership, rich errors, redaction, integrity and
offline rebuilding. The Node companion includes serial and parallel Go/Node and
HTTP/gRPC paths, metadata, trailers-only failures, deadlines and TLS.

Keep capture integrated with the user's test lifecycle. No mandatory Wiretap
RPC client, application-specific trace IDs, automatic client instrumentation,
reflection requirement, or new test framework. Keep payload attachments as
hashed `.bin` files. gRPC exploration, Connect's wire protocol, gRPC-Web, mTLS
and semantic validation engines remain separate follow-ups.

### A. Native Node interoperability and adoption example

**Starting points:** `examples/grpc-boundaries/run.sh`, its `server/main.go` and
`proto/` fixtures; `sdk/js/src/index.js`, `sdk/js/test/existing-otel.test.js`,
`internal/app/grpc_integration_test.go`. Existing tests prove grpc-go and a
Connect client in native gRPC mode. JavaScript lifecycle tests alone do not
exercise the native Node gRPC stack.

- [x] Add an optional `examples/grpc-boundaries/node/` package with a lockfile,
  `@grpc/grpc-js`, and either ordinary generated clients or `@grpc/proto-loader`
  loading the checked-in protos. Pin tested dependencies in the lockfile. Keep
  gRPC and OTel gRPC instrumentation dependencies in the example, not mandatory
  dependencies of the JavaScript SDK. If generating clients, check in the
  generated files and a regeneration command; normal runs must not need protoc.
- [x] Run the example through Node's existing test lifecycle and the JS SDK.
  Reuse the current servers and capture addresses from `startRun`; retain the
  default Go example without a Node requirement. Add an explicit Node mode or
  companion runner that fails clearly if dependencies are missing. Document
  installation, execution, output path and cleanup; do not silently download
  packages inside an ordinary test run.
- [x] Exercise Node as both the test client and an existing dependency server
  (mock) against the Go fixture. Include gRPC provider→gRPC mock and an HTTP
  provider→Node gRPC mock case. Use a local proto or descriptor source for the
  Node mock if it does not expose reflection; do not require reflection solely
  to make this example work.
- [x] Show serial capture without trace propagation. Separately initialize
  standard OTel gRPC instrumentation **before importing** the client/server
  modules and prove parallel ownership through a provider and its outgoing
  dependency client. Instrument the fixture provider normally or use a Node
  provider; the current serial Go example does not propagate context by itself.
  Preserve an existing provider/context manager, use W3C propagation, and do
  not require a collector or replace the user's HTTP/gRPC clients.
- [x] Test a reused multiplexed channel with at least two overlapping tests,
  success, trailers-only non-OK status, repeated/binary metadata, a valid empty
  message, deadline/cancellation, and TLS with a local CA. Compare results seen
  by the same native client directly and through Wiretap; request paths and
  application payloads must match.
- [x] Assert canonical bundle evidence, not only the client's result: correct
  test/flow ownership and boundary roles, frozen schemas, preserved numeric RPC
  status including zero, payload attachments, and no reflection calls credited
  as test traffic. Run without reflection using local contracts, then rebuild
  after upstream shutdown and removal of the source files.

**Done when:** a documented command runs the Node fixture from a clean checkout
after dependency installation, all cases above pass, and its README states which
fixture processes require Go. Add the fixture to a
repeatable verification command or CI job. The delivered Node companion runs in
`.github/workflows/grpc-node.yml`. Update `sdk/js/README.md`, the gRPC user
guide and examples index with the tested Node/OTel setup and requirements.

### B. Decode rich RPC errors without leaking duplicate values

**Starting points:** `internal/proxy/grpc.go` captures terminal metadata;
`internal/app/grpc_record.go` builds RPC evidence;
`internal/grpccontract/contract.go` provides the contract-local type registry;
`internal/artifact/types.go`, `internal/bundle/grpc.go`, `internal/redact/redact.go`
and `internal/bundlereport/grpc.go` own persistence, privacy and presentation.
The pinned Go module graph already includes `google.golang.org/genproto/googleapis/rpc`.

- [x] Add a shared bounded decoder for `grpc-status-details-bin` as
  `google.rpc.Status`. Accept valid padded/unpadded binary-metadata base64;
  handle headers versus trailers consistently with `grpcTerminalStatus`.
  Duplicates, malformed envelopes and contradictions with the terminal
  `grpc-status` must produce explicit diagnostics, not an assumed successful
  decode. Never change bytes delivered to the native client.
- [x] Resolve each `Any` against the frozen boundary registry. When standard
  `google.rpc` error-detail types are absent, freeze their pinned descriptors
  as a separately hashed auxiliary descriptor set and reference it in the bundle.
  Preserve supplied contract bytes and semantic revisions; do not quietly add
  runtime helper types to the service contract's identity. Prefer boundary
  definitions and reject conflicting auxiliary definitions. A custom type in
  the contract decodes locally; unknown type URLs remain opaque with a reason.
  Do not depend on a global application registry, fetch a type URL, contact a
  registry or perform fresh reflection while recording/reporting.
- [x] Add explicit response error-detail evidence: the envelope's numeric code
  and message, plus ordered details with index, type URL, fully qualified type
  when resolved, raw attachment reference, readable ProtoJSON projection and
  decoding/redaction/retention state. Distinguish absent details from an empty
  `Status`, undecodable data and omitted data. The terminal RPC status remains
  authoritative; detail content does not invent a second successful outcome.
- [x] Extend attachment identity to distinguish a status envelope/detail from
  an application message. Keep a single verified raw source per representation
  and verify derived JSON against it and the frozen descriptors on bundle load.
  Reject duplicate/mismatched indices, references and projection tampering.
  Do not overload ordinary response-message indices or count details as output
  messages when checking RPC cardinality or field coverage.
- [x] Share the existing 64 KiB/256-entry metadata budget with the raw status
  envelope. Bound decoded projection bytes to 64 KiB per response, detail records
  to 256, and recursion depth to 100; stop with explicit unavailable/truncated
  markers on exhaustion. These are observation limits, not upstream delivery
  limits. Count duplicate representations against retention limits instead of
  creating an unbounded decoded copy beside bounded raw metadata.
- [x] Define `response.status_details[*].body.<proto_field>` selectors, including
  repeated/map fields, and document selection by index. Apply a rule to the
  decoded projection and remove **every original containing representation**:
  the selected detail bytes, raw `google.rpc.Status` envelope, and the binary
  metadata value in both headers and trailers. Redacting the status message
  must also remove/mask its scalar and envelope copies. For an undecodable or
  unknown selected detail, omit it safely; never retain it because matching
  could not be evaluated. Keep current whole-value header/trailer redaction.
- [x] Render typed details beneath the RPC outcome in Markdown, knowledge and
  the viewer, linked to the same test and flow. Label opaque, truncated and
  redacted states. Do not create error-detail coverage targets: a service
  declaration does not enumerate allowed rich errors, and capturing a mock's
  detail is still an assumption rather than provider agreement.

**Acceptance tests:** native grpc-go `status.WithDetails` with a standard
`BadRequest` and a custom local `Any`; multiple ordered details; unknown `Any`;
invalid base64/protobuf; inconsistent status codes; repeated metadata; oversized
details; and trailers-only responses. Compare direct/proxied client results.
Round-trip/rebuild offline, reject tampered attachments/projections, and scan
all bundle files and rendered views for a distinctive redacted secret—including
its base64 representation. A passing negative test must retain its error outcome
without falsely covering a successful application response message.

### C. Complete streaming evidence and coverage

**Original starting point:** the proxy retained a bounded raw prefix and parsed
frames only at completion. This has been replaced by incremental observers in
`internal/capture/grpc.go`, retaining a bounded prefix while counting all observed
frames. The implementation extends the existing
`internal/app/grpc_lifecycle.go` drain/once-only commit mechanism instead of
creating a second session or stream ownership system.

#### C1. Incremental observation and explicit accounting

- [x] Implement an envelope observer under `internal/capture` that consumes
  arbitrary byte chunks while forwarding. Track the five-byte header, remaining
  payload length, compression flag and direction-local index incrementally.
  Skip unretained payload bytes without allocating the advertised length. Never
  buffer an entire stream or change backpressure, flushing, half-close, retries
  or cancellation behaviour to improve evidence.
- [x] Record timing at observation time: message-start and complete-frame offsets
  from the RPC's monotonic start, alongside the flow's UTC start. A zero-byte
  message completes when its envelope arrives. Do not stamp every message at
  teardown or claim cross-direction observation order proves causal order.
  Incomplete frames have a start but no fabricated completion timestamp.
- [x] Extend each direction with complete frames observed, messages retained,
  complete frames omitted, observed bytes, count completeness, EOF/completion
  state and any partial-frame state. `observed_bytes` includes envelopes; record
  per-message wire payload length separately. Keep counting valid envelopes
  after retention stops, including zero-byte messages; count an oversized but
  fully forwarded frame once even if none of its payload is retained.
- [x] Define counts precisely: complete observed frames = retained complete
  frames + omitted complete frames. A partially received frame is separate.
  Malformed framing means subsequent counts are unknown; do not guess where to
  resume. Count completeness describes the interval actually observed, not
  traffic that might have occurred after cancellation or a committed snapshot.
- [x] Keep the current finite byte, decoded-byte, metadata and 1,024-record
  direction budgets. Preserve original wire indices when records are omitted;
  use a bounded aggregate omission summary rather than one record per dropped
  message. Keep parse/decode work bounded and unknown/unsupported compression
  opaque. An empty-message flood must not grow retained memory with duration.

#### C2. Immutable lifecycle and verified persistence

- [x] Keep one admitted flow ID and owner for the entire invocation. Snapshot
  observer state safely while each direction is active. Drain expiry commits
  that bounded incomplete snapshot exactly once; a later upstream result cannot
  mutate it or appear under the next test. `finishRun` and shutdown cancel and
  release retired work within the existing grace period, including h2c channels.
- [x] Persist direction completeness separately from terminal RPC status. A
  complete, expected non-OK application result can belong to a passing test;
  a client/proxy cancellation, missing status, partial frame or omitted evidence
  cannot be relabelled as a complete provider result. Do not infer request EOF
  merely because an early response completed.
- [x] Update `GrpcMessage`/`GrpcPayload`, their codec, bundle loader and evidence
  schema together. Retention stops permanently at the first byte/record limit,
  so retained indices remain a dense wire prefix. Validate the contiguous omitted suffix,
  monotonic unique timing sequences and original wire indices. Enforce counter/timing/completion
  consistency and bounded attachment counts. Missing timestamps/counters stay unavailable; never reconstruct them
  from retained array length or report-generation time.
- [x] Preserve known message type, raw/compressed bytes, ProtoJSON, flags and
  exact `(run, attempt, flow, direction, message index)` references. Use the
  same redaction rules on every retained message and remove raw duplicates.
  Update the once-only drain and bundle-integrity tests before enabling coverage.

#### C3. Stream measurement and views

- [x] Extend `internal/app/schema_validation.go` and
  `internal/coverage/contract_grpc.go` to evaluate every retained complete message
  with `protoreflect` and the frozen contract. Preserve explicit presence,
  numeric precision and unknown/custom-option limits. Remove the unary-only
  gates in `internal/coverage/report.go` only after the transport matrix passes.
- [x] Define cardinality by descriptor: unary has one request and one response
  on OK; server streaming has one request and zero or more responses; client
  streaming has zero or more requests and one response on OK; bidirectional
  streaming has zero or more in each direction. Non-OK termination can omit a
  result but cannot justify extra messages on a unary side. Missing required
  unary messages are not the same as a valid zero-byte message.
- [x] Credit method and payload conditions only from complete, passing RPC
  evidence with valid terminal status, structurally valid messages, and no
  truncation/omissions/redaction/decode or transport uncertainty in the affected
  scope. Retain incomplete observations with reasons. A valid empty stream can
  exercise a streaming method but covers no payload conditions in that direction.
  Do not invent a denominator for stream lengths, message combinations or
  cross-message business invariants.
- [x] Attach field observations and combination fingerprints to their exact
  message indices. Render one RPC with expandable ordered request/response
  messages, completion and omission summaries. Keep Markdown, JSON knowledge,
  exports and static viewer consistent. Do not flatten streams into the first
  message or count each message as an independent test or RPC.

**Acceptance tests:** add checked-in fixtures for all four RPC shapes with
reflection on/off and imported types; generated test code must not require
protoc at test time. Exercise early server/bidirectional responses before request
EOF, concurrent send/receive, half-close, no messages, zero-byte messages,
fragmented/coalesced envelopes, gzip, unsupported compression, oversized frames,
slow readers, deadlines, explicit cancellation, trailers-only failures, and
upstream disconnects mid-header/mid-payload. Include a stream exceeding both
byte and record budgets, with counted empty messages beyond the retained prefix.
Assert bounded retention, exact counters/indices, timestamps captured during
traffic, direct/proxied client equivalence and no successful credit from partial
evidence. Race-test parallel traced streams sharing one channel, teardown drain
expiry, shutdown and finish while streams run; verify saved attempt bytes remain
unchanged after late completion. Rebuild copied bundles offline and verify
per-message links/redaction across all views before advertising streaming support.

### Evidence versions, delivery order and verification

1. Land A as an independently runnable interoperability fixture without changing
   evidence semantics. Land B with its decoder, privacy and persistence tests
   together; do not expose readable details before duplicate-source redaction.
2. Land C1–C2 with streaming coverage still marked unsupported, then C3 with the
   full stream matrix. Keep incomplete/unknown scope visible throughout.
3. Evidence 3.0 was established by the separate bundle-format work. This
   implementation writes **3.1**, with required capabilities for new authoritative
   detail and observation structures. Writer, reader, JSON Schema, integrity tests
   and docs move together. Capability checks make older readers reject unsupported
   semantics. An accepted earlier bundle never gains timing/count guarantees it
   did not record; no migration layer is needed.
4. Keep HTTP `wiretap-contract-1` identities unchanged. Version the Protobuf
   measurement profile when streaming measurement changes its semantics;
   document that comparison requires matching profiles. Do not change descriptor
   normalization just to add message timing or rich error projections.
5. Update `docs/grpc-user-guide.md`, `docs/bundle-evidence.md`, its JSON Schema,
   `docs/contract-coverage-rules.md`, `docs/bundle-knowledge.md`,
   `docs/okf-bundles.md`, SDK/example READMEs and site support claims to match
   verified behaviour. Update this review status with commands and remaining
   exceptions; a checkmark requires evidence, not just new code.

Run focused packages while developing, then the release checks below from the
repository root. The Node companion requires dependency installation as documented
in its README; its runner never silently installs packages.

```bash
go test -race ./...
npm --prefix sdk/js test
./examples/grpc-boundaries/run.sh
WIRETAP_GRPC_CONTRACT_SOURCE=proto ./examples/grpc-boundaries/run.sh
WIRETAP_GRPC_CONTRACT_SOURCE=descriptor_set ./examples/grpc-boundaries/run.sh
WIRETAP_GRPC_CONTRACT_SOURCE=mixed ./examples/grpc-boundaries/run.sh
./examples/grpc-boundaries/run-node.sh
./examples/grpc-streaming/run.sh
git diff --check
```

Verified on 2026-10-05: the full Go race suite, JavaScript SDK suite, all four
original contract-source modes, the Node companion (5 groups, 15 attempts), and
the streaming example (8 cases) passed. Live integration tests cover all four RPC
shapes across provider, consumer and mixed paths, including TLS and parallel
ownership. Bundle tests verify offline rebuilding, attachment integrity,
redaction, omissions and rejected tampering. One existing JavaScript test remains
marked skipped by that suite.

Inspect actual generated bundles as well: each declared boundary has one resolved
contract snapshot, observed statuses include `OK (0)`, payloads and descriptors
verify, provider evidence and mock assumptions remain separate, and offline
`wiretap report --bundle <path>` yields matching counts and evidence links.

## Outcome

Teams keep their existing gRPC clients, test framework, application and mocks.
They route test and dependency calls through Wiretap, resolve their Protobuf
contracts, and receive the same OKF bundle of tested APIs, dependency assumptions
and coverage that HTTP users receive today.

**Prefer server reflection by default for standalone development instances.**
When a gRPC API has no explicit schema input, fetch its contract from that
upstream's reflection service. Teams can instead supply `.proto` sources or an
existing descriptor set, so reflection is never required. All three inputs
produce a self-contained frozen `FileDescriptorSet` in the bundle. A contract is
mandatory for every configured gRPC provider and dependency, including mocks.
There is no schema-less gRPC capture mode or silent fallback to one.

The [gRPC user guide](grpc-user-guide.md) describes the implemented setup and
its current limits.

Capture remains a library integration controlled by the existing HTTP lifecycle
API. This work does not introduce a test framework, require a Wiretap RPC client,
or require application-specific trace metadata. Existing W3C trace propagation
supports parallel capture; sequential capture or isolated worker runtimes remain
the fallback when the application does not propagate context.

## Decisions and scope

| Area | Decision |
| --- | --- |
| Current release | Native gRPC with Protobuf messages: all four RPC shapes, contract inventory, structural validation, bounded condition coverage, OKF/knowledge/viewer output. |
| Transport | Direct reverse capture endpoints using HTTP/2, with h2c and TLS. Preserve the RPC procedure path. |
| Dependencies | A direct capture address per gRPC dependency; keep existing mocks and their generated clients. |
| Schema | Mandatory resolved Protobuf contract per gRPC boundary. Reflection, local `.proto` sources and precompiled descriptor sets converge on the same frozen descriptor model. |
| Reflection | Default source when no schema is configured. Query the configured upstream once per run before test admission, with bounded requests; reports never query it. |
| Source compilation | Compile local `.proto` files inside Wiretap with Buf's `protocompile`; users do not need to install Buf/protoc or generate Wiretap clients. |
| Streaming | Implemented for all four RPC shapes, with explicit evidence quality, message timing, bounded retention and structural coverage. |
| Validation | Observe and report contract violations at test finalization, as today. Never reject or rewrite an application message because passive validation failed. |
| Reports | Extend existing bundle evidence, coverage, OKF, knowledge and static viewer; no separate RPC reporting pipeline. |
| Exploration | Remains HTTP-only initially. Preserve explicit rejection of unsupported provider sequences. |
| Other protocols | The Connect wire protocol and gRPC-Web are separate follow-ups, even though ConnectRPC supports them. |

Initial exclusions: HTTP CONNECT interception of gRPC, mTLS identity forwarding,
automatic BSR downloads, automatic protocol translation,
custom protobuf codecs, inferred `.proto` generation,
cross-repository comparison, and gRPC invalid-input generation.

In-process mocks or transports such as Go `bufconn` cannot be observed by an
external binary unless the harness exposes a network endpoint. Document this
limit explicitly. A configurable network address/trust setting is sufficient
for ordinary clients; Wiretap does not require changing application business code.

## Where ConnectRPC helps

ConnectRPC for Go is compatible with native gRPC clients and servers. Its client
defaults to the Connect protocol, so native gRPC calls must explicitly use
`connect.WithGRPC()`. It also offers dynamic message initializers and codecs.
These make it useful for interoperability fixtures and a later descriptor-driven
replay client. Its `connectrpc.com/grpcreflect` companion provides a production
reflection client for the default contract source. These libraries do not remove
the need for schemas or supply Wiretap's capture, correlation and evidence model.
See [gRPC compatibility](https://connectrpc.com/docs/go/grpc-compatibility/)
and the [Connect Go API](https://pkg.go.dev/connectrpc.com/connect).

Use `grpcreflect.NewClient` with native gRPC and the configured HTTP/2 transport.
It supports the v1 reflection service with v1alpha fallback when v1 is
unimplemented. Keep authentication failures distinct from protocol-version
fallback. Buf's separate `protocompile` library compiles source files into
descriptors; Connect itself does not compile `.proto` files.
See [grpcreflect](https://pkg.go.dev/connectrpc.com/grpcreflect)
and [protocompile](https://github.com/bufbuild/protocompile).

**Recommended capture design: forward HTTP/2 bytes and observe a bounded copy.**
Do not make the capture path a generated or dynamic Connect service which
unmarshals and re-marshals every call. Wiretap needs to observe unknown methods,
malformed payloads, unknown fields, compression it cannot decode, and oversized
messages without changing what reaches the upstream. Connect handlers can
validate framing/codecs before application handlers run; their message limits
are delivery limits, whereas Wiretap's limits bound retained evidence.
This separation is a design choice, not a limitation of Connect's ability to
serve gRPC. See [serialization](https://connectrpc.com/docs/go/serialization-and-compression/)
and [message limits](https://connectrpc.com/docs/go/deployment/#message-size-limits).

Use `net/http` HTTP/2 support and preferably `httputil.ReverseProxy` for forwarding,
with a small passive gRPC envelope observer. Do not implement HTTP/2 framing.
Use `google.golang.org/protobuf` descriptors and `dynamicpb` for inspection.
Use Connect for reflection acquisition and interoperability, while keeping
application forwarding independent of RPC handlers. Native `grpc-go` fixtures
are also required so the tests do not merely prove Connect can talk to itself.

The repository already targets Go 1.24, which introduced `http.Protocols`,
including explicit unencrypted HTTP/2 support. Verify compatible dependency
versions at implementation time and pin them; an upgrade of the minimum Go
version is a separate decision. See [Go 1.24](https://go.dev/doc/go1.24#net/http)
and [ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy).

## User setup

These examples describe the implemented project configuration.

### 1. Declare the boundaries; reflection is the default

Add `protocol: http | grpc` to each API, defaulting to `http`. Preserve the string
`schema` path for HTTP/OpenAPI. For gRPC, omit `schema` to use reflection, or use
the explicit source object below. Do not guess source type from a file extension.

```yaml
version: 1
output_dir: artifacts
http_listen: 127.0.0.1:18000
proxy_listen: 127.0.0.1:18080

sut:
  id: orders
  protocol: grpc
  url: http://127.0.0.1:50051
  kind: service
  services: [orders.v1.OrdersService]

dependencies:
  inventory:
    protocol: grpc
    url: http://127.0.0.1:50052
    capture_listen: 127.0.0.1:18081
    kind: mock
    services: [inventory.v1.InventoryService]
```

Both services in this example must expose reflection, but neither needs local
proto files configured. `services` optionally narrows reflection to those fully
qualified names. When omitted, discover the advertised services and exclude
exactly `grpc.reflection.v1.ServerReflection`,
`grpc.reflection.v1alpha.ServerReflection` and `grpc.health.v1.Health` by default.
Explicit selection may include those services. Record the returned inventory,
selected services and exclusions; fail clearly if the selection is empty or a
named service cannot be resolved. Coverage is scoped to that snapshot, not a
claim that every possible deployed API was discovered.

For `.proto` and descriptor-set sources, require an explicit nonempty `services`
list so unrelated services in imported files do not inflate the denominator.
All selected methods appear even if no traffic exercises them. Calls outside
the selected inventory remain visible as undeclared operations.

The SUT capture endpoint uses `proxy_listen`; gRPC dependencies use
`capture_listen`, defaulting to `127.0.0.1:0`. Reject collisions. Paths are resolved
relative to `.wiretap.yaml`, with the same environment expansion as other config.
Reject a gRPC upstream URL with credentials, a query, fragment, or base path.
Use `http://` for prior-knowledge h2c and `https://` for HTTP/2 over TLS.

Provide optional `capture_tls: {cert_file, key_file}` and
`upstream_tls: {ca_file, server_name}` on gRPC APIs. For the SUT, capture TLS
configures its main capture listener; for a dependency, its own listener.
Require both certificate and key, validate upstream certificates, negotiate
`h2`, and fail clearly on an HTTP/1-only upstream. Do not reuse an insecure TLS
setting or downgrade silently. Certificate/private-key paths are runtime
configuration and never copied into bundles. mTLS remains explicitly unsupported.

Protobuf contracts are mandatory for gRPC. Reject `schema_required: false` through
configuration and the HTTP API. Absence of a gRPC `schema` means reflection, not
an omitted contract. Failure to resolve the chosen source blocks test admission.
Keep the existing schema opt-out available only for HTTP APIs. Never use observed
messages to invent a descriptor or coverage denominator.

### 2. Select another source or configure reflection

Each boundary selects exactly one source; explicit configuration takes precedence
over the reflection default. Reject unknown/mixed source fields and a bare gRPC
schema string with an example of the correct object shape. This is a new API;
there is no legacy gRPC spelling to support. Only an absent `schema` selects the
default; explicit `null`, `{}` or an empty source is invalid. Require a positive
bounded reflection timeout; zero must not create an unbounded discovery call.

```yaml
# Optional settings for an authenticated reflection endpoint.
# With no schema block, reflection uses a 10s timeout and no extra headers.
schema:
  source: reflection
  timeout: 10s
  headers:
    authorization: ${REFLECTION_TOKEN}
```

The token variable contains the full header value, such as `Bearer ...`.
Reflection uses the API's configured upstream URL and upstream TLS settings;
headers apply only to schema discovery, not application requests. Resolve them
from the runtime environment and never persist or echo their values in control
API defaults, diagnostics or logs. Never reuse a captured test's token. No alternate
reflection endpoint or automatic cross-target lookup in the initial release.

```yaml
# Compile existing sources inside Wiretap, with no external compiler process.
schema:
  source: proto
  files: [orders/v1/orders.proto]
  import_paths: [./proto, ./third_party]
services: [orders.v1.OrdersService]
```

`files` are logical proto filenames relative to the import roots. Import roots
are relative to the config directory; require a nonempty list for predictable
resolution. Supply external dependencies locally under those roots. Bundle the
compiler's standard imports, but do not resolve Buf remote dependencies, run
plugins, invoke subprocesses or fetch missing imports. Fail on missing imports
with the filename and configured search roots. Map descriptor input and compiled
sources to the same validation path.

```yaml
# Reuse a self-contained binary FileDescriptorSet from the team's build.
schema:
  source: descriptor_set
  path: build/api.protoset
services: [orders.v1.OrdersService]
```

The descriptor path is relative to the config file. Existing Buf users can create
one with `buf build --as-file-descriptor-set -o build/api.protoset`; protoc users
can use `--descriptor_set_out` with `--include_imports`. Retain custom option
definitions. Neither tool is required by the reflection or source-file modes.
See [Buf build](https://buf.build/docs/reference/cli/buf/build/).

Explicit file modes never query reflection. Reflection failure never switches to
files, a previous run's cache, or schema-less capture. Return an actionable error
offering the two local alternatives. Users can mix source types across APIs.

### 3. Keep the current lifecycle and clients

1. Start the binary using the team's existing harness.
2. Call `startRun` to allocate capture listeners and obtain their addresses. It
   records declared boundaries as unresolved and does not wait for reflection.
3. Start mocks and the SUT, configuring the SUT's dependency target as
   `127.0.0.1:18081` and the test's ordinary gRPC client as `127.0.0.1:18080` in
   this example. Wait for the upstreams to be ready.
4. The first `setupTest` resolves and freezes all declared contracts before
   admitting any test. Later tests reuse this snapshot. Call `teardownTest` from
   the framework's completion hook, as today.
5. Use standard trace instrumentation where available; finish the run normally.

Keep `sut_url` and the `dependencies` URL map in session responses; gRPC values
are direct origins with no path prefix. Return the configured dependency map
from `startRun` too, so the SUT can be started before any test attempt exists.
Document how a native client extracts `host:port` and chooses TLS/insecure
credentials from that URL. Connect clients can use the full origin.
The old `dependency_base_url` is an HTTP routing convenience, not a way to
construct gRPC targets; SDK examples must use returned addresses.

For API-only integrations, let `startRun` register the same boundary definitions
that project configuration supplies. In the first release, bind each gRPC target
URL, API identity, protocol/TLS settings, source and service selection to that
run. A per-test override cannot change these: start a fresh run to change the
boundary. This prevents applying a snapshot reflected from one endpoint to
another. Preserve existing HTTP per-session routing independently, and reject
conflicting concurrent registrations rather than rebind a live listener.
Establish routes at `startRun`, then freeze every declared contract at the first
admission barrier so unused methods remain visible. This order lets the SUT start
with dynamic dependency ports without a circular dependency on reflection.
Pre-test traffic forwards using run routes but is not recorded. Roll back new
listeners if their allocation fails. Schema-resolution failure leaves the run
open with unresolved inventory and diagnostics, no admitted test and no partial
success claim; a subsequent setup can retry after the cause is fixed.

Make schema resolution single-flight across concurrent first setup calls and
publish the complete snapshot atomically; never hold session locks during
discovery I/O. Persist each boundary's `contract_resolution` state as `pending`,
`resolved` or `failed`, with safe diagnostics on failure. Before a zero-test
`finishRun` can mark a run complete, resolve outstanding contracts while the upstreams still
run; on failure return an error and preserve non-complete evidence. Never omit an
unresolved boundary from the coverage denominator or report unknown as 100%.
Coordinate bounded discovery concurrency, source timeouts and control/SDK
request deadlines; existing fixed timeouts must not silently defeat supported
resolution budgets. Cancellation aborts discovery without half-admitting a test.

No special Wiretap client is needed. Go callers pass `Session.Context` to their
usual generated client with standard OTel gRPC instrumentation. Node callers run
their generated client inside the SDK's test context with standard gRPC
instrumentation loaded before the client module. Existing HTTP/Undici
instrumentation does not automatically instrument a gRPC client.
See [OTel's Go gRPC integration](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc).

Preserve existing instrumentation and avoid duplicate global providers or
interceptors. A collector is not required. Without end-to-end W3C propagation,
use one active test or one runtime/SUT/mock set per parallel worker. Do not add
a Wiretap-specific metadata key, require a new SUT interceptor, or assume a
gRPC connection identifies a test: many tests can share one multiplexed channel.

## Implementation design

### A. Descriptor loading and frozen contracts

Add a focused `internal/protobuf` package for contract acquisition, selecting
services, looking up methods/types, decoding messages and computing identities.
Keep it independent of proxy and HTML packages. All source adapters return the
same complete descriptor set, selection and acquisition provenance.

- Reflection adapter: query the configured upstream directly using
  `connectrpc.com/grpcreflect`, `connect.WithGRPC()` and the same TLS policy as
  forwarding. Use v1 then v1alpha only on unimplemented-service response, with
  a 10-second default overall timeout per boundary (including fallback/imports).
  Fetch advertised/selected service definitions and their complete import/option
  closure, with limits on files, bytes, queries and recursion. Treat conflicting
  definitions, incomplete closure or inconsistent responses as resolution failure.
  Accumulate returned descriptors within one reflection stream: responses can
  omit files already sent. A lost stream requires a fresh bounded acquisition,
  not merging cached fragments from possibly different server revisions. Only
  an unimplemented v1 service triggers version fallback, not an unsupported
  extension lookup or an authentication failure.
  Discovery traffic is control work: do not record it as test traffic or award
  coverage for it. Discard acquired credentials/streams after discovery.
- Source adapter: use `github.com/bufbuild/protocompile` in-process with local
  resolvers, bounded work and standard imports. Compile entry files and collect
  their import closure; retain options and available source information. Do not
  assume its resolver reads `buf.yaml`, `buf.lock` or the BSR automatically.
- Descriptor adapter: load the explicit local binary set without probing any
  server. Capture works against servers with reflection disabled in both local
  source modes. Report/rebuild never instantiates any acquisition adapter.

- Parse `descriptorpb.FileDescriptorSet`, build a local `protodesc`/`protoregistry`
  index, and construct `dynamicpb` messages. No application-generated Go code is
  required in the Wiretap binary. Use a registry per contract snapshot, not
  process-global registration: two dependencies may declare conflicting names.
- Verify the acquired import closure, including well-known types and custom
  option extensions. Fail before test admission on missing imports,
  duplicate files/symbols, missing selected services, unsupported editions or
  unusable descriptors. Bound descriptor size, traversal depth and decode work.
- Freeze the acquired descriptor set plus the selected service list in a
  typed manifest under `contracts/<hash>/`. Record source kind, acquisition time,
  reflection API version where relevant, advertised inventory/exclusions,
  compiler/normalizer versions and content hashes, excluding credentials and
  private filesystem paths. Preserve original descriptor-file bytes when supplied;
  reflection/source modes serialize the acquired set once for integrity storage.
  Extend schema dispatch instead of passing protobuf through the OpenAPI JSON/ref
  loader. Live validation and
  offline reporting must use this same frozen snapshot.
- Hash frozen bytes for integrity. Separately derive the contract revision
  from deterministic descriptor serialization: sort files by logical filename,
  exclude source comments/locations, retain field numbers, types, presence,
  syntax/edition/features, method cardinality and all options, including unknown
  and custom option bytes. Sort/deduplicate the selected service names and include
  them in this identity. Initially hash the complete acquired descriptor set:
  unrelated declarations can therefore change the revision, a conservative limit
  which must be documented. Do not attempt service-closure pruning in the first
  release. Preserve internal descriptor indices when normalizing; do not sort
  fields or dependencies naively. Version this normalization rule and do not
  promise semantic equivalence across every compiler representation.
- Keep source kind, acquisition time and addresses out of semantic keys. Two
  source modes yielding the same normalized graph/selection get matching keys;
  runtime reflection may omit source-retained options or comments, so a reflected
  snapshot is not guaranteed equivalent to the source build. Preserve/report
  unknown option semantics and provenance. Reflection proves what that endpoint
  advertises, not agreement with a separately published contract. In particular,
  a mock's reflected schema is still a mock assumption. Teams checking an
  independently published contract should select that local contract explicitly.
- Cache only the immutable snapshot within a run. Do not re-fetch for each RPC,
  refresh the denominator mid-run, or reuse an old snapshot silently in a new
  run. Changed deployments require a fresh run. Reflection cannot guarantee an
  atomic snapshot across a concurrently redeployed or load-balanced fleet;
  target a stable standalone test instance and surface observed inconsistencies.
- Include descriptors for reachable `Any` values when available. An unknown
  `Any` type stays opaque with a diagnostic; do not fetch its type URL. Unknown
  wire fields and custom options are preserved and surfaced as scope limits,
  not automatically treated as invalid or as known, covered fields.

The [Go dynamic message API](https://pkg.go.dev/google.golang.org/protobuf/types/dynamicpb)
provides the descriptor-backed message implementation. This local runtime
reflection is unrelated to enabling a server reflection endpoint. Protobuf
[deterministic serialization is not universally canonical](https://protobuf.dev/programming-guides/serialization-not-canonical/);
comparison requires the same documented normalization/profile version.

### B. Forwarding, routing and lifecycle

Add a gRPC path beside the HTTP handler in `internal/proxy`, with shared
admission/routing/lifecycle logic. Keep protocol recognition explicit: native
`application/grpc` and `application/grpc+proto` are supported; do not mistake
`application/grpc-web*` or Connect bodies for native gRPC.

- Use a direct listener bound to an API/dependency identity. Forward the exact
  `/fully.qualified.Service/Method` procedure to its upstream. Per-dependency
  ports avoid nonstandard paths and custom authority requirements in user code.
- The main capture listener must still support the existing HTTP dependency
  gateway when the provider is gRPC. Accept HTTP/1 and HTTP/2 downstream, dispatch
  the reserved `/__wiretap/dep/` and session dependency paths to HTTP routing
  first, and dispatch the remaining provider calls by configured protocol.
  Return the actual HTTP dependency URL and listener TLS scheme. For a native
  gRPC h2c upstream, use a separate HTTP/2-only transport; enabling HTTP/1 on that
  transport can prevent prior-knowledge h2c from being selected.
- Reuse long-lived upstream HTTP/2 transports, separated by target and TLS
  configuration, and close them on runtime shutdown. Add no Wiretap application
  retry/replay and do not populate `GetBody` for captured inbound bodies. Go's
  transport can retry connections/streams it considers safe before delivery;
  verify and document these semantics with the pinned version instead of
  promising a blanket transport retry switch. Never follow redirects. Client
  retries are separate observed flows; do not deduplicate them into one execution.
- Preserve repeated metadata, binary metadata, request/response trailers,
  `grpc-encoding`, `grpc-accept-encoding`, error details, cancellation and
  half-close semantics. Configure upstream authority/SNI from the target;
  transport-specific connection headers are not copied blindly.
- Respect `grpc-timeout`, including elapsed proxy time, without extending the
  caller's deadline. Distinguish upstream RPC status, upstream connection failure,
  client cancellation and proxy-generated routing/admission errors.
- A native gRPC response may have HTTP 200 with a non-OK `grpc-status`, or be
  trailers-only. Record both transport status and terminal RPC status. Missing,
  invalid or contradictory terminal status is a protocol diagnostic, never
  assumed `OK`. Label any client-equivalent fallback status as derived.
- Continue forwarding if the observer cannot decode. Use an incremental parser
  for the gRPC five-byte message envelope; HTTP/2 DATA boundaries are unrelated
  to message boundaries. Decode identity/gzip within bounds; preserve unsupported
  encodings as opaque evidence. Do not decompress/recompress the forwarded body.
- Start with the existing 4 MiB capture budget per direction, applied across all
  retained messages in that RPC, plus a finite message-record limit (proposed
  default: 1,024 per direction). Bound decompressed size independently. A large
  advertised length, gzip expansion, or an endless stream of empty messages
  must not allocate unbounded memory. Keep total observed bytes/counts and
  explicit dropped/truncated markers when limits are reached. Apply a separate
  aggregate budget to metadata, trailers, status text and rich error details
  (proposed defaults: 64 KiB and 256 retained entries per direction, with details
  sharing that byte budget). Bound detail decoding depth/expansion as for other
  protobuf messages and mark omitted values unavailable. Do not credit affected
  assurance. These are observation limits, distinct from the HTTP/2 stack's
  transport safety limits.
- Record one in-flight admission per RPC until completion; never per message.
  Assign its flow ID at admission and retain a bounded active snapshot. Drain
  expiry commits an incomplete RPC snapshot exactly once, even if the stream
  finishes later. Preserve ownership until late work exits; never attach it to
  the next test or mutate an already committed attempt.
- `finishRun` must cancel retired RPCs after a bounded drain grace and wait for
  their admission releases, rather than remain permanently blocked by an endless
  stream. Runtime shutdown similarly cancels calls and force-closes connections
  after its grace expires. Persist the incomplete snapshot first, label local
  termination, and keep the committed attempt immutable. Test these transitions
  against the current `inFlight` finalization guard.
- Forward traffic outside an active test using run-level routes without
  attributing it to a later attempt. Reject ambiguous/retired ownership with a
  protocol-correct, explicitly proxy-origin error. No timing-based guessing.
- A streaming response must flush before request completion, and buffering must
  not grow with duration. Do not apply the control listener's short total
  read/write timeout to capture streams. Keep control API, HTTP capture and RPC
  timeout configurations distinct.

See the [gRPC wire protocol](https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md)
and [status codes](https://grpc.io/docs/guides/status-codes/) for protocol details.
Forwarding is application-message preserving; it is not a promise to preserve
HTTP/2 frame boundaries, connection identity, header order or exact timing.

### C. RPC evidence and redaction

Use a deliberate evidence **2.0** change. Current readers accept additive 1.x
fields but could silently ignore RPCs and publish misleading coverage. Old
readers must reject new bundles explicitly. No legacy migration layer is
required; update the writer, reader, schema and fixtures together, document that
old artifacts can be regenerated, and do not silently reinterpret 1.x messages.

Keep shared flow identity, channel, API identity, test name, trace/correlation,
timestamps and dependency-assumption provenance. Introduce an explicit tagged
HTTP/RPC message union in `artifact.Message` and its custom JSON codec. For RPC,
one flow represents one invocation, with:

| Field group | Required meaning |
| --- | --- |
| Method | Fully qualified service, method, complete procedure, selected schema reference, unary/server/client/bidirectional kind. Unknown method kind stays unknown. |
| Request/response | Metadata, trailers where applicable, ordered message records and direction completeness. |
| Message | Direction-local index, observed time/order, fully qualified type when known, retained payload reference, compression, byte counts, decoding/retention/redaction state. |
| Completion | Observed numeric RPC code/name, message and rich error details, status source, transport status/error, cancellation and terminal-completion state. |
| Correlation | Existing test ownership and parent-flow confidence, not a fabricated parent relationship. |

Use per-direction order as authoritative. Cross-direction timestamps describe
observation order at the proxy, not exact causal ordering. Distinguish an empty
protobuf message (valid zero-byte payload) from no message, unavailable payload
and an incomplete frame. Do not store a stream as concatenated JSON.

Persist complete retained protobuf messages as hashed attachments. Generate
ProtoJSON for display from the verified descriptor and payload; it is not an
alternative contract or independent coverage input. Extend body references to
include message index and representation, and verify every attachment on load.
Represent opaque compressed/partial bytes explicitly so readers cannot feed them
to a protobuf decoder as complete messages. Metadata keeps repeated values;
binary values use a documented base64 representation, never a lossy UTF-8 cast.

On load, require unique `(flow_id, direction, message_index, representation)`
references, valid ordered indices, explicit zero-byte payload support, bounded
sizes, manifest/service-selection consistency and agreement between the flow
protocol and tagged message kind. Reject duplicate/mismatched references and
inline-plus-attachment duplicates instead of silently choosing one. Unknown
future message kinds must fail explicitly rather than vanish from reports.

**Redaction must be implemented before RPC capture ships.** Extend selectors to
RPC metadata, trailers, each message and rich status details. Preserve familiar
`request.headers` / `response.trailers` semantics where applicable; introduce a
documented `request.messages[*].body.<field>` equivalent for protobuf payloads.
Selectors use proto field names; repeated/map selectors and `Any` handling need
explicit rules and tests.

For an affected message, retain a redacted readable projection and remove the
original protobuf/compressed bytes and other duplicate representations from
persistence. Mark it redacted and ineligible for payload coverage/replay. If
the message cannot be decoded sufficiently to apply a configured rule, omit its
payload with a diagnostic rather than silently retaining it. Apply the same
policy to `grpc-status-details-bin` and its decoded details. Perform redaction
before hashing/attachments, including logs, knowledge summaries and error text.
Do not mutate the payload forwarded to the SUT or client.

### D. Validation and coverage semantics

Separate four claims: the method was called; the payload decodes under the
contract; declared semantic constraints hold; the test's assertions passed.
Protobuf types alone do not declare arbitrary string lengths, numeric business
ranges, authorization rules or permitted error responses.

First release validation checks method declaration, method/message cardinality,
protobuf decoding, required-field initialization where applicable, and supported
descriptor semantics. Perform this passively on complete captured messages.
Unknown enum numbers can be valid for open enums, and unknown fields can be valid
schema evolution; do not apply JSON Schema's assumptions to these cases.
See [Protobuf enum behaviour](https://protobuf.dev/programming-guides/enum/).

Use `protoreflect` values/presence for evaluation. ProtoJSON is for humans:
64-bit integers, bytes, enums, special floats and well-known types have specific
representations. Do not round-trip these through generic JSON float64 maps or
insert default values as if they were explicitly sent.
See [field presence](https://protobuf.dev/programming-guides/field_presence/)
and [ProtoJSON](https://protobuf.dev/programming-guides/json/).

Create a versioned protobuf measurement profile, e.g. `wiretap-protobuf-1`,
alongside the existing HTTP rules. Dispatch by schema type and record the profile
per spec; do not change existing HTTP condition keys merely to add RPC.

| Initial family | Coverage rule |
| --- | --- |
| Method inventory | Every selected descriptor method appears, including uncalled ones. Streaming methods remain visibly unsupported until the streaming milestone. |
| Explicit field presence | Present/absent classes only where the descriptor actually has presence. Required fields have no valid absent target. Implicit proto3 scalar omission versus explicit default is not observable as semantic presence. |
| Oneof | Each legal member and unset when legal; exclude synthetic oneofs used to represent proto3 optional. Multiple wire occurrences follow protobuf merge semantics, not an invented validation failure. |
| Enum | Declared distinct numeric values, alias names collapsed; observe unknown numbers separately without inventing an infinite denominator. |
| Boolean | True/false, with explicit presence measured separately where supported. |
| Repeated/map | Empty/nonempty structural classes; examine retained items using exact indices/keys. Do not invent every possible length or key as a target. |
| Nested message | Recurse through supported, nonrecursive paths; report recursive expansion limits explicitly. Unsupported subtrees cannot disappear from scope. |
| RPC status | List observed terminal outcomes. Standard protobuf service declarations do not enumerate allowed statuses, so do not count all 17 codes as required coverage. |

For declared validation annotations in the first release, preserve the options
and mark semantic validation/measurement unsupported. Do not advertise complete
contract enforcement when only wire/type checks ran. Retain structural
observations, but mark affected valid-condition assurance unknown: an annotation
could forbid an otherwise representable enum value or absent field. If the scope
of an unknown constraint cannot be established, apply that limit to the whole
message tree. Keep the unknown/unsupported scope visible in counts. A later
milestone can use Protovalidate for standard rules and CEL evaluation. Generating
finite coverage targets for those rules is separate work; arbitrary CEL is not
generally enumerable. See [Protovalidate rules](https://protovalidate.com/schemas/standard-rules/).

Reuse existing evidence quality gates: failed/incomplete attempts, redacted or
truncated messages, unsupported decoding and transport failure cannot earn
successful payload coverage. A complete, passing negative test can record a
declared method and its actual error outcome; a non-OK RPC is not automatically
a failed test. A successful error response without a result message does not
cover the declared success response type. Client/proxy failures do not establish
provider rejection behaviour. Unknown/partial validation stays visible.

Keep provider, mock and real-dependency provenance separate. Dependency results
describe observed assumptions, with consumed fields unknown and provider
agreement not checked. Do not introduce invalid-value coverage targets for mocks.

Use protocol-aware operation identities based on stable API ID plus fully
qualified service/method. Shared condition keys include profile, contract
revision, phase and descriptor field-number path (with readable names beside
them), not host/port, test name or provider/dependency role. Evidence adds
message index and item location. One message can cover several conditions;
retain individual observations and a combination fingerprint without a
Cartesian-product coverage percentage.

### E. Bundle views and existing features

Extend the current report builders to show RPC service/method, request/response
message types, stream kind, terminal code, metadata, ordered messages and exact
test/evidence links. Use a protocol-neutral operation view model instead of
scattering `if grpc` conditions through HTML templates.

- Keep the existing separation between coverage requirements and test runs.
  An RPC invocation appears once; streaming messages are expandable children.
- Group provided methods and dependency assumptions by API/service. Render
  statuses as `OK`, `INVALID_ARGUMENT`, etc., keeping HTTP transport information
  as detail. Never label HTTP 200 alone as RPC success.
- Reuse existing OKF pages, `docs/knowledge.json`, verification tasks and coverage
  catalogues. Add protocol, descriptor revision, message references and limits
  so an agent can distinguish observation from assurance.
- Feed the same protocol-aware calculation to `report` and diagnostic `export`.
  Rebuilding works offline without the original project, Buf, protoc or server.
- Generate observed OpenAPI drafts only for HTTP mocks. gRPC dependencies link
  resolved descriptors and readable observed examples; no fabricated OpenAPI or
  inferred `.proto`. Pending/failed contract acquisition stays visible as an
  unresolved boundary with an unknown denominator. A missing descriptor claimed
  as resolved is an integrity error when reading a bundle.
- Keep `explore`'s HTTP-only selection and sequence guard. Mixed bundles are
  reportable; an attempted replay containing unsupported provider RPC steps must
  fail before sending traffic or running fixture hooks. An HTTP provider with
  observed gRPC dependencies still follows the existing provider-only replay
  policy; do not claim those dependencies were replayed or reverified.
- Emit specific verification tasks for untested methods, unobserved valid field
  cases and unsupported validation semantics. Missing contracts indicate an
  explicit HTTP opt-out or failed/pending gRPC acquisition; distinguish them.
  A gRPC failure blocks capture readiness and complete-run assurance. Capturing
  a mock response is never proof of provider agreement.

## Work packages, in order

Each package should be independently reviewable. Avoid a broad framework/plugin
refactor: add only the shared interfaces required by HTTP and RPC.

### 0. Prove forwarding semantics

- [ ] Build a small real grpc-go service/client fixture with reflection switchable;
  add a Connect client using `WithGRPC` as an independent interoperability case.
- [ ] Exercise the grpcreflect client against native gRPC v1 and v1alpha servers,
  and prove `protocompile` handles local imports without an external executable.
- [ ] Prototype raw reverse forwarding with h2c and TLS, trailers-only failures,
  gzip, cancellation, split message envelopes and early streaming responses.
- [ ] Confirm bounded observation cannot change delivery. Check `ReverseProxy`
  cancellation/trailer/flush behaviour, including early upstream rejection while
  the request body is still being sent. Do not snapshot mutable buffers before
  their readers have stopped.
- [ ] Record the chosen public transport APIs/dependency versions here before
  proceeding. Do not substitute a decoding Connect gateway without demonstrating
  equivalent preservation and memory behaviour.

Done when native clients see the same RPC results with and without the proxy,
including failure and stream lifecycle cases. This is a spike, not a release.

### 1. Configuration, descriptors and inventory

- [ ] Extend `internal/project/config.go`, `internal/core/config.go`,
  `internal/app/project.go`, `api.go` and `runtime_sessions.go` with the proposed
  protocol/source/service/listener/TLS settings and run-level address discovery.
  Omitted gRPC schema selects reflection; HTTP's existing string schema remains.
- [ ] Add `internal/protobuf` and typed schema freeze/load in
  `internal/bundle/schema.go`, with reflection, source-compilation and descriptor
  adapters converging on one mandatory contract. Keep schema opt-outs HTTP-only.
- [ ] Persist unresolved boundary inventory on `startRun`; add the single-flight
  all-contract readiness barrier to first setup and zero-test finalization.
  Publish snapshots atomically and align control/SDK deadlines with acquisition.
- [ ] Add matching Go SDK config structs and JS normalization/types in `sdk/go`
  and `sdk/js/src`; keep SDKs as control-API clients.
- [ ] Test default/explicit reflection, v1alpha fallback, authentication and
  deadlines, reflected service filtering, missing imports/services, rejected
  opt-outs/empty source objects, registry collisions, descriptor revisions,
  config/API parity, port cleanup and unused-service method inventory.
- [ ] Assert local source modes never probe reflection or resolve imports over
  the network, failed acquisition is retryable without half-admitted attempts,
  zero-test/unresolved runs cannot claim completeness, and credentials never
  reach API defaults, artifacts or diagnostics.

Done when all three sources produce frozen contracts usable offline, reflection
works as the default, and local inputs work with reflection disabled. The same
normalized graph/selection produces matching keys across source modes.

### 2. Evidence and safe persistence

- [ ] Update `internal/artifact/types.go` and `encode.go` with the explicit union,
  RPC call/message/completion types and deterministic ordering.
- [ ] Update `internal/bundle/bundle.go`, `docs/bundle-evidence.schema.json` and
  evidence version checks for per-message attachments and typed manifests.
- [ ] Implement RPC redaction in `internal/redact`, including raw/decoded/status
  detail consistency. Extend stable fingerprints in `internal/app/recorder.go`.
- [ ] Add round-trip, corruption, incomplete-message, zero-byte payload, redaction
  leak and old-reader/new-version rejection tests.

Done when mixed HTTP/RPC evidence round-trips without loss or disclosure and
every retained attachment and descriptor is verified before use.

### 3. Unary runtime capture

- [ ] Implement direct listeners and shared HTTP/2 transports in `internal/proxy`;
  reuse `admission.go` and application session ownership.
- [ ] Add the bounded envelope observer under `internal/capture` and an RPC
  recorder path in `internal/app/recorder.go`. Generalize the HTTP-only admission
  record callback without duplicating session bookkeeping.
- [ ] Integrate startup/rollback/shutdown through `internal/app/components.go`
  and `run.go`; allocate routes before SUT startup, freeze settings while
  listeners are in use, and require ready contracts before admitting tests.
- [ ] Capture terminal status/errors and incomplete active calls; retain the
  correct attempt across reused channels, parallel traces and drain expiry.
- [ ] Pass streaming traffic through, but until package 6 is complete mark its
  capture/coverage unsupported and exclude it from successful assurance. Never
  present a retained first message as the entire RPC.

Done when provider and mock unary traffic produces accurate evidence using
unchanged native generated clients in reflection and local-source modes.

### 4. Validation, coverage and views

- [ ] Dispatch schema validation in `internal/app/schema_validation.go`; separate
  decode/type validation from unsupported semantic annotation validation.
- [ ] Extend `internal/coverage/report.go`, `contracts.go`, `contract_identity.go`
  and add protobuf-specific evaluation files; keep HTTP evaluators intact.
- [ ] Extend `internal/bundlereport/report.go`, `knowledge.go`, `contracts.go`,
  `viewer.go`, `viewer_coverage.go` and `viewer_tests_view.go` through shared
  operation/message view models. Gate `mock_openapi.go` on HTTP.
- [ ] Test offline rebuilding, exact message links, zero-traffic RPC inventory,
  unresolved/incomplete inventories, non-OK statuses, cross-role/source key
  matching where normalized contracts agree, and explicit exploration rejection.

Done when the bundle explains what was tested and assumed, with truthful scope
and identical measurements across JSON, Markdown and the static viewer.

### 5. Adoption example and first release

- [ ] Add `examples/basic-grpc` with a provider calling a mock dependency, native
  generated clients, imported proto types, and reflection enabled by default.
  Include alternate configs for proto sources and descriptor sets with reflection
  disabled on both targets, plus a mixed-source example.
  Check in fixture descriptors/generated test code with a regeneration script;
  ordinary tests should not require Buf/protoc installation.
- [ ] Demonstrate happy path, legitimate error outcome, parallel trace ownership,
  no-trace sequential capture, dynamic-port startup before reflection readiness,
  and HTTP-to-gRPC mixed-boundary capture.
- [ ] Document ordinary Go/Node gRPC instrumentation and TLS setup; do not bundle
  a mandatory alternative client or silently add duplicate instrumentation.
- [ ] Update root README, SDK READMEs, `docs/configuration.md`, `runtime-api.md`,
  `getting-started-existing-tests.md`, `otel-adoption.md`, `https-trust.md`,
  `architecture.md`, bundle format/profile guides, contract coverage rules,
  troubleshooting and the examples index. Reconcile and promote the
  [gRPC user guide](grpc-user-guide.md) when its examples work.
  Label unary and semantic limits.
- [ ] Run the existing Go and JS suites plus the new interoperability tests;
  verify `go test -race ./...` for shared transport/session/capture state.

Packages 0–5 form the unary release. Do not announce complete gRPC streaming,
arbitrary validation coverage, or gRPC exploration at this point.

### 6. Complete streaming support

- [ ] Promote server-, client- and bidirectional-streaming capture after tests
  prove full duplex forwarding, half-close, partial-message cancellation,
  backpressure and bounded retention over long streams.
- [ ] Validate every retained complete message and terminal outcome; represent
  dropped/unavailable messages and unfinished streams explicitly.
- [ ] Link per-message observations and combinations to the same RPC/test; do
  not infer unobserved cross-message business invariants or flatten a stream
  into a unary response. Conservative incomplete-stream coverage remains unknown.
- [ ] Test drain expiry and runtime interruption midstream with deterministic
  partial evidence, no late writes and no attribution to the next test.
- [ ] Add streaming examples and update the advertised support matrix.

## Acceptance matrix

| Area | Cases that must be verified |
| --- | --- |
| Default reflection | No local schemas supplied; discover provider/mock services and uncalled methods, fetch imports and freeze contracts. Test v1/v1alpha, TLS/auth, timeout, filtered/empty inventory and reflection traffic exclusion. |
| No reflection | Both `.proto` and descriptor-set inputs work with reflection disabled and no network schema lookup; copied bundles rebuild with source files removed and servers offline. |
| Mandatory contracts | Provider, mock and real-dependency boundaries reject opt-outs, invalid source objects, missing imports or failed reflection before admission. Pending/failed inventory stays visible; HTTP opt-outs still work. |
| Snapshot/provenance | Acquisition single-flight, no mid-run refresh, new run fetches fresh definitions, reflected mock contract labelled as mock-advertised, no claim of independent provider verification, no false equivalence across differing source graphs. |
| Interoperability | Native grpc-go client/server, Connect client in gRPC mode, Node native gRPC example; no method-path prefix or custom client requirement. |
| Topology | gRPC provider + gRPC mock, HTTP provider + gRPC dependency, gRPC provider + HTTP dependency; stable API IDs and correct provenance. |
| Routing/lifecycle | Multiple dependencies with the same service names, dynamic ports and SUT boot before discovery, pre-test traffic, shared channels, traced parallel tests, sequential fallback, ambiguous/retired contexts, acquisition failure/retry, zero-test finalization and listener cleanup. |
| Transport | h2c and TLS, custom CA, invalid certificate/ALPN, multiple calls per connection, timeout/cancellation, upstream unavailable, early error, no Wiretap application retries and tested transport retry semantics. |
| RPC outcomes | OK, HTTP 200 + non-OK trailers, trailers-only error, rich status details, missing/malformed status; proxy failures distinguished from provider results. |
| Payloads | Empty message, no message, fragmented/coalesced envelopes, gzip, unsupported compression, malformed protobuf, unknown fields/enums, optional/default/oneof, maps/repeated, int64/uint64 limits, bytes, well-known types and known/unknown Any. |
| Resource limits | Oversized and compressed messages forward intact; retained bytes, metadata and record counts stay bounded; no coverage from truncation. |
| Privacy/integrity | Redacted secrets absent from raw attachments, binary metadata, JSON, logs, status details and views; missing/tampered descriptor or message rejected. |
| Coverage | Uncalled methods visible; no fake HTTP statuses or OpenAPI drafts; condition identities stable across workspace moves/roles; unsupported annotations and scopes visible; multiple conditions per message. |
| Streaming milestone | All three streaming shapes, early responses, half-close, slow reader, cancellation, empty-message flood, partial capture on drain and interruption. |
| Regression | Existing HTTP capture, schema enforcement, redaction, tracing, bundle rebuilding, exploration and SDK lifecycle tests still pass. |

Use targeted protocol tests for each package, then the full regression suites at
the release gates. No need to add tests for documentation wording itself.

## Follow-ups after capture is established

1. **Protovalidate.** Evaluate declared standard/CEL rules using the frozen
   descriptors; build finite coverage targets only for explicitly supported
   rules. Unknown custom validation systems remain clearly unsupported.
2. **Unary gRPC exploration.** Reuse project-owned hook processes and emit a new
   evidence bundle. Connect's dynamic client can call native gRPC using frozen
   descriptors without user-generated clients. Mutate only declared constraints,
   verify the invalid request, and require an explicit allowed rejection code
   (typically `INVALID_ARGUMENT`); authentication, transport failure and arbitrary
   non-OK results do not prove validation. Streaming replay is separate work.
3. **Connect and gRPC-Web wire formats.** Add distinct adapters and conformance
   tests while sharing protobuf contracts, identities and reports. Do not enable
   them merely because a dependency library advertises support.
4. **Remote schema convenience.** Optional BSR imports or Buf module dependency
   resolution can produce the same frozen descriptor artifact. The three initial
   source modes already cover reflection, local sources and descriptor files.
   Keep credentials outside bundles, pin remote revisions and preserve offline
   reporting without hidden registry access.

The product promise stays the same: reuse existing tests and mocks, observe
system boundaries, and publish portable evidence of tested behaviour and
dependency assumptions. gRPC changes the transport and contract evaluator,
not who owns the team's tests.
