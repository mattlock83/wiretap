# Examples

Run these commands from the repository root. Examples capture existing tests
and print their capture directory. Inspect `bundle.json`, the attempts in
`tests/`, and `exports/coverage.json` for JSON coverage.

| Example | Demonstrates | Requirements |
| --- | --- | --- |
| [Existing Jest suite](existing-jest/README.md) | Pinned external Express API, 120 unchanged tests, automatic SDK setup and Postman parser checks. | Git, Go 1.26+, Node.js 22+. |
| [Playwright Test](playwright-runner/README.md) | Copyable Todo API suite with managed capture, ordinary request fixtures and coverage exports. | Go 1.26+, Node.js 22+, Playwright Test 1.64+. |
| [Complete Node suite](node-suite/README.md) | Copyable two-file harness, startup/readiness, negative tests, framework outcomes and finalization after failures. | Go 1.26+, Node.js 22+. |
| [Basic REST](basic-rest/README.md) | Existing Node tests and an HTTP dependency mock; optional parallel Jest capture. | Go 1.26+, Node.js 22+, Bash and curl. |
| [Microsvc Transactions](microsvc-transactions/README.md) | A real service image, an Accounts mock, disposable database state and authenticated tests. | Go 1.26+, Python 3.9+, Docker and the documented local images. |
| [Native gRPC](grpc-boundaries/README.md) | Existing grpc-go test, gRPC SUT and gRPC mock, reflection/local contracts and JSON coverage. | Go 1.26+, Bash, Python 3 and curl. |
| [Streaming gRPC](grpc-streaming/README.md) | All four RPC shapes, traced provider and dependency calls, per-message evidence and structural coverage. | Go 1.26+, Bash, Python 3 and curl. |
| [Native Node gRPC](grpc-boundaries/node/README.md) | Ordinary Node clients/mocks, a Go provider, local contracts, traced parallel and mixed paths, TLS and offline coverage rebuilding. | Go 1.26+, Node.js 22+, Bash and OpenSSL. |

## Basic REST

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
