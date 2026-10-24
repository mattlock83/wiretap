# Examples

Start with the example for your test runner. Each managed integration owns
startup, test capture, exports and shutdown while retaining your assertions and
mocks. The [SDK guide](../sdk/js/README.md) covers Jest, Mocha, Playwright and
plain JavaScript; the [Go guide](../sdk/go/README.md) uses standard `testing`.

Run these commands from the repository root. Each example prints its capture
directory. Keep the whole archive: `bundle.json`, `tests/`, captured payloads,
contracts and exports. Receipts give exact output paths. JavaScript managed runs
write `last-run.json` beside their run directories; Go packages write it inside
each run directory so concurrent packages cannot overwrite one another.

The main HTTP API requires its OpenAPI spec by default. HTTP dependencies and
mocks need only their URL and kind; examples that supply dependency specs opt
into their validation and contract coverage. Without a dependency spec, calls
and Postman examples remain available with unknown contract coverage. Native
gRPC always needs descriptors, using reflection when no local file is supplied.
See [schema defaults](../docs/configuration.md#schema-defaults-by-boundary).

| Example | Demonstrates | Requirements |
| --- | --- | --- |
| [Existing Jest suite](existing-jest/README.md) | Pinned external Express API, 120 unchanged tests, automatic SDK setup and Postman schema/content checks. | Git, Go 1.26+, Node.js 22+. |
| [Mocha](mocha-runner/README.md) | Config wrapper, ordinary tests/hooks and an existing shared client; automatic coverage and Postman exports. | Go 1.26+, Node.js 22.12+. |
| [Playwright Test](playwright-runner/README.md) | Copyable Todo API suite with managed capture, ordinary request fixtures and coverage exports. | Go 1.26+, Node.js 22+, Playwright Test 1.64.1+ within 1.x. |
| [Go test suite](go-suite/README.md) | Two lifecycle hooks, an existing HTTP handler/client, final test outcomes and automatic exports. | Go 1.26+. |
| [Plain JavaScript / Node test](node-suite/README.md) | Two test files sharing one managed runtime, negative tests and finalization after failures. | Go 1.26+, Node.js 22+. |
| [Basic REST](basic-rest/README.md) | Advanced external runtime, HTTP dependency mock and optional parallel Jest capture. | Go 1.26+, Node.js 22+, Bash and curl. |
| [Microsvc Transactions](microsvc-transactions/README.md) | Advanced Docker harness, real service image, Accounts mock, disposable database and Python HTTP lifecycle. | Go 1.26+, Python 3.9+, Docker and the documented local images. |
| [Native gRPC](grpc-boundaries/README.md) | Advanced external runtime, generated grpc-go clients, reflection/local contracts and coverage. | Go 1.26+, Bash, Python 3 and curl. |
| [Streaming gRPC](grpc-streaming/README.md) | Advanced external runtime, all four RPC shapes, provider/dependency calls and structural coverage. | Go 1.26+, Bash, Python 3 and curl. |
| [Native Node gRPC](grpc-boundaries/node/README.md) | Ordinary Node clients/mocks, a Go provider, local contracts, traced parallel and mixed paths, TLS and offline coverage rebuilding. | Go 1.26+, Node.js 22+, Bash and OpenSSL. |

To compare a consumer run with the provider's own test evidence, use the
[assumption checker](../docs/assumption-checks.md). A provider contract can
validate schema-free HTTP mock examples, but it does not create missing
consumer contract coverage or replace a matching passing provider test.

## Basic REST

These advanced examples retain an outer harness because it must configure
application and mock processes around capture listeners. Keep that harness as
the sole runtime owner; do not add a second managed runner integration to them.

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

For parallel Jest capture against the instrumented demo application:

```bash
WIRETAP_TEST_RUNNER=jest ./examples/run-basic-rest-e2e.sh
```

## Microsvc Transactions

```bash
./examples/run-microsvc-transactions.sh
```

Read the [Microsvc guide](microsvc-transactions/README.md) for image requirements,
contract scope and reading a capture.

## Native gRPC

```bash
./examples/grpc-boundaries/run.sh
```

For the Node interoperability companion:

```bash
npm ci --prefix sdk/js
npm ci --prefix examples/grpc-boundaries/node
./examples/grpc-boundaries/run-node.sh
```

For all four RPC shapes and their captured message timelines:

```bash
./examples/grpc-streaming/run.sh
```
