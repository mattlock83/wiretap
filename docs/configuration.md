# Configuration and commands

Check in one `.wiretap.yaml` at the test project root. Wiretap discovers it in the working directory or nearest ancestor; `--config path` selects another file. The Go runtime resolves project defaults; the Jest adapter sends explicit overrides.

```yaml
version: 1
application: {name: Orders, id: orders-app}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text, html], arazzo: true}
sut:
  id: orders
  url: ${SUT_BASE_URL}
  kind: service
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    name: Inventory Service
    url: ${INVENTORY_MOCK_URL}
    kind: mock
provider_mappings:
  inventory: {application_id: inventory-app, api_id: inventory}
```

Omit `sut.url` when Jest hosts the configured `app` handler. `schema`, `output_dir` and `ca_dir` paths resolve relative to the project file. `${VARIABLE}` resolves from the runtime environment; missing variables are errors. Unknown keys and multiple YAML documents are rejected.

`application` accepts `name`, `id` and optional `description`; at least a name or ID is required. `sut.id` identifies the provided API, dependency keys identify consumed APIs and optional dependency `name` supplies a display name. `kind` is `mock`, `service` or `unknown` and describes the target actually exercised. Stable application/API identities and `provider_mappings` connect [shared snapshots](assumption-checks.md#share-provider-snapshots).

Optional `test_source` and `sut_source` objects retain tested repository, revision, build and version metadata. These describe the tested application, independently of the Wiretap producer version.

## Start the runtime

Use the [Jest adapter](../sdk/js/docs/jest.md):

```js
const { withWiretap } = require('@wiretap/sdk/jest');
module.exports = withWiretap(existingConfig, { app: '<rootDir>/app.js' });
```

The adapter chooses available ports, starts the local executable, captures final outcomes, finalizes exports and stops its process. Put `wiretap` on `PATH` or set `WIRETAP_BINARY`. Point the shared client at `process.env.WIRETAP_SUT_URL`; the app keeps its existing downstream clients and mocks. Configure those clients to use the dependency capture addresses.

The underlying runtime can also be started explicitly for control-API work:

```sh
wiretap --config .wiretap.yaml
```

It hosts the control API and waits for lifecycle calls. It does not choose or launch a test command. Default control/capture listeners are `127.0.0.1:18000` and `127.0.0.1:18080`; `http_listen` and `proxy_listen` override them. Managed Jest uses allocated local ports and the returned addresses. A runtime has one reverse SUT target.

## Connect the framework's lifecycle

The Jest adapter starts capture once, registers each test attempt before its per-test work, records its final runner outcome after cleanup, then finalizes after all files/workers finish. One suite owner controls a shared runtime. The adapter uses one worker by default; parallel workers and `.concurrent` tests need explicit sessions and trace propagation through the SUT. See [tracing](otel-adoption.md).

Finalization completes the raw archive and configured exports. Managed shutdown closes a parent stdin lifeline and waits for the child; unsuccessful exits or forced termination fail cleanup. If the owner dies, EOF asks the runtime to preserve an interrupted archive. Abrupt termination cannot guarantee exports or a completion receipt; killing the runtime can leave an open archive. Retain the entire output root in CI.

## Schema defaults by boundary

| Boundary | Default | Optional setting |
| --- | --- | --- |
| Main HTTP API | OpenAPI required. | `schema_required: false` explicitly permits omission. |
| HTTP dependency or mock | A URL and kind suffice. | Supply `schema` to validate/measure it; `schema_required: true` enforces a spec. |

Supplied schemas always load and validate, even with `schema_required: false`. A missing or invalid supplied spec fails setup. Omitted schemas retain observed traffic while recording skipped validation and an unknown coverage denominator. Wiretap does not infer allowed enums or bounds from samples. A later check can validate consumer examples against the provider's frozen OpenAPI contract; a provider spec alone cannot replace passing provider test evidence.

Validation is independent of the framework outcome: a deliberate invalid request can fail schema validation while its Jest rejection test passes. Missing capture, setup failures and incomplete capture still surface as integration errors.

Use `schema_required` in project YAML and HTTP requests, or `schemaRequired` in Jest options. HTTP and HTTPS targets are selected by their URL; project `protocol` is no longer accepted. Project YAML takes a single `schema` path per API. Jest overrides and HTTP setup requests use `schemas` arrays of source objects; see the [Jest guide](../sdk/js/docs/jest.md#dynamic-fixtures-and-session-settings) and [runtime API](runtime-api.md#test-lifecycle).

## Capture and proxy settings

Set these on the runtime process:

| Variable | Default | Purpose |
| --- | --- | --- |
| `WIRETAP_HTTP_LISTEN` | `127.0.0.1:18000` | Control address. |
| `WIRETAP_PROXY_LISTEN` | `127.0.0.1:18080` | Capture address. |
| `WIRETAP_OUTPUT_DIR` | `artifacts` | Run parent directory. |
| `WIRETAP_MAX_CAPTURE_BYTES` | `4194304` | Retained body prefix; `0` is unlimited. Delivery still streams. |
| `WIRETAP_PROXY_MODE` | `mitm` | Decrypt HTTPS; `passthrough` cannot supply decrypted request/response evidence. |
| `WIRETAP_PROXY_CA_DIR` | System cache under `wiretap/ca` | Proxy CA directory. |

Environment paths resolve from the process working directory and override file settings. `WIRETAP_READY_FILE` publishes private JSON readiness (`control_url`, `pid`) for managed startup; its parent must exist. The runtime creates it atomically with mode `0600` and removes it on shutdown. Normal harnesses use the adapter's readiness handling.

Teardown drains accepted exchanges for five seconds by default. Configure the API's bounded per-attempt drain when needed. See [HTTPS trust](https-trust.md), [runtime API](runtime-api.md) and [archive loading budgets](archive-loading.md).

## CLI commands

| Command | Purpose |
| --- | --- |
| `wiretap [--config path]` | Host the control service. |
| `wiretap version [--json]` | Show producer identity. |
| `wiretap init --sut-url <url> --schema <path> --application <id>` | Create a minimal HTTP project without overwriting a file. |
| `wiretap doctor [--config path]` | Inspect local setup without starting capture. |
| `wiretap report --bundle <run> --format html --output report.html` | Build an offline coverage and test-run dashboard. |
| `wiretap report --bundle <run> --format text` | Read verified coverage; JSON is the default. |
| `wiretap report --coverage-json <json> --format html --output report.html` | Render exported coverage JSON with optional repeatable `--spec` and `--arazzo` inputs. |
| `wiretap report --bundle <run> --format arazzo --output-dir <dir>` | Write Arazzo workflows and portable source companions. |
| `wiretap report --bundle <run> --format all` | Generate default coverage formats and Arazzo outputs. |
| `wiretap check --consumer <run> --provider <run>` | Check captured mock assumptions with verified Arazzo inputs. |
| `wiretap run-set --bundle <a> --bundle <b> --output runs.json` | Pin completed shards from one build. |
| `wiretap report --run-set runs.json` | Calculate aggregate JSON coverage. |
| `wiretap ca` | Print the configured public proxy CA. |

Coverage formats are `json`, `text` and `html`. `report` also supports `arazzo` and `all`. Arazzo needs a destination to keep companion references portable. Standalone `--coverage-json` input is for HTML rendering; its optional specs and workflows are supplementary and do not recalculate the measurement. See [artifacts](artifacts.md) for the dashboard inputs and strict eligible-traffic limits.

Checks use Arazzo on both sides and require complete raw archives. Defaults are `exports/arazzo/arazzo.json` and its source files within each run. Explicit pairs accept `--consumer-arazzo` and `--provider-arazzo`; run sets use each member's defaults. There is no input-format selector. Use `--dependency` when a pair has multiple mock APIs. Keep reports outside input archives, selected Arazzo export trees and run-set manifests. See [assumption checks](assumption-checks.md#arazzo-check-inputs).

## Automatic outputs

`outputs: {coverage: [json, text, html], arazzo: true}` is the default. It generates coverage and Arazzo after finalization. Open the receipt's `outputs.coverage.html` in a browser for the offline dashboard. The completion receipt and `exports/result.json` name exact paths and errors. Each export is independent; one failure retains other successful outputs but fails finalization/cleanup. Raw evidence remains available for an offline retry:

```sh
wiretap report --bundle ./artifacts/wiretap/run-... --format all
```

Use [run sets](run-sets.md) for same-build shards. Compatibility checks provide text and JSON results. Process option/input errors return `2`; runtime/export failures return `1`. Test outcomes remain owned by Jest.
