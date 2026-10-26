# Wiretap

Turn existing Jest HTTP API tests into **portable evidence, Arazzo workflows and API coverage**. Keep your clients, assertions and mocks. Wiretap captures application boundaries, measures supported OpenAPI conditions and checks captured mock assumptions against provider test evidence.

Use isolated tests with synthetic data and dummy credentials. Captured traffic, including payloads and credentials, is retained unchanged.

```text
Jest tests → Wiretap → Your application → Wiretap → Existing mocks or services
                         One portable run archive
                     Raw evidence · Arazzo · API coverage
```

## Try it

From this checkout, with Go 1.26+ and Node 22+:

```sh
node examples/existing-jest/run.mjs
```

The [Express/Jest demo](examples/existing-jest/README.md) builds Wiretap, installs the local SDK and captures 120 existing tests. It verifies that their original outcomes remain intact and checks HTML/JSON coverage and Arazzo outputs. It publishes nothing.

## Add it to your Jest suite

Build the runtime and pack the SDK:

```sh
go build -o dist/wiretap ./cmd/wiretap
npm ci --prefix sdk/js
npm pack ./sdk/js --ignore-scripts --pack-destination ./dist
```

Install the printed tarball in your test project and put `wiretap` on `PATH`, or set `WIRETAP_BINARY` to its absolute path. Use Node 22+ and Jest 29.7 or 30.

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');
module.exports = withWiretap(existingConfig, { app: '<rootDir>/app.js' });
```

Point the shared client at capture, for example `supertest(process.env.WIRETAP_SUT_URL || app)`. Existing test imports, assertions and mocks stay in place. The adapter hosts the supplied HTTP handler inside Jest, starts capture, records final test outcomes and finalizes the run. For an already running API, omit `app` and configure `sut.url`.

```yaml
version: 1
application: {id: orders-app, name: Orders}
output_dir: artifacts/wiretap
sut:
  id: orders
  kind: service
  schema: schemas/orders.openapi.yaml
```

The main API requires OpenAPI by default. HTTP dependencies and mocks need only their URL and kind; adding a schema enables their validation and contract coverage. Supplied schemas always validate. See the [Jest SDK](sdk/js/README.md), [getting started guide](docs/getting-started-existing-tests.md) and [configuration reference](docs/configuration.md).

For parallel tests sharing an application, propagate W3C trace context through its server and outbound clients, or isolate workers. The adapter uses one worker by default. See [tracing and concurrency](docs/otel-adoption.md).

## Read the results

| Output | Purpose |
| --- | --- |
| `bundle.json`, `tests/`, `contracts/`, `payloads/` | Captured requests/responses, final outcomes, validation diagnostics and frozen contracts. |
| `exports/coverage.json` | Detailed operation and contract-condition evidence. |
| `exports/coverage.html` | Offline dashboard with API coverage, gaps and linked test runs. |
| `exports/arazzo/arazzo.json` and companions | JSON/REST workflows with portable OpenAPI sources. |
| `exports/result.json` | Exact export paths and errors. |
| `last-run.json`, `wiretap.log` inside the run | Managed completion receipt and bounded runtime log. |

Retain the complete run directory. Use the archive path returned and printed by the adapter; its receipt stays inside that run. Reports and exports also work offline:

```sh
wiretap report --bundle ./artifacts/wiretap/run-... --format text
wiretap report --bundle ./artifacts/wiretap/run-... --format html --output report.html
wiretap report --bundle ./artifacts/wiretap/run-... --format all
wiretap report --bundle ./artifacts/wiretap/run-... --format arazzo --output-dir ./arazzo
```

Open `exports/coverage.html` directly in a browser. You can also [rebuild the dashboard from coverage JSON, specs and Arazzo](docs/artifacts.md#offline-html-dashboard) without the runtime or original workspace.

Arazzo exports supported JSON/REST workflows. Incomplete or unrepresentable eligible traffic can make export fail; retained raw evidence remains available. See [artifacts and exports](docs/artifacts.md) and the [machine interface](docs/machine-interface.md).

## Check consumer/provider assumptions

Restore complete consumer and provider runs with their Arazzo exports, then compare them offline:

```sh
wiretap check --consumer ./orders-run --provider ./inventory-run --dependency inventory
```

The checker verifies each Arazzo workflow and source companion against its capture archive. Each mock request and paired response needs support from a complete passing provider test. It can also compare exercised OpenAPI 3.0 rules. Missing provider coverage, changed response values and declared rule drift remain distinct findings. A passing check covers the selected examples and supported rules; assertions about business behavior remain in your tests.

Use [run sets](docs/run-sets.md) to combine explicitly selected shards of one build. See [assumption checks](docs/assumption-checks.md), the [CI example](docs/ci-example.md) and the [documentation index](docs/README.md).

This experimental branch supports Jest, HTTP/HTTPS capture and Arazzo export. See [support](docs/support.md), [architecture](docs/architecture.md) and [local release preparation](docs/local-release.md).
