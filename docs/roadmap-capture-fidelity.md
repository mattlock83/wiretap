# Phrond product roadmap

Status: active
Last reviewed: 2026-08-25

Phrond focuses on correlated test→SUT and SUT→dependency traffic capture,
strict schema validation, and reporting. The built-in mock server, response
stack, mock verification APIs, and mock-serving artifact fields remain removed.
Consumers keep their preferred mock tooling independently; Phrond forwards to
those existing servers solely to capture and correlate the traffic.

## Completed capture work

- B1/B2: compressed and binary body fidelity.
- B4: bounded streaming capture without truncating delivery.
- B5: upstream MITM failures return the same recorded HTTP 502 as plain and
  reverse proxy failures.
- B6: deterministic error responses and artifacts.
- C1: consumer-owned SUT lifecycle and low-ceremony named Node test API.
- C2: strict-by-default OpenAPI enforcement with explicit, deterministic
  OpenAPI 3.1 generation for teams bootstrapping a contract.
- C3: optional immediate dependency-response field assertions linked to the
  captured flow, preserving full payloads and failing the owning test on drift.
- E1: Postman JSON and Open Workflow Specification YAML exports.
- E2: standard Pact V3 export from successful captured dependency traffic,
  including stable participant naming, producer/version metadata, optional
  provider states, assertion-selected response fields, OpenAPI-derived
  matchers, credential filtering, deterministic interaction deduplication,
  and one broker-ready contract per consumer/provider pair.
- E3: real Pact Broker import compatibility and an independently owned
  orders/inventory consumer-provider demonstration, including disposable
  SQLite-backed local Broker startup, duplicate-safe publication, standard
  provider-state setup, official Pact V3 provider verification, and published
  verification results.
- F1: capture-only product simplification; all mock-serving code and public
  mock APIs removed.
- F2: restore external-only dependency capture on the existing proxy listener,
  with strict dependency schemas, stable asset identity, per-test isolation,
  parent-flow correlation, and dual-channel HTML/Postman/Workflow reporting.

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
  proprietary RPC schema format is introduced; externally owned dependencies
  remain capture-only forwarding targets.

## Later capture improvements

- Pluggable redaction profiles and safer defaults for credentials.
- Artifact compatibility/version tests for downstream integrations.
- Parallel-session stress and long-stream memory tests.
- Richer report filtering by test, tag, operation, status, and transport error.
- Additional assertion operators and richer schema-derived Pact matchers.
