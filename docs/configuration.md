# Configuration and commands

Check in one `.wiretap.yaml` at the project root. Wiretap discovers it in the
working directory or the nearest ancestor; `--config path` selects another file.
The runtime loads it once, and SDKs inherit its defaults through the HTTP API.
Test integrations can then focus on names, outcomes, and trace context.

Use isolated services with synthetic data and dummy secrets. Traffic is retained
as sent, including payloads and credentials.

```yaml
version: 1
application:
  name: Orders
  id: orders-app
output_dir: artifacts
outputs:
  coverage: [json, text]
  postman: true
  arazzo: true
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
```

`schema`, `output_dir`, and optional `ca_dir` paths are relative to the
configuration file. `${VARIABLE}` references in URLs, paths, and listener
addresses resolve from the runtime's environment; missing variables are errors.
Unknown fields and multiple YAML documents are rejected.

`WIRETAP_HTTP_LISTEN`, `WIRETAP_PROXY_LISTEN`, `WIRETAP_OUTPUT_DIR`, and
`WIRETAP_PROXY_CA_DIR` override file settings. Relative environment paths are
relative to the process's working directory. Additional capture and proxy
settings are listed below. Per-test SDK/API values override project test
defaults; an explicitly supplied dependency list replaces that list.

`WIRETAP_READY_FILE` optionally publishes the bound control address and process
ID as JSON (`control_url` and `pid`). This supports starting the control listener
on `127.0.0.1:0` without reserving a port first. Supply a path in a private,
existing directory owned by the launching harness. Wiretap atomically creates
the file with mode `0600`, fails startup if it cannot publish it, and removes it
on shutdown.

Optional `application` metadata records the SUT's human-readable `name`, stable
`id` and optional `description` in the capture bundle. For example, the
application above exports a Postman collection named `Orders - orders-app`.
At least a name or ID must be supplied when the object is present.

The SUT `id` identifies the provided API. Dependency keys identify their APIs;
use stable IDs when comparing repositories. `kind` is `mock`, `service`, or
`unknown`. It describes the actual target used during the tests.
An optional dependency `name` supplies its display name in Postman exports;
the inventory mock above exports as `Inventory Service - inventory`.

## Start the runtime

Use the [Python/pytest adapter](../sdk/python/README.md), managed
[Jest, Playwright Test or Mocha adapter](../sdk/js/README.md),
Go's [`wiretap.Run(m)`](../sdk/go/README.md), or the JavaScript
[`startManagedRuntime` helper](../sdk/js/docs/javascript.md). They start Wiretap
on available local ports, discover the configuration, finalize outputs and stop
their owned runtime. Put the local binary on `PATH` or set `WIRETAP_BINARY`.

For a runtime owned by your existing service harness or container setup, build
and start it explicitly:

```bash
go build -o dist/wiretap ./cmd/wiretap
./dist/wiretap --config .wiretap.yaml
```

Without `--config`, the runtime discovers `.wiretap.yaml`. It hosts the HTTP
control API and waits for SDK/API lifecycle calls to start capture. It does not
launch test processes or choose their framework, ordering, concurrency, or exit
status. Use your usual test command separately, such as `npm test` or `go test`.
Wiretap has one reverse SUT target per runtime.

For an externally managed runtime, set `WIRETAP_BASE_URL` in the test process
when the control API is not at its default `http://127.0.0.1:18000`. The plain
JavaScript API, Go's `Runtime` and Python's `Runtime` connect to that process and read configuration
defaults over HTTP. Startup/session responses give the actual
`sut_url` and dependency capture addresses. A proxy listener ending in `:0`
allocates a free port; your harness must pass its returned addresses to the
relevant clients. Use a known control listener address so SDKs can connect.
Optional `http_listen` and `proxy_listen` configuration fields select fixed
listeners; defaults are `127.0.0.1:18000` and `127.0.0.1:18080`. Managed SDKs
override these with available local ports, so use their returned addresses.

## Connect the framework's lifecycle

The [existing-tests guide](getting-started-existing-tests.md) describes the SDK
integration. The language-independent contract is:

1. Suite setup calls `POST /v1/startRun` (JavaScript `startCapture`). Test
   setup also starts capture lazily when necessary, as Go's `Runtime.StartTest` does.
