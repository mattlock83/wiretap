# Native gRPC support

Wiretap captures native gRPC traffic, validates supplied contracts, writes raw
artifacts and reports structural coverage. It uses the existing test lifecycle
and ordinary generated clients. Managed SDK adapters own their Wiretap process;
project-owned harnesses can use the same control API directly.

## Supported scope

- Unary, server-streaming, client-streaming and bidirectional RPCs use bounded
  incremental observation, exact frame counts, per-message timing, explicit
  omissions and immutable lifecycle snapshots.
- Provider, dependency and mixed boundaries support h2c and TLS. Capture listeners
  are allocated before tests begin, and can be read before starting a SUT that
  needs their addresses. Parallel ownership uses W3C tracing; sequential capture
  requires no application tracing.
- Reflection v1/v1alpha is the default. Local proto sources/import roots and
  descriptor sets work without reflection or external compilation executables.
  Contracts freeze once per run and support offline reporting.
- Native grpc-go, Connect clients in native gRPC mode and Node `@grpc/grpc-js`
  clients interoperate. Existing generated clients and application instrumentation
  remain in use.
- Evidence distinguishes HTTP transport status from terminal gRPC status and
  retains ordered request/response messages, metadata, trailers, half-close,
  completion and direction-specific capture limits. Cancellation, drain expiry
  and shutdown preserve partial evidence without late attribution.
- Rich `google.rpc.Status` errors retain a verified envelope and ordered typed or
  opaque details. Boundary descriptors and frozen standard error types resolve
  offline. Decode, retention and recursion budgets yield explicit unavailable
  evidence when exceeded.
- Payload attachments are hashed `.bin` files. Readers verify message bytes,
  projections, descriptors and references before coverage or export.

The raw format is **3.1**. Required capabilities `grpc-observation.v1` and
`grpc-status-details.v1` identify guarantees recorded by newer captures. Earlier
supported 3.x records remain readable without acquiring guarantees they did not
record; readers reject unknown required capabilities. Current capture retains
synthetic test values unchanged. Readers preserve legacy redaction flags only as
compatibility evidence limits.

## Coverage and exports

The `wiretap-protobuf-2` measurement profile covers selected methods and supported
conditions in complete retained messages, including streams: explicit presence,
oneofs, enums, booleans, collections and nested message conditions. Descriptor
normalization remains `wiretap-protobuf-1`.

Incomplete, omitted or undecodable evidence earns no successful payload coverage;
the same restriction applies to legacy-redacted records. Empty streams do not
invent payload conditions. Protobuf does not declare permitted status codes,
stream lengths or cross-message business rules, so these do not become coverage
denominators. Recursive scopes, dynamic `Any` expansion and arbitrary validation
annotations/CEL are outside passive condition measurement. See the
[coverage rules](contract-coverage-rules.md).

Postman exports use native gRPC v3 JSON for all four RPC shapes. Complete error
responses remain examples; transport/capture gaps fail strict export or appear
as explicit omissions when requested. The
[compatibility harness](../tests/postman-compat/README.md) verifies generated JSON
and captured-content preservation. Desktop import and imported-collection
invocation remain unverified. See the [export guide](artifacts.md).

## Verification and limits

The Go suite covers provider/dependency/mixed boundaries, TLS, reflection/local
contracts, all four RPC shapes, fragmentation, empty messages, gzip, limits,
early errors, cancellation, drain/shutdown, parallel ownership, rich errors,
legacy-record integrity and offline reporting.

The [Node companion](../examples/grpc-boundaries/node/README.md) covers serial and
parallel Go/Node and HTTP/gRPC paths, standard OTel setup, metadata,
trailers-only failures, deadlines and TLS. The `grpc-node.yml` workflow runs it.
The [streaming example](../examples/grpc-streaming/README.md) exercises all RPC
shapes and bounded capture.

Connect's separate wire protocol, gRPC-Web, mTLS and remote schema acquisition
such as BSR imports are not supported. Supply local imports or a complete
descriptor set; reflection credentials and TLS trust stay outside the artifact.
See the [gRPC user guide](grpc-user-guide.md) for current setup.
