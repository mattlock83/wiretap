# gRPC support and follow-ups

Native gRPC unary and streaming capture was reviewed and completed on
2026-10-05. Local Wiretap captures traffic, validates contracts, writes raw
artifacts and reports structural coverage. It uses the existing test lifecycle
and ordinary generated clients.

## Delivered scope

- Unary, server-streaming, client-streaming and bidirectional RPCs use incremental
  bounded observation, exact frame counts, per-message timing, explicit omissions
  and immutable lifecycle snapshots.
- Provider, dependency and mixed boundaries support h2c and TLS. Listeners are
  allocated before the SUT starts. Parallel ownership uses ordinary W3C tracing;
  sequential capture requires no application tracing.
- Reflection v1/v1alpha is the default. Local proto sources/import roots and
  descriptor sets work without reflection or external compilation executables.
  Contracts freeze once per run and rebuild reports offline.
- Native grpc-go, Connect clients in native gRPC mode and Node `@grpc/grpc-js`
  clients interoperate. No mandatory Wiretap RPC client or automatic application
  instrumentation is introduced.
- Evidence distinguishes HTTP transport status from terminal gRPC status and
  retains ordered request/response messages, metadata, trailers, half-close,
  completion and direction-specific capture limits. Cancellation, drain expiry
  and shutdown produce immutable partial evidence without late attribution.
- Rich `google.rpc.Status` errors retain a verified envelope and ordered typed or
  opaque details. Contract-local descriptors and frozen standard error types
  resolve offline. Redaction removes containing wire/envelope copies. Decode,
  retention and recursion budgets yield explicit unavailable evidence.
- Payload attachments are hashed `.bin` files. Readers verify message bytes,
  projections, descriptors and references before coverage or export.

Bundle evidence is **3.1**. The required capabilities `grpc-observation.v1` and
`grpc-status-details.v1` identify guarantees recorded by newer captures. Earlier
3.x evidence remains readable without acquiring guarantees it did not record;
readers reject required capabilities they do not understand.

## Coverage

The `wiretap-protobuf-2` measurement profile covers declared methods and supported
conditions in every complete retained message, including streams: explicit
presence, oneofs, enums, booleans, collections and nested message conditions.
Descriptor normalization remains `wiretap-protobuf-1`.

Incomplete, omitted, redacted or undecodable evidence earns no successful payload
coverage. Empty streams do not invent payload conditions. Protobuf does not
declare permitted status codes, stream lengths or cross-message business rules;
these do not become artificial denominators. Recursive scopes, dynamic `Any`
values and arbitrary validation annotations/CEL remain qualified limitations.
See [coverage rules](contract-coverage-rules.md).

## Verification

The Go suite covers all four RPC shapes across provider/dependency/mixed
boundaries, TLS, reflection/local contracts, fragmentation, empty messages, gzip,
limits, early errors, cancellation, drain/shutdown, parallel ownership, rich
errors, redaction, integrity and offline reporting. Native clients see the same
application results directly and through Wiretap.

The [Node companion](../examples/grpc-boundaries/node/README.md) covers serial and
parallel Go/Node and HTTP/gRPC paths, standard OTel setup, metadata,
trailers-only failures, deadlines and TLS. The `grpc-node.yml` CI workflow runs
the companion. The [streaming example](../examples/grpc-streaming/README.md)
exercises all streaming shapes and bounded capture.

Use `go test ./...` and the JavaScript SDK suite for regressions. Shared
transport/session/capture changes also warrant `go test -race ./...` and the
relevant native-client examples. Export checks cover native gRPC Postman v3 JSON;
a real Postman desktop import/export round trip remains to be verified.

## Follow-ups

1. Broader semantic validation and finite coverage for supported annotations.
   Preserve unknown/custom rules without claiming enforcement or enumerable CEL.
2. Connect and gRPC-Web wire-format adapters with independent transport conformance
   tests. Connect's native gRPC client mode already works.
3. mTLS and optional remote schema convenience, such as pinned BSR imports. Keep
   credentials outside artifacts and retain complete offline descriptor snapshots.
4. Future provider verification, dependency variation and generated checks through
   a separately designed Hub runner. Execution hooks, fixtures and credentials
   stay outside the capture runtime, SDK and artifact.

See the [gRPC user guide](grpc-user-guide.md) for current setup and the
[roadmap](roadmap.md) for product priorities.
