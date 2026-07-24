# Capture Fidelity and Export Roadmap — Outstanding Items

Status: open
Last reviewed: 2026-07-24

These are the known gaps and recently completed work from the capture-fidelity
review, plus independent export work. B3 and B4 remain before the recorder can
claim lossless capture. They are deliberately deferred (v1 is scoped to plain
HTTP request/response), but each can silently corrupt or bloat artifacts when
hit, so they are specified here in enough detail to implement without
re-deriving context. Item numbering (B3–B5) is historical, from the original
capture-fidelity review; B1/B2 (content-encoding + binary bodies), B6
(deterministic responses), and D (session-correlation friction) are already
resolved — see `testing-tool-requirements-and-design.md` §11a. B5 was completed
on 2026-07-24. E1 is a separate coverage-export track and does not change the
canonical Phrond artifact.

Suggested order for the remaining work: **B4 → B3** (B4 is contained; B3
needs design). E1 can proceed independently.

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

## B4 — No capture size caps (unbounded memory + artifact bloat)

Priority: high (CI stability) · Effort: small-medium

### Problem
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

### What needs to be done
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

### Acceptance criteria
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

## E1 — Replace the Postman export with Open Workflow YAML

Priority: medium (vendor-neutral executable export) · Effort: medium

### Goal
Replace the Postman v2.1 collection export with deterministic YAML conforming
to the CNCF Open Workflow Specification (formerly Serverless Workflow). The
workflow export is a replay/orchestration projection of captured tests; the
per-test Phrond artifact remains the lossless source of truth for broker-side
integration detection.

### Current state
- `phrond-coverage` defaults to `--format postman` and writes a combined Postman
  collection containing captured requests, saved responses, and Phrond metadata
  embedded in descriptions.
- `POST /v1/report` accepts `collection_path` and defaults to
  `coverage.postman_collection.json`.
- The HTML report serializes to Postman and parses it back before rendering.
- Examples, SDK report options, documentation, and coverage tests expose
  Postman-specific names and behavior.

### What needs to be done
1. **Define the workflow projection** against a pinned stable Open Workflow DSL
   schema (currently 1.0.3):
   - emit one independently runnable workflow per test case, with a deterministic
     package/manifest when multiple artifacts are exported;
   - translate `test_to_sut` flows into ordered HTTP `call` tasks using stable,
     sanitized task names;
   - parameterize SUT base URLs so captured ephemeral addresses are not baked
     into the workflow;
   - encode captured response expectations as explicit checks that raise a
     workflow error on mismatch;
   - preserve causally linked `sut_to_mock` expectations, `asset_id`,
     `mock_stub`, `consumed_fields`, fingerprints, and coverage classification
     under namespaced Phrond metadata rather than executing dependency calls a
     second time;
   - preserve redaction and binary-body semantics.
2. **Add a native workflow writer** in `internal/coverage` that emits
   deterministic YAML and validates generated documents against the pinned
   official schema in tests.
3. **Replace the public export surface**:
   - make `--format workflow` the CLI default and remove
     `--format postman`/`collection`;
   - replace runtime/SDK `collection_path` with `workflow_path` and use a
     `.workflow.yaml` default;
   - update README, runtime API docs, and example scripts.
4. **Decouple HTML from Postman** by rendering the internal `coverage.Report`
   directly. Keep HTML and JSON report formats, but delete the Postman models,
   metadata codec, reverse parser, and Postman-specific tests.
5. **Document the boundary**: Open Workflow YAML is an executable export for
   replay and orchestration; raw Phrond artifacts remain the input to
   integration-drift detection.

### Acceptance criteria
- `phrond-coverage --format workflow` produces deterministic YAML that validates
  against the pinned Open Workflow schema.
- A conforming workflow runtime can replay a generated test workflow against a
  configurable SUT endpoint and fails when a captured response expectation is
  violated.
- Downstream mock assumptions and coverage metadata survive the projection
  without producing duplicate dependency calls during replay.
- The runtime API, JavaScript SDK, examples, and docs expose
  `workflow_path`/`.workflow.yaml` and no longer expose Postman output.
- HTML generation uses `coverage.Report` directly and produces the same
  coverage information without a Postman serialization round-trip.
- The canonical per-test Phrond artifact and its OpenAPI schema are unchanged.

---

## Non-goals of this roadmap
- Protobuf/gRPC *schema decoding* (recording raw frames is sufficient until a
  schema type exists).
- HTTP/3 (QUIC).
- Any server-side/broker functionality — the recorder stays a passive,
  self-contained capture tool (see design doc §11a).
