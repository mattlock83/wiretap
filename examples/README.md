# Examples

Run these commands from the repository root. Examples write capture bundles;
the HTTP examples can also explore inside their tests and record generated cases
in that same bundle. They print the path. Open
`docs/viewer.html` for the dashboard or `index.md` for the OKF pages.

| Example | Demonstrates | Requirements |
| --- | --- | --- |
| [Basic REST](basic-rest/README.md) | Existing Node tests and an HTTP dependency mock; optional parallel Jest capture; generated invalid-input checks. | Go 1.24+, Node.js 20.12+, Bash and curl. |
| [Provider exploration](../docs/exploration.md#try-the-complete-examples) | SQLite fixtures, fresh authentication, cleanup and side-effect assertions through local Python callbacks. | Go 1.24+ and Python 3.9+. |
| [Microsvc Transactions](microsvc-transactions/README.md) | A real service image, an Accounts mock, disposable database state and authenticated exploration. | Go 1.24+, Python 3.9+, Docker and the documented local images. |
| [Native gRPC](grpc-boundaries/README.md) | Existing grpc-go test, gRPC SUT and gRPC mock, reflection/local contracts and OKF coverage. | Go 1.24+, Bash, Python 3 and curl. |
| [Streaming gRPC](grpc-streaming/README.md) | All four RPC shapes, traced provider and dependency calls, per-message evidence and structural coverage. | Go 1.24+, Bash, Python 3 and curl. |
| [Native Node gRPC](grpc-boundaries/node/README.md) | Ordinary Node clients/mocks, a Go provider, local contracts, traced parallel and mixed paths, TLS and offline bundle rebuilding. | Go 1.24+, Node.js 20.12+, Bash and OpenSSL. |

## Basic REST

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

For parallel Jest capture against the instrumented demo application:

```bash
WIRETAP_TEST_RUNNER=jest ./examples/run-basic-rest-e2e.sh
```

## Provider exploration with local database callbacks

```bash
python3 examples/provider-exploration/demo.py
```

## Microsvc Transactions

```bash
./examples/run-microsvc-transactions.sh
```

Read the [Microsvc guide](microsvc-transactions/README.md) for image requirements,
contract scope and replaying an existing capture.

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

For all four RPC shapes and their bundle timelines:

```bash
./examples/grpc-streaming/run.sh
```
