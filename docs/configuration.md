# Configuration and commands

Check in one `.wiretap.yaml` at the project root. Wiretap discovers it in the
working directory or the nearest ancestor; `--config path` selects another file.
The runtime loads it once, and SDKs inherit its defaults through the HTTP API.
Test integrations can then focus on names, outcomes, and trace context.

Use isolated services with synthetic data and dummy secrets. Traffic is retained
as sent. Redaction settings are unsupported; remove obsolete `redactions`
configuration when upgrading. Readers can still inspect older redacted archives
with their original uncertainty preserved.

```yaml
version: 1
application:
  name: Orders
  id: orders-app
output_dir: artifacts
outputs:
  coverage: [json, text]
  postman: true
http_listen: 127.0.0.1:18000
proxy_listen: 127.0.0.1:18080
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
    schema: schemas/inventory.openapi.yaml
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

Build and start Wiretap alongside the SUT and mocks using your existing service
harness, container setup, or a separate terminal:

```bash
go build -o /tmp/wiretap ./cmd/wiretap
/tmp/wiretap --config ./test/.wiretap.yaml
```

Without `--config`, the runtime discovers `.wiretap.yaml`. It hosts the HTTP
control API and waits for SDK/API lifecycle calls to start capture. It does not
launch test processes or choose their framework, ordering, concurrency, or exit
status. Use your usual test command separately, such as `npm test` or `go test`.
Wiretap has one reverse SUT target per runtime.

Set `WIRETAP_BASE_URL` in the test process when the control API is not at its
default `http://127.0.0.1:18000`. The SDK reads configuration defaults over HTTP;
it does not invoke the binary. Startup/session responses give the actual
`sut_url` and dependency capture addresses. A proxy listener ending in `:0`
allocates a free port; your harness must pass its returned addresses to the
relevant clients. Use a known control listener address so SDKs can connect.

## Connect the framework's lifecycle

The [existing-tests guide](getting-started-existing-tests.md) describes the SDK
integration. The language-independent contract is:

1. Suite setup calls `POST /v1/startRun` (JavaScript `startCapture`). Test
   setup also starts capture lazily when necessary, as Go's `StartTest` does.
2. Each test attempt calls `POST /v1/setupTest` with its name and trace context.
3. Requests in test/fixture callbacks run in that attempt's context.
4. The framework's completion hook calls `POST /v1/teardownTest` with the result.
5. After all attempts and workers finish, suite cleanup calls
   `POST /v1/finishRun` (JavaScript `finishRun`, Go `FinishRun`).

One suite-level owner must finalize a shared run. Worker-local Node integrations
use `manageRuntime: false`; the optional Jest adapter always leaves finalization
to a global teardown hook. For multiple Go packages sharing a runtime, finalize
once in the outer harness, not in each package's `TestMain`. Independently owned
runtimes can be finalized by each package or suite.

Finalization writes the completed raw run artifact.
`finishRun` stops capture and finalizes the run; the HTTP
control process stays available for another run. The harness stops that process
when it is no longer needed. Stopping it with unfinished capture preserves an
interrupted bundle and pending attempts. SDK cleanup failures should fail the
owning test or suite; the test framework controls the test process exit code.

Without a configuration file, supply schemas, addresses, and dependencies
through the SDK or API. Wiretap process usage/configuration errors return `2` and
runtime failures return `1`; those are independent of the test command's status.

## Specs remain required

HTTP SUTs and dependencies require an OpenAPI spec by default. For gRPC,
`protocol: grpc` requires a Protobuf contract. Omitting `schema` chooses server
reflection; local `.proto` sources and descriptor sets are alternatives. gRPC
does not allow `schema_required: false`. See the [gRPC guide](grpc-user-guide.md)
for listener, service selection, TLS and lifecycle examples.

For an API without a spec, make the exception explicit on that API:

```yaml
dependencies:
  inventory:
    url: ${INVENTORY_MOCK_URL}
    kind: mock
    schema_required: false
```

Traffic is still captured. Its evidence records an omitted schema, skipped
validation, and an unknown coverage denominator. Concrete observed paths and
payloads remain available; they are not inferred contract operations. Other
APIs remain strict. A supplied schema is always loaded and validated, even with
`schema_required: false`; missing files and invalid specs remain errors.

The HTTP API spelling is also `schema_required`; JavaScript uses
`schemaRequired` and Go uses `SchemaRequired *bool`. An attempt's validation
status is `partial` when some APIs have specs and others explicitly omit them, `skipped` when all omit
them, and `fail` if any supplied schema fails. `skipped` lists the omitted API
schema IDs. Validation status is independent of the framework outcome: contract
violations stay visible while a deliberate negative test can pass its assertions.
Setup and incomplete-capture failures still fail the integration.

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
| `wiretap report --bundle <path>` | Print a JSON coverage report from verified raw artifacts; `--format yaml` and `--output <file>` are supported. |
| `wiretap export --bundle <path> --format json` | Export a diagnostic coverage report to stdout; `--format yaml` and `--output <file>` are also supported. |
| `wiretap export --bundle <path> --format postman --output-dir <directory>` | Export HTTP v2.1 and gRPC v3 JSON collections for the SUT and mocked dependencies, with test folders and saved interactions. |
| `wiretap ca [--config path]` | Print the public proxy CA certificate, creating the local CA if needed. |

`report` and `export` support `json`, `yaml`, `text`, `postman` and `all`;
JSON is the default. `--format text` prints the compact coverage summary.
They work offline from complete capture directories and do not execute tests.
See [capture artifacts and exports](artifacts.md) for examples.

## Suite outputs

Select `outputs: {coverage: [json, text], postman: true}` to generate exports when
suite ownership calls `/v1/finishRun`. This is opt-in; without it capture continues
to write only the raw archive. SDK options can select outputs for that finalization.
The result returns `bundle_path`, `run_id`, and `outputs` containing exact coverage
file paths, Postman file paths and export errors. Files live under the archive's
`exports/` directory, with `exports/result.json` recording available outputs.

Coverage and Postman exports are independent: a Postman failure leaves valid
coverage and the raw failed/passing attempts available, while finalization reports
an error. Preserve that cleanup error and any original test failure. Finalize
only once in the outer harness after all workers have torn down their attempts.
`wiretap export --bundle <exact-path> --format all` generates the standard JSON
coverage, text summary and Postman outputs offline, for example to retry exports after fixing
configuration. Avoid selecting a run by timestamp or scanning for the newest file.
