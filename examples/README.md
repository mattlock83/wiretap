# Examples

Start with the [consumer/provider workflow](consumer-provider/README.md) to see
capture, agreement, a provider change and repair. For integration details, choose
the example for your test runner. Each managed integration owns
startup, test capture, exports and shutdown while retaining your assertions and
mocks. The [SDK guide](../sdk/js/README.md) covers Jest, Mocha, Playwright and
plain JavaScript; the [Go guide](../sdk/go/README.md) uses standard `testing`,
and the [Python guide](../sdk/python/README.md) integrates pytest or a custom harness.

Run these commands from the repository root. Each example prints its capture
directory. Keep the whole archive: `bundle.json`, `tests/`, captured payloads,
contracts and exports. Receipts give exact output paths. Managed owners write `last-run.json` and
bounded logs inside each run directory so concurrent suites retain their own
records. JavaScript also maintains an advisory latest receipt beside the runs.

The main HTTP API requires its OpenAPI spec by default. HTTP dependencies and
mocks need only their URL and kind; examples that supply dependency specs opt
into their validation and contract coverage. Without a dependency spec, calls
and Postman examples remain available with unknown contract coverage. Native
gRPC always needs descriptors, using reflection when no local file is supplied.
See [schema defaults](../docs/configuration.md#schema-defaults-by-boundary).

| Example | Demonstrates | Requirements |
| --- | --- | --- |
| [Consumer/provider workflow](consumer-provider/README.md) | Real captures and Postman exports; passing checks, an intentional provider incompatibility, then a repair. | Go 1.26+, built Wiretap binary. |
| [Existing Jest suite](existing-jest/README.md) | Pinned external Express API, 120 unchanged tests, automatic SDK setup and Postman schema/content checks. | Git, Go 1.26+, Node.js 22+. |
| [Playwright Test](playwright-runner/README.md) | Copyable Todo API suite with managed capture, ordinary request fixtures and coverage exports. | Go 1.26+, Node.js 22+, Playwright Test 1.64.1+ within 1.x. |
| [Python / pytest](pytest-suite/README.md) | Existing urllib client, parametrized tests, fixture outcomes and worker-owned capture. | Go 1.26+, Python 3.9+, local SDK with pytest extra. |
| [Go test suite](go-suite/README.md) | Two lifecycle hooks, an existing HTTP handler/client, final test outcomes and automatic exports. | Go 1.26+. |
| [Plain JavaScript / Node test](node-suite/README.md) | Two test files sharing one managed runtime, negative tests and finalization after failures. | Go 1.26+, Node.js 22+. |
| [Native gRPC](grpc-boundaries/README.md) | Advanced external runtime, generated grpc-go clients, reflection/local contracts and coverage. | Go 1.26+, Bash, Python 3 and curl. |
| [Streaming gRPC](grpc-streaming/README.md) | Advanced external runtime, all four RPC shapes, provider/dependency calls and structural coverage. | Go 1.26+, Bash, Python 3 and curl. |
| [Native Node gRPC](grpc-boundaries/node/README.md) | Ordinary Node clients/mocks, a Go provider, local contracts, traced parallel and mixed paths, TLS and offline coverage rebuilding. | Go 1.26+, Node.js 22+, Bash and OpenSSL. |

To compare a consumer run with the provider's own test evidence, use the
[assumption checker](../docs/assumption-checks.md). A provider contract can
validate schema-free HTTP mock examples, but it does not create missing
consumer contract coverage or replace a matching passing provider test.

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
