# Phrond Product Roadmap — Outstanding Items

Status: open
Last reviewed: 2026-07-25

These are the known gaps and recently completed work across capture fidelity,
mock interoperability, and exports. B3 remains before the recorder can claim
lossless capture. It is deliberately deferred (v1 is scoped to plain HTTP
request/response), but can silently corrupt artifacts when hit, so it is
specified here in enough detail to implement without re-deriving context. Item
numbering (B3–B5) is historical, from the original capture-fidelity review;
B1/B2 (content-encoding + binary bodies), B4 (bounded capture), B6
(deterministic responses), and D (session-correlation friction) are already
resolved — see `testing-tool-requirements-and-design.md` §11a. B5 was completed
on 2026-07-24. M1, the BYO-mock interoperability track, and E1, the independent
coverage-export track, were completed on 2026-07-24 without changing the
canonical Phrond artifact format beyond additive flow metadata. C1, the
consumer-facing suite API and explicit externally managed SUT boundary, was
completed on 2026-07-25.

Next remaining work: **B3**, implemented in the phases below.

---

## C1 — Consumer-owned SUT lifecycle and low-ceremony Node API — resolved

Status: completed 2026-07-25

### Resolution

- Added `createNodeTestSuite`, which registers Phrond-only `node:test` hooks,
  automatically names each session from `phrond.test("name", ...)`, records
  truthful pass/fail status, and delegates capture/mock inspection operations.
- Added `startCapture` as the preferred lower-level lifecycle name while
  retaining `startServer` for compatibility. Both start only Phrond's proxy
  and mock components.
- Added `match.json` and `response.json` SDK shorthands for literal JSON mock
  bodies, including the default JSON response content type.
- Documented the stable pre-start dependency URL shape
  `http://<mock-listen>/__phrond/dep/<asset-id>` so Docker Compose,
  Testcontainers, Spring Boot, Kubernetes, or CI can configure the SUT without
  waiting for test-side discovery.
- Moved both runnable REST examples' application startup, readiness, shutdown,
  and dependency configuration into their shell runners. Test code now owns
  only Phrond capture intent and any test fixture/mock state.

### Ownership boundary

Phrond never launches, stops, mutates, injects configuration into, or performs
readiness checks for the SUT. `sutBaseUrl` is only the capture proxy's routing
target. Application orchestration remains an explicit consumer responsibility.

---

## B5 — MITM upstream errors drop the connection instead of returning 502 — resolved

Status: completed 2026-07-24 in `341e201`

### Resolution
The MITM `RoundTrip` error path now records the exchange and writes a closing
HTTP/1.1 `502 Bad Gateway` response with `Content-Type: text/plain` and the
same `proxy error: <err>` body as the plain/reverse path. A focused MITM test
uses an unreachable upstream and verifies both the response and recorded
error.

### Problem
The two proxy capture paths behave differently when the upstream request fails:

- Plain/reverse path (`handleHTTP`, `internal/proxy/proxy.go`): `RoundTrip`
  error → the exchange is recorded with `Error` set **and the client receives
  a 502** via `writeProxyError`.
- MITM path (`proxyTLSRequests`, `internal/proxy/proxy.go`): `RoundTrip`
  error → the exchange is recorded, then the function just `return`s, which
  closes the TLS connection **without writing any HTTP response**.

### Impact
The test's HTTP client sees a raw connection reset ("socket hang up",
`ECONNRESET`) instead of a 502. This looks like a phrond bug rather than an
upstream failure, and different test HTTP clients surface it differently,
making failures non-deterministic to debug.

### What needs to be done
1. In the `RoundTrip` error branch of `proxyTLSRequests`, synthesize an HTTP
   response and write it to `clientTLS` before returning:
   - `HTTP/1.1 502 Bad Gateway`, `Content-Type: text/plain`,
     body `proxy error: <err>` — same body format `writeProxyError` uses so
     both paths are indistinguishable to the client.
   - Build it with `http.Response{...}.Write(clientTLS)` (or write bytes
     directly); set `Connection: close` and return after writing.
2. Keep the existing exchange recording (already correct — `Error` is set).
3. Test: MITM proxy test with an unreachable upstream host asserting the
   client receives status 502 (not a transport error) and that the recorded
   exchange carries the error. Mirror the existing passthrough/MITM tests in
   `internal/proxy/proxy_test.go`.

### Acceptance criteria
- A failed upstream call through MITM returns HTTP 502 to the client.
- Behavior (status, body shape, recorded exchange) matches `handleHTTP`.

---

## B4 — Bounded body capture — resolved

Status: completed 2026-07-25

