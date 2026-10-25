# Plain JavaScript with Node's test runner

Copy this directory into an existing test project to get a working starting
point. It uses the plain JavaScript SDK's Node test adapter, two test files and
one parent harness. Prefer the [Jest](../existing-jest/README.md),
[Mocha](../../sdk/js/docs/mocha.md) or [Playwright](../playwright-runner/README.md)
adapter when your suite uses that runner. All traffic is synthetic; Wiretap stores payloads and dummy
credentials unchanged.

From the Wiretap checkout with Go 1.26+ and Node 22+:

```bash
mkdir -p dist
go build -o dist/wiretap ./cmd/wiretap
npm ci --prefix sdk/js
cd examples/node-suite
npm install --no-save ../../sdk/js
node run-tests.mjs --binary ../../dist/wiretap
```

On Windows, build and pass `wiretap.exe`. For another project, install the
[packed SDK](../../docs/local-release.md) as a development dependency instead of
the relative source checkout. `WIRETAP_BINARY` can supply a persistent runtime
path, so `npm test` runs the harness without extra arguments.

| File | Adapt it for your project |
| --- | --- |
| [run-tests.mjs](run-tests.mjs) | Start your service, call `startManagedRuntime`, run all workers, then call `owner.close()`. |
| [service.mjs](service.mjs) | Replace the synthetic service with existing application/mock startup. |
| [.wiretap.yaml](.wiretap.yaml) | Declare API identities, URLs, contracts and desired outputs. |
| [orders.openapi.yaml](orders.openapi.yaml) | Replace with your real test contract. |
| [test/orders.test.cjs](test/orders.test.cjs) | Retain existing assertions inside the outcome-aware adapter. |
| [test/invalid-order.test.cjs](test/invalid-order.test.cjs) | A deliberate invalid request correctly produces an asserted `400` and a passing test. |

Each file uses `manageRuntime: false`. Worker cleanup closes its own attempts;
only `run-tests.mjs` finalizes the shared run after the test process exits.
`startManagedRuntime` chooses available control and proxy ports, waits for
readiness, and starts capture. Its `close()` method finalizes configured exports,
saves `last-run.json` and `wiretap.log` inside that run's archive, and stops the
owned runtime even when an export fails. The harness keeps its application alive
until that completes.
Sequential file execution works with an uninstrumented SUT. Parallel workers
need [W3C context propagation or isolated services](../../docs/otel-adoption.md).

Use the printed raw capture path to find the complete run at
`artifacts/<run-id>/`. Its `last-run.json` is the canonical finalization receipt
with exact raw, coverage and Postman paths; `exports/result.json` also records
any export failures. `artifacts/last-run.json` is an advisory latest snapshot
that can be replaced by another run.
`WIRETAP_OUTPUT_DIR` selects an absolute artifact root for CI.
See the [plain JavaScript guide](../../sdk/js/docs/javascript.md) for custom
test frameworks and an already-running Wiretap process.

Set `WIRETAP_EXAMPLE_FAIL=1` to make the positive test's `t.after` cleanup fail.
The adapter records that failure, the other test still runs, the parent writes
exports, and the command exits with the test process's original nonzero code.
For example on a POSIX shell:

```bash
WIRETAP_EXAMPLE_FAIL=1 node run-tests.mjs --binary ../../dist/wiretap
```

The repository's clean-package smoke test copies this exact example outside the
checkout, installs the packed SDK and checks both success and failure runs on
Linux, macOS and Windows. See [CI integration](../../docs/ci-example.md).
