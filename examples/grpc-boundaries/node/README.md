# Native Node gRPC example

This companion to the Go example uses `@grpc/grpc-js`, `@grpc/proto-loader`,
Node's existing test runner and Wiretap's JavaScript lifecycle library. It has
no generated-client build step, reflection service or collector requirement.
The mock and provider have ordinary gRPC implementations and do not import
Wiretap. Local `.proto` files supply both contracts.

This is an advanced protocol fixture: each group controls its own external
runtime so it can test TLS, mixed protocols, cancellation and offline rebuilding.
For ordinary Node suites use the [managed Node example](../../node-suite/README.md)
or your [runner's adapter](../../../sdk/js/README.md). Do not add another runtime
owner to this fixture.

From the repository root:

```bash
npm ci --prefix sdk/js
npm ci --prefix examples/grpc-boundaries/node
./examples/grpc-boundaries/run-node.sh
```

Requires Go 1.26+, Node.js 22+, Bash and OpenSSL with `req -addext` support.
Go builds the Wiretap binary and the existing Go provider used by the cross-language
case. The other providers and mocks run in Node. The runner uses temporary local
ports and creates a fresh one-day development TLS certificate. It stops processes
and removes temporary contracts, certificates and private keys when finished.
It never downloads packages during the run; missing dependencies fail with the
installation command.

Bundles are written beneath `artifacts/grpc-node/`; set
`WIRETAP_GRPC_ARTIFACT_DIR` to choose another directory. Each test group prints
its capture path. Inspect `bundle.json`, `tests/`, and `exports/coverage.json`.

The fixture exercises:

- An uninstrumented Node test → Go provider → Node mock, captured sequentially.
- A Node provider without dependencies, using h2c and TLS on both sides of
  Wiretap, with a local CA trusted by the ordinary client.
- Direct versus captured success, trailers-only `INVALID_ARGUMENT`, repeated
  and binary metadata, a valid zero-byte Protobuf response, deadlines and
  explicit cancellation. Cancellation is retained as incomplete/failed evidence,
  not successful provider coverage.
- Two overlapping tests on a reused channel, through a Node gRPC provider and
  Node mock, and through an HTTP provider calling that same gRPC mock. Standard
  OTel propagates each test's trace through the provider's ordinary outbound client.
- Raw capture ownership, API roles, numeric RPC status (including zero),
  payload hashes and frozen local contracts. The fixture stops upstreams and
  deletes source contracts before rebuilding JSON coverage from the capture.

`tracing.cjs` shows application-owned OTel setup loaded **before** importing gRPC
and HTTP modules. The SDK preserves the application's tracer provider and context
manager. In an existing application, use its existing standard instrumentation;
do not register a second provider. HTTP instrumentation does not instrument gRPC.

`test/serial.test.cjs` deliberately does not load that instrumentation. Wiretap
can associate one active test with its dependency calls; overlapping tests need
propagation through the application or separate test environments.

The tests retain `node:test`, their normal clients, assertions and lifecycle.
`runtime.runTest` activates a callback's test context and closes its capture;
the harness owns starting Wiretap, the application and mocks. No Wiretap-specific RPC client
or test framework is involved. The dedicated `grpc-node` CI workflow repeats
this command.
These protocol callbacks explicitly describe their capture outcomes. Use the
runner adapters for ordinary tests so failures in runner cleanup hooks are also
reflected in the final test outcome.
