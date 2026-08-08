# Phrond product roadmap

Status: active
Last reviewed: 2026-08-09

Phrond's product focus is now exclusively test→SUT traffic capture, schema
validation, and reporting. The built-in mock server, response stack, dependency
gateway (including BYO forwarding), mock verification APIs, SDK helpers, and
mock-specific artifact/report fields were removed on 2026-08-08. Consumers use
their preferred mock tooling independently.

## Completed capture work

- B1/B2: compressed and binary body fidelity.
- B4: bounded streaming capture without truncating delivery.
- B5: upstream MITM failures return the same recorded HTTP 502 as plain and
  reverse proxy failures.
- B6: deterministic error responses and artifacts.
- C1: consumer-owned SUT lifecycle and low-ceremony named Node test API.
- C2: strict-by-default OpenAPI enforcement with explicit, deterministic
  OpenAPI 3.1 generation for teams bootstrapping a contract.
- E1: Postman JSON and Open Workflow Specification YAML exports.
- F1: capture-only product simplification; all mock-serving code and public
  mock APIs removed.

## Next: B3 transport and RPC capture

The next major track broadens capture fidelity without adding execution or
mocking responsibilities.

1. Record negotiated HTTP version and detect upgrade/streaming protocols.
2. Add safe WebSocket and SSE passthrough with connection metadata.
3. Support HTTP/2 on the upstream and client-side MITM paths.
4. Record gRPC, gRPC-Web, and Connect envelopes and trailers as opaque bytes.
5. Optionally decode protobuf payloads only when consumers attach descriptors.

Acceptance criteria:

- HTTP/1.1 behavior and existing artifacts remain compatible.
- gRPC and streaming traffic passes through without corruption.
- Protocol, method, status/trailers, timing, and bounded payload evidence are
  available in artifacts and exports.
- No service startup, workflow execution, dependency simulation, or
  proprietary RPC schema format is introduced.

## Later capture improvements

- Pluggable redaction profiles and safer defaults for credentials.
- Artifact compatibility/version tests for downstream integrations.
- Parallel-session stress and long-stream memory tests.
- Richer report filtering by test, tag, operation, status, and transport error.
