# Add Wiretap to existing Jest tests

Wiretap records the HTTP traffic your Jest tests already send. It retains test
outcomes and portable evidence, measures OpenAPI coverage, exports eligible
JSON/REST requests as Arazzo workflows, and checks captured mocks against
provider test evidence. Keep your existing assertions, HTTP clients and mocks.

This experimental branch supports Jest on Node.js. The runtime is written in
Go. Use isolated tests with synthetic data and dummy credentials; recordings
retain the traffic you send unchanged.

## Try the external Jest example

With Git, Go 1.26.10+ and Node.js 22+ installed, run from this checkout:

```sh
node examples/existing-jest/run.mjs artifacts/existing-jest
```

The script clones a pinned external Express/Jest suite, runs its unchanged tests,
builds Wiretap, installs the local SDK and reruns the suite with capture enabled.
It verifies that all original test outcomes remain intact and checks the archive,
coverage and Arazzo export. The example intentionally contains existing failing
tests; the harness succeeds when capture preserves those results.

See the [example guide](../examples/existing-jest/README.md) for its output
layout, exact integration changes and retained evidence.

## Install the runtime and Jest SDK

Build and pack from the Wiretap checkout:

```sh
go build -o dist/wiretap ./cmd/wiretap
npm pack ./sdk/js --pack-destination ./dist
```

Install the generated SDK tarball in your test project alongside Jest. Put the
Wiretap executable on `PATH`, or set `WIRETAP_BINARY` to its absolute path.
The SDK requires Node.js 22+ and supports Jest 29.7 and 30 with Jest Circus and
the Node test environment. See [local installation](install-local-release.md)
for platform-specific binary and package instructions.

## Configure the API and outputs

Add `.wiretap.yaml` to your test project:

```yaml
version: 1
application: {id: orders-service, name: Orders}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text, html], arazzo: true}
sut:
  id: orders-http
  kind: service
  schema: openapi.yaml
```

The main API requires an OpenAPI contract by default. Set `schema_required: false`
explicitly to capture it without a contract. Supplied contracts always validate.
Dependencies need their URL and kind; a dependency schema is optional:

```yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
```

A schema-free dependency still contributes captured examples and eligible Arazzo
workflows; its contract coverage remains unknown. Point the application's
inventory client at the dependency capture URL so the requests cross Wiretap.
See [configuration](configuration.md) and [the Jest guide](../sdk/js/docs/jest.md)
for dependency URLs and test context propagation.

## Wrap the Jest configuration

Keep your settings and add `withWiretap`:

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');
module.exports = withWiretap(existingConfig, {
  app: '<rootDir>/app.js',
});
```

For Supertest, change the shared API client from `supertest(app)` to:

```js
const api = supertest(process.env.WIRETAP_SUT_URL || app);
```

The SDK loads the application inside Jest after its mocks are registered, starts
Wiretap, records each test attempt, waits for user cleanup, finalizes exports and
stops the owned process. If your harness already starts the API, set `sut.url`
and omit `app`. Dynamic fixtures can use `captureJest({ app })` in a Jest setup
file. Keep one integration owner; do not combine automatic and manual capture
for the same test.

Jest runs serially by default. Parallel capture requires explicit sessions and
working W3C trace propagation through the application and dependencies. Follow
the [Jest guide](../sdk/js/docs/jest.md) before enabling parallel workers.
In-process mock function calls do not cross the HTTP proxy and are not captured.

## Retain and inspect the evidence

The SDK prints the exact archive and export paths after finalization. Keep the
complete run directory, including failed test attempts:

| Path | Contents |
| --- | --- |
| `bundle.json` | Run identity, API inventory and test references. |
| `tests/` | Immutable recordings and outcomes for each attempt. |
| `contracts/` | Frozen OpenAPI contracts and their imports. |
| `payloads/` | Retained request and response bytes. |
| `exports/coverage.json` and `coverage.txt` | API and contract-condition coverage. |
| `exports/coverage.html` | Offline API coverage and test-run dashboard. |
| `exports/arazzo/` | Eligible JSON/REST workflows and OpenAPI source companions. |
| `last-run.json` and `wiretap.log` | This run's receipt and bounded runtime log. |

Read a compact report or regenerate the standard exports offline:

```sh
wiretap report --bundle ./artifacts/wiretap/<run-id> --format text
wiretap report --bundle ./artifacts/wiretap/<run-id> --format html --output report.html
wiretap report --bundle ./artifacts/wiretap/<run-id> --format all
```

Open `exports/coverage.html` directly in a browser. To rebuild it from coverage
JSON with optional specs and workflows, see the [offline HTML dashboard](artifacts.md#offline-html-dashboard).

Arazzo excludes non-JSON HTTP traffic, and incomplete eligible captures can fail
export. Raw HTTP evidence and coverage remain available. Keep the Arazzo document
and all its companions together. See [artifacts and exports](artifacts.md) for
eligibility, capture limits and strict export behavior.

## Check captured mocks against provider tests

Retain complete consumer and provider runs, including their Arazzo exports:

```sh
wiretap check --consumer ./orders-run --provider ./inventory-run \
  --dependency inventory
```

Every selected captured mock request and response needs supporting evidence from
one complete passing provider test exchange. The checker also validates supported
frozen OpenAPI rules where available and verifies the Arazzo inputs against the
archives. It does not replay traffic or execute workflows.

Use [provider mappings and shared snapshots](assumption-checks.md#share-provider-snapshots)
for checks across teams, and [the CI guide](ci-example.md) for artifact retention
and gates. A passing check covers observed examples; it does not prove every API
scenario, workflow or deployment is compatible.