Priority: high (CI stability) · Effort: small-medium

### Resolution

- Added `core.Config.Capture.MaxBodyBytes` and
  `PHROND_MAX_CAPTURE_BYTES`, defaulting to 4 MiB. `0` explicitly means
  unlimited.
- Proxy request and response bodies now stream while a shared counting writer
  retains only the configured prefix. The same limit applies to plain/reverse
  traffic and HTTPS MITM traffic.
- Built-in mock requests retain only the bounded prefix, literal responses are
  returned in full, external BYO mock traffic streams end to end, and fallback
  requests use a temporary-file replay so large bodies remain memory-bounded.
- Artifact bodies carry `truncated: true` and `original_bytes`; Workflow YAML
  metadata preserves those markers.
- Gzip and deflate decoding has its own bounded counting pass, preventing a
  small compressed body from expanding without limit.
- Truncated bodies use the stable `truncated` fingerprint shape and skip only
  OpenAPI body validation. Routing, parameters, headers, and status validation
  continue normally.
- Focused tests verify complete delivery with bounded recording across the
  plain proxy, MITM, built-in mock, external mock, fallback upstream, and
  compressed-body paths.
- The runnable `basic-rest` example uses a 64-byte limit to demonstrate a
  complete 512-byte response with bounded artifact evidence.

### Original problem

Every request and response body is buffered fully into memory and stored whole
in the artifact:

- Proxy plain/reverse path: `io.ReadAll(r.Body)` and `io.ReadAll(resp.Body)`
  in `handleHTTP` (`internal/proxy/proxy.go`).
- Proxy MITM path: same pattern in `proxyTLSRequests`.
- Mock server: `io.ReadAll(r.Body)` in `handle`, plus the fallback-upstream
  path (`forwardToUpstream`) buffering the upstream response
  (`internal/mock/server.go`).

There is no limit anywhere.

### Impact
One test that moves a large payload (file download/upload, big batch response)
through the proxy means that many bytes held in RAM and written into the
per-test artifact — degraded CI at best, OOM at worst. It also makes artifacts
too large to ship to a broker.

### Implemented requirements

1. **Config**: add a capture limit to `core.Config` (e.g.
   `Capture.MaxBodyBytes`, default ~4MB) with env override
   `PHROND_MAX_CAPTURE_BYTES`. `0` = unlimited (explicit opt-out).
2. **Capture must truncate; forwarding must not.** The client/SUT must always
   receive the complete body. Replace the read-all-then-forward pattern with a
   tee: stream the body through to its destination while capturing at most N
   bytes into the recording buffer (`io.TeeReader` into a capped writer, or a
   `LimitedWriter` that discards past the cap but keeps counting).
   - Note this is a behavior improvement over today's full buffering: large
     bodies stop transiting through memory entirely.
3. **Artifact fields**: extend `artifact.Body` with
   `truncated: true` and `original_bytes: <int64>` (total size actually
   transferred). Update `docs/artifact-openapi.yaml` (the schema uses
   `additionalProperties: false`, so new fields must be declared) and the
   fingerprinting/body-shape code (`internal/app/recorder.go`) to treat
   truncated JSON bodies as `truncated` rather than mis-parsing a cut-off
   document.
4. **Apply everywhere bodies are captured**: both proxy paths, mock request
   bodies, and the fallback-upstream response path.
5. **Interaction with content-encoding**: decompression (`bodyFromHTTP`) must
   also be capped — a small gzip body can decompress huge (zip-bomb shape).
   Cap the decompressed output at the same limit and mark truncated.

### Verified acceptance criteria

- A response larger than the cap is delivered intact to the client, while the
  artifact stores at most the cap with `truncated: true` and the true size.
- Memory use during a large transfer stays bounded (no full-body buffering).
- Schema validation and fingerprinting degrade gracefully on truncated bodies
  (no false schema errors from cut-off JSON; document that truncated flows are
  skipped for body validation).

---

## B3 — HTTP/2 + RPC support (Buf Connect) + WebSocket/SSE upgrade handling

Priority: medium (blocks h2/gRPC/streaming adopters) · Effort: large, phased

### Problem
The proxy only speaks HTTP/1.1, the mock server cannot serve RPC protocols,
and the proxy misbehaves on protocol upgrades:

- **No h2 upstream**: the shared transport sets `ForceAttemptHTTP2: false`
  (`httpTransport`, `internal/proxy/proxy.go`), silently downgrading upstreams
  that prefer h2.
- **No h2 client-side in MITM**: the MITM TLS handshake advertises only
  `http/1.1` via ALPN (`mitmTunnel`). Clients that require h2 — all gRPC —
  cannot connect at all.