2. Each test attempt calls `POST /v1/setupTest` with its name and trace context.
3. Requests in test/fixture callbacks run in that attempt's context.
4. The framework's completion hook calls `POST /v1/teardownTest` with the result.
5. After all attempts and workers finish, suite cleanup calls
   `POST /v1/finishRun` (JavaScript `finishRun`, Go `FinishRun`).

One suite-level owner must finalize a shared run. Worker-local Node integrations
use `manageRuntime: false`; the Jest adapter leaves finalization to a global
teardown hook. Go's `Run` owns an independent runtime per test package, finalizes
after all test cleanup, and writes `last-run.json` and `wiretap.log` inside that
package's unique archive. Packages can run concurrently against one configured
output directory. Use `go test -count=1 ./...` for fresh captures. An advanced Go
setup sharing one external runtime across packages must finalize once in its
outer harness after every package finishes.

Finalization writes the completed raw run artifact.
`finishRun` stops capture and finalizes the run; the HTTP
control process stays available for another run. The harness stops that process
when it is no longer needed. Stopping it with unfinished capture preserves an
interrupted bundle and pending attempts. SDK cleanup failures should fail the
owning test or suite; the test framework controls the test process exit code.
Managed Go, JavaScript and Python SDKs keep an open stdin pipe as a parent lifeline.
They set the internal `WIRETAP_EXIT_ON_STDIN_EOF=1` switch so EOF requests graceful
shutdown when the owning test process crashes, times out or is killed. This
preserves an interrupted raw archive and pending attempts when shutdown can
complete. Normal CLI invocations leave the switch unset. SDK shutdown has a
bounded wait and force-kills an unresponsive child.

Abrupt owner death cannot run normal suite finalization or save the owner's
completion receipt. Exports may be incomplete, and `last-run.json` may be absent
or left from an earlier run. Killing Wiretap itself before graceful shutdown
finishes can leave the archive open rather than interrupted.

Without a configuration file, supply schemas, addresses, and dependencies
through the SDK or API. Wiretap process usage/configuration errors return `2` and
runtime failures return `1`; those are independent of the test command's status.

## Schema defaults by boundary

| Boundary | Without a schema setting | Opt in or enforce a schema |
| --- | --- | --- |
| Main HTTP SUT | Setup requires OpenAPI. | Supply `sut.schema`; the existing explicit `schema_required: false` exception remains available. |
| Consumed HTTP API, including mocks | Capture works without a spec. | Supply the dependency's `schema` for validation and coverage; `schema_required: true` makes omission an error. |
| Native gRPC SUT or dependency | Acquire descriptors through reflection. | Select local `.proto` files or a descriptor set when needed; descriptors remain required for decoding. |

The main HTTP API under test (`sut`) requires an OpenAPI spec by default. Its
existing explicit `schema_required: false` opt-out remains available when that
spec is intentionally absent. Consumed HTTP dependencies, whether mocks,
services or unknown targets, do not require a spec by default:

```yaml
sut:
  id: orders
  url: ${SUT_BASE_URL}
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    url: ${INVENTORY_MOCK_URL}
    kind: mock
```

Add `schema: schemas/inventory.openapi.yaml` to a dependency to opt into its
validation and contract coverage. Set dependency `schema_required: true` when
setup must fail if no spec is available. No `schema_required: false` flag is
needed for ordinary schema-free dependency capture. A supplied schema is always
loaded and validated, including when `schema_required: false`; missing files
and invalid supplied specs remain errors.