- **No RPC mocking**: the mock server cannot answer gRPC/gRPC-Web/Connect
  calls (framing, `grpc-status` trailers, h2c) even though an RPC method call
  is conceptually just `POST /pkg.Service/Method` — a perfect fit for the
  existing asset-scoped path matcher.
- **Upgrades are misparsed**: the MITM tunnel processes decrypted traffic with
  a manual `http.ReadRequest` loop (`proxyTLSRequests`). A WebSocket/SSE
  upgrade (`Connection: Upgrade` → `101 Switching Protocols`) turns the
  connection into a bidirectional byte stream that the loop will misparse —
  recording garbage flows or killing the connection. The plain reverse path
  has the same blindness to upgrades.

### Impact
Any team whose SUT or dependencies use gRPC, h2, WebSockets, or SSE gets
broken traffic or corrupt artifacts with no diagnostic. This is the largest
remaining gap against "no changes to how teams work".

### Design decision: Buf Connect with a raw-bytes codec — schema-less RPC
Implement RPC support on `connectrpc.com/connect` (connect-go) rather than
hand-rolling gRPC framing or importing `google.golang.org/grpc`:

- **No type definitions, no reflection.** connect-go supports pluggable
  codecs. Register a custom identity codec (`Marshal`/`Unmarshal` on
  `[]byte`/`*bytes.Buffer` pass-through) and generic handlers, and phrond can
  serve and call RPC endpoints **without `.proto` files, generated types, or
  gRPC server reflection**. The method is identified by URL path
  (`/pkg.Service/Method`), which the existing stub matcher already handles.
- **Three protocols for free.** One connect-go handler speaks gRPC, gRPC-Web,
  and the Connect protocol, including the fiddly parts (5-byte envelope
  framing, `grpc-status`/`grpc-message` trailers, error-code mapping, h2c via
  `golang.org/x/net/http2/h2c`).
- **What Connect does NOT change**: decoding binary protobuf *payloads* into
  structured JSON inherently requires descriptors — the protobuf wire format
  is not self-describing (field numbers and wire types only, no names). This
  stays out of scope: payloads are captured as opaque bytes via the existing
  base64 body path, consistent with the literal-response philosophy. (Buf's
  Vanguard transcoder is explicitly *not* an option here — it requires
  descriptors.) A later optional `protobuf` schema type can add decoding for
  teams that attach descriptors, mirroring how OpenAPI is optional today.
- **Adoption note (zero work, document it)**: services already using the
  Connect protocol in JSON mode send plain `POST /pkg.Service/Method` with
  `application/json` over HTTP/1.1 — phrond captures those **today** with
  readable bodies and no B3 work at all. Worth stating in the README once RPC
  support lands.

New dependencies: `connectrpc.com/connect`, `golang.org/x/net` (http2/h2c).

### What needs to be done
Phased — each phase is independently shippable:

1. **Record the truth first (cheap)**: capture the negotiated protocol per
   flow. Add `http_version` (e.g. `"1.1"`, `"2"`) to the artifact flow (from
   `resp.Proto`), and when an upgrade is detected, record a metadata-only flow
   with a new field such as `upgraded: "websocket" | "sse" | <proto>`. Add an
   `rpc_protocol` field (`grpc` | `grpc-web` | `connect`) for phases 3–5.
   Update `docs/artifact-openapi.yaml`.
2. **Upgrade detection + passthrough**: in both the reverse path and the MITM
   loop, detect upgrade requests (`Connection: Upgrade` + `Upgrade` header)
   and `101` responses. On detection: stop HTTP parsing, splice the two
   connections as a raw byte tunnel (the `proxyStream` helper already exists
   for CONNECT passthrough), and record a single metadata-only flow (method,
   URL, request/response headers, `upgraded`, duration). SSE (`Accept:
   text/event-stream`, no 101) needs streaming-response handling instead:
   stream the body through and cap the captured prefix (reuses B4's tee).
3. **h2 everywhere on the wire**:
   - Upstream: `http2.ConfigureTransport` (or `ForceAttemptHTTP2: true`) on
     the proxy transports; preserve HTTP trailers when forwarding (gRPC status
     lives in trailers).
   - Mock server + proxy listeners: wrap handlers in `h2c.NewHandler` so
     cleartext h2 (gRPC's default for local/test traffic) connects.
   - MITM: advertise `h2` in ALPN and serve decrypted connections with
     `http2.Server`, replacing the manual `ReadRequest` loop for h2
     connections.
4. **RPC mocking via connect-go (unary)**: mount a catch-all connect-go
   handler with the raw-bytes codec on the mock server. Route
   `/pkg.Service/Method` through the existing asset/session-scoped registry:
   the queued `response.body` bytes are returned as the reply message
   (base64-decoded when the stub marks the body as binary), faults map to
   Connect error codes (`connection_error` → aborted stream,
   `delay_ms` unchanged). Recorded exchanges carry `rpc_protocol` and the raw
   request message bytes. Unary only in v1 — queued literal responses are
   unary semantics; streaming RPC mocking is explicitly deferred.
5. **RPC capture through the proxy**: for gRPC/Connect traffic the reverse
   path forwards with the h2 transport from phase 3 (a byte-faithful forward
   preserving trailers — connect-go is not required on the pure pass-through
   path); capture tees the framed messages into the artifact as base64 bodies
   with `rpc_protocol` set and `grpc-status` recorded from the trailer.

### Acceptance criteria
- Phase 1: every flow records its HTTP version; upgraded connections produce a
  metadata flow instead of corrupt request/response flows.
- Phase 2: a WebSocket echo through the proxy works end-to-end and the
  artifact contains one `upgraded: websocket` flow; an SSE stream is delivered
  intact with a capped captured prefix.
- Phase 3: curl `--http2-prior-knowledge` works against mock and proxy;
  trailers survive forwarding.
- Phase 4: a connect-go *client* (gRPC, gRPC-Web, and Connect protocol modes)
  gets a queued literal response from the mock **with no proto registration
  anywhere in phrond**; the exchange records the method path, `rpc_protocol`,
  and raw message bytes; `grpc-status: 0` on success and mapped codes for
  faults.
- Phase 5: a SUT calling a real gRPC upstream through the reverse proxy works
  end-to-end with flows recorded as `http_version: "2"`, `rpc_protocol:
  "grpc"`, and correct `grpc-status`.

---

## M1 — Capture gateway for bring-your-own mock servers — resolved

Status: completed 2026-07-24

Priority: highest (adoption without replacing established mock tooling) ·
Effort: medium

### Goal
Allow a dependency to use an external mock server such as WireMock while
Phrond remains in the network path and records the complete `sut_to_mock`
exchange. Teams keep the mock framework and integration that best fits their
stack; Phrond continues to provide the unique value of cross-channel capture,
dependency attribution, causality, validation, and broker-ready artifacts.

The default remains Phrond's built-in literal-response mock. BYO mode is
selected per dependency, so built-in and external mocks can coexist in one
test.

### Implemented model
Declare the external mock as the upstream for a dependency:

```json
{
  "asset_id": "inventory-service",
  "asset_name": "Inventory",
  "mock": {
    "mode": "external",
    "upstream_url": "http://127.0.0.1:18089"
  }
}
```

`setupTest` still returns Phrond's asset-scoped `dependency_url`. The SUT calls
that URL; Phrond removes its internal session/asset prefix, forwards the
original method/path/query/headers/body to the external mock, returns the
external response unchanged, and records the exchange against the known
session and `asset_id`.

This gateway is the reliable default because attribution comes from routing,
not host inference, and it works even when multiple dependencies expose the
same paths. A later optional forward-proxy variant can support frameworks that
must inject the external mock URL directly, but it is not required for M1.

### Artifact semantics
- The flow remains `channel: sut_to_mock` and retains `asset_id`, `trace_id`,
  `parent_flow_id`, fingerprints, request/response bodies, schema validation,
  and timing.
- `mock_mode: external` lets consumers
  distinguish an observed external response from a Phrond-served stub.
- Optional flow-level `transport_error` metadata keeps a failed external
  upstream visible in the finalized artifact, not only in the in-memory
  recorder.
- `mock_stub` is absent because a generic gateway cannot know the external
  mock's match rule or unrendered response template. The captured response is
  evidence, but not a complete declaration of the consumer's configured
  assumption.
- `unmatched` is not set merely because Phrond did not match a local stub.
  Whether an external mock's response represents an unmatched request is
  provider-specific and cannot be inferred reliably from HTTP status alone.
- A future adapter interface may enrich flows from external admin APIs (for
  example, WireMock mappings), but generic traffic capture must not depend on
  an adapter.

### Implementation
1. **Configuration and session state**
   - each dependency accepts `mock.mode` (`builtin`, the default, or
     `external`) and an absolute `mock.upstream_url` required for external
     mode;
   - schemes and recursive targets are validated, and the mapping is retained
     per session plus `asset_id`;
   - the runtime API and JavaScript SDK expose the configuration while existing
     dependency declarations remain backward-compatible.
2. **Per-dependency capture gateway**
   - external dependencies route before the built-in stub registry;
   - only Phrond's internal session and dependency prefixes are stripped,
     preserving the application path and query exactly;
   - request and response bytes, status, end-to-end headers, and trailers are
     forwarded without redirects or implicit retries; `Host` is rewritten to
     the upstream by default with an explicit preserve-host option;
   - the gateway applies the same TLS controls, error-to-502 behavior, body
     fidelity, and future B4 capture limits as the other proxy paths;
   - the external response is returned to the SUT independently of artifact
     recording.
3. **Recording and correlation**
   - every completed or failed external exchange is recorded through the
     existing session recorder with the routed `asset_id`;
   - `X-Phrond-Trace-Id` is preserved so exact parent linking works when the SUT
     propagates it, with time-window nesting as the fallback;
   - zero-config serial sessions and explicit-session URLs are supported for
     parallel suites;
   - artifact types and `docs/artifact-openapi.yaml` include `mock_mode`,
     optional `transport_error`, and HTTP trailers.
4. **API behavior**
   - `queue-response` rejects an external dependency with a clear error
     rather than silently registering an unused Phrond stub;
   - `verify` behavior remains unchanged for built-in mocks; generic BYO
     mode cannot automatically identify consumed external stub fields;
   - upstream connection/TLS failures become recorded 502 exchanges without
     hiding the original error.
5. **Tests and examples**
   - focused gateway tests cover path/query/header/body fidelity, response
     fidelity, errors, session isolation, asset attribution, and trace
     correlation;
   - a mixed-mode integration test uses one built-in dependency and one
     external mock dependency exposing an overlapping route;
   - the runnable `complex-rest` example routes the payment SUT to a
     user-owned mock and the transaction service to the built-in mock, then
     exports their correlated artifacts as Workflow YAML, Postman, and HTML;
   - `docs/byo-mock-server.md` shows WireMock/Spring Boot integration where
     WireMock still owns stub setup while the SUT uses Phrond's capture URL.

Phrond does not own the external mock's lifecycle, reset behavior, scenarios,
or verification API. The test framework starts, configures, verifies, and
stops its chosen mock server exactly as it does today.

### Verified acceptance criteria
- An SUT call through an external-mock dependency receives the same status,
  end-to-end headers, and body the external server produced.
- The artifact contains one `sut_to_mock` flow with the correct `asset_id`,
  request/response, external source marker, and `parent_flow_id`, without a
  fabricated `mock_stub` or `unmatched` flag.
- Two external dependencies with identical routes remain correctly attributed,
  and built-in plus external dependencies work in the same session.
- External mode works for both zero-config serial tests and explicit parallel
  sessions without changing the external mock's routes.
- Connection and TLS failures return a deterministic 502 to the SUT and retain
  the upstream error in the finalized artifact flow.
- Existing built-in mock users require no configuration or SDK changes.

---

## E1 — Add Open Workflow YAML export alongside Postman — resolved

Status: completed 2026-07-24

### Resolution
- Added deterministic YAML export using Open Workflow Specification (formerly
  Serverless Workflow) DSL 1.0.3.
- The generated document nests captured calls by test case, traffic channel,
  and schema. HTTP interactions use standard `call: http` tasks with stable,
  sanitized names.
- Schema-matched and unmatched HTTP traffic are both exported. Captured
  responses, original multi-value request headers, flow ids, trace and parent
  ids, fingerprints, assets, consumed fields, mock stubs, channels, operations,
  schema ids, and roles are preserved under namespaced `metadata.phrond`
  fields.
- Workflow task bodies use the complete captured artifact body rather than the
  coverage preview; binary and content-encoding metadata are retained.
- `phrond-coverage --format workflow` and the aliases
  `serverless-workflow`/`serverless_workflow`/`sw` emit the new format.
- `POST /v1/report` now writes and returns `workflow_path` in addition to the
  existing `collection_path` and HTML report. The JavaScript SDK accepts
  `workflowPath`.
- Postman remains the CLI default and continues to be supported unchanged.
  HTML generation retains its existing Postman round-trip.

The workflow document is an import/replay projection for tools such as
Dispatch. Raw per-test Phrond artifacts remain the lossless input for
integration-drift detection.

---

## Non-goals of this roadmap
- Protobuf/gRPC *schema decoding* (recording raw frames is sufficient until a
  schema type exists).
- HTTP/3 (QUIC).
- Any server-side/broker functionality — the recorder stays a passive,
  self-contained capture tool (see design doc §11a).