Without a dependency spec, traffic and Postman examples are still captured.
The consumer archive records an omitted schema, skipped validation and an
unknown contract-coverage denominator. A later assumption check can validate
those examples against the provider's own frozen OpenAPI contract, but it does
not retroactively create consumer contract coverage. It still needs a matching
complete passing provider test; a provider spec alone is insufficient. See
[assumption checks](assumption-checks.md) and the
[shared snapshot workflow](assumption-checks.md#share-provider-snapshots).
Wiretap does not infer an OpenAPI file or allowed enum/length rules from a
schema-free dependency capture. Supply a consumer dependency spec when you
want those independent declarations compared with the provider's rules.

For gRPC, `protocol: grpc` always requires a Protobuf descriptor. Omitting
`schema` chooses server reflection, so a local file is optional when reflection
is available; local `.proto` sources and descriptor sets are alternatives.
gRPC does not allow `schema_required: false`. See the [gRPC
guide](grpc-user-guide.md) for listener, service selection, TLS and lifecycle
examples.

The HTTP API spelling is `schema_required`; JavaScript uses `schemaRequired`
and Go uses `SchemaRequired *bool`. Omission delegates to the boundary defaults
above. An attempt's validation status is `partial` when some APIs have specs and
others omit them, `skipped` when all omit them, and `fail` if any supplied schema
fails. `skipped` lists omitted API schema IDs. Validation is independent of the
framework outcome: contract violations stay visible while a deliberate negative
test can pass its assertions. Setup and incomplete-capture failures still fail
the integration.

## Capture and proxy settings

Set these on the Wiretap process:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `WIRETAP_MAX_CAPTURE_BYTES` | `4194304` (4 MiB) | Retained bytes per request or response body; `0` retains the full body. Delivery still streams through the proxy. |
| `WIRETAP_PROXY_MODE` | `mitm` | Decrypt HTTPS with the proxy CA; `passthrough` retains connection metadata only. |
| `WIRETAP_PROXY_CA_DIR` | System cache under `wiretap/ca` | Directory containing the proxy CA. |

Test teardown drains accepted requests for five seconds by default. The SDK/API
can override that per attempt. See [tracing and completion](otel-adoption.md#evidence-and-completion)
and [HTTPS trust](https-trust.md).

## CLI commands

The `wiretap` binary provides these commands. Capture lifecycle is controlled
through the [HTTP API](runtime-api.md), usually via an SDK.

| Command | Purpose |
| --- | --- |
| `wiretap [--config path]` | Start the HTTP control service; wait for capture lifecycle calls. |
| `wiretap version [--json]` | Show producer version and source commit; `--version` is an alias. |
| `wiretap init --sut-url <url> --schema <path> --application <id>` | Create a minimal HTTP project configuration without overwriting an existing file; see [setup checks](setup.md). |
| `wiretap doctor [--config path]` | Check local project configuration, contracts and output paths without starting capture. |
| `wiretap report --bundle <path>` | Print a JSON coverage report from verified raw artifacts; `--format yaml` and `--output <file>` are supported. |
| `wiretap report --bundle <path> --policy <file>` | Apply opt-in per-API coverage gates while writing the ordinary JSON, YAML or text report. |
| `wiretap run-set --bundle <run-a> --bundle <run-b> --output <file>` | Pin explicitly selected archives from the same build; see [run sets](run-sets.md). |
| `wiretap report --run-set <file>` | Write a JSON aggregate retaining each member's report and combined condition evidence. |
| `wiretap compare --baseline <report> --current <report>` | Compare two single-archive JSON coverage reports; see [coverage policies and comparison](report-policies.md). |
| `wiretap export --bundle <path> --format json` | Export a diagnostic coverage report to stdout; `--format yaml` and `--output <file>` are also supported. |
| `wiretap export --bundle <path> --format postman --output-dir <directory>` | Export HTTP v2.1 and gRPC v3 JSON collections for the SUT and mocked dependencies, with test folders and saved interactions. |
| `wiretap export --bundle <path> --format arazzo --output-dir <directory>` | Export JSON/REST workflows for the SUT and mocked dependencies, with OpenAPI source companions. |
| `wiretap check consumer --consumer <run> --artifacts <root>` | Find mapped providers beneath shared application folders and check all local mock dependencies. |
| `wiretap check provider --provider <run> --artifacts <root>` | Find mapped consumers beneath shared application folders and check that local provider tests support their mocks. |
| `wiretap check <consumer\|provider> --consumer <run> --provider <run>` | Compare one explicitly selected consumer/provider pair. |
| `wiretap check <consumer\|provider> --consumer <run> --provider <run> --input-format arazzo` | Check JSON/REST assumptions using verified Arazzo workflows and companions from both archives. |
| `wiretap check <consumer\|provider> --consumer-set <file> --provider-set <file>` | Check consumer/provider evidence across explicit same-build run sets. |
| `wiretap ca [--config path]` | Print the public proxy CA certificate, creating the local CA if needed. |

For a single archive without a policy, `report` and `export` support `json`,
`yaml`, `text`, `postman`, `arazzo` and `all`; JSON is the default. `--format text` prints
the compact coverage summary.
They work offline from complete capture directories and do not execute tests.
See [capture artifacts and exports](artifacts.md) for examples.

`check` accepts run directories or `bundle.json` paths. With `--artifacts`,
supply the local run (`--consumer` or `--provider`) and an extracted local store
whose top-level folders are application IDs. Declared provider mappings select
relationships; exactly one snapshot must resolve for each selected
application/API. Missing or ambiguous peers fail instead of selecting a latest
build. The CLI does not upload or download artifacts. See
[shared snapshot selection](assumption-checks.md#share-provider-snapshots).

`--dependency <id>` scopes consumer discovery to a consumer-local mock ID. In
explicit pair mode it is required only when the dependency cannot be inferred
unambiguously. Provider discovery checks all mapped consumers and rejects
`--dependency`; use an explicit pair to scope that viewpoint. `--input-format`
selects the projection format for both sides: `postman` (default) or `arazzo`.
Collections default to `exports/postman/`; Arazzo defaults to
`exports/arazzo/arazzo.json` and its source companions. Only explicit pair mode
permits repeatable `--consumer-collection <file-or-dir>` and
`--provider-collection <file-or-dir>` overrides, or
`--consumer-arazzo <file-or-dir>` and `--provider-arazzo <file-or-dir>` with
`--input-format arazzo`. Mixing the two formats is rejected. Shared-store and
run-set checks use each run's default location for the selected format.

Arazzo checking supports eligible JSON/REST boundaries and still requires the
complete archives for response evidence, test provenance and contracts. The
workflow and source companions must match their regenerated archive projection.
Selected unsupported traffic cannot be omitted to obtain a passing check.
Verification does not execute Arazzo expressions or workflows. Arazzo generation
is strict across the whole run; incomplete or unrepresentable eligible JSON
traffic on any boundary can block export and verification. See
[Arazzo input limits](assumption-checks.md#use-arazzo-instead-of-postman-for-jsonrest).

`--ignore-header <name>` is repeatable and applies to every selected comparison.
`--format text|json|markdown|junit` and `--output <file>` control the result. JSON
retains complete diagnostics; Markdown and JUnit XML are bounded presentations
for reviews and CI. Exit statuses are
`0` for agreement, `1` for incompatible/uncovered/unverified assumptions, and
`2` for invalid inputs. The output's parent must exist. Keep output outside the
artifact root, all input archives and Postman or Arazzo input directories, and
do not replace a projection or source companion file. When selecting an Arazzo
file explicitly, keep output outside its containing export tree so its companions
remain protected even if an early unverified verdict skips input verification.
See [assumption checks](assumption-checks.md) for comparison rules and limits.
That guide separates [issues the check finds](assumption-checks.md#issues-the-check-will-find)
from [claims it cannot establish](assumption-checks.md#what-the-check-will-not-establish)
and explains [result interpretation](assumption-checks.md#interpreting-results).
An equal coverage condition is insufficient: the checker requires the same
concrete request and a compatible response from one provider exchange. With only
the provider's frozen OpenAPI 3.0 contract, it validates both exchanges under
that schema and marks the match `openapi_3_0_provider_contract`. With both
contracts, it also compares exercised rules and validates each exchange under
its own schema. Both omitted HTTP schemas allow an example-only match. A
consumer contract without a provider contract, or unsupported required checks,
is unverified.
Native gRPC also compares the selected method's reachable message/enum
declarations after observed wire cross-decoding. Custom validation options are
compared as opaque declarations and are not executed.

## Suite outputs

Select `outputs: {coverage: [json, text], postman: true, arazzo: true}` to generate exports when
suite ownership calls `/v1/finishRun`. This is opt-in; without it capture continues
to write only the raw archive. SDK options can select outputs for that finalization.
For JSON/REST projects, `outputs: {coverage: [json, text], arazzo: true}`
generates Arazzo without Postman and supports `check --input-format arazzo`.
The result returns `bundle_path`, `run_id`, and `outputs` containing exact coverage
file paths, Postman file paths, an eligible Arazzo document and its source files,
and export errors. Files live under the archive's
`exports/` directory, with `exports/result.json` recording available outputs.

Coverage, Postman and Arazzo exports are independent: an exporter failure leaves valid
coverage and the raw failed/passing attempts available, while finalization reports
an error. Preserve that cleanup error and any original test failure. Finalize
only once in the outer harness after all workers have torn down their attempts.
`wiretap export --bundle <exact-path> --format all` generates the standard JSON
coverage, text summary, Postman and eligible Arazzo outputs offline, for example to retry exports after fixing
configuration. Avoid selecting a run by timestamp or scanning for the newest file.
