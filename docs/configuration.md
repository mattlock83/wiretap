# Configuration and commands

Check in one `.wiretap.yaml` at the project root. Wiretap discovers it in the
working directory or the nearest ancestor; `--config path` selects another file.
The runtime loads it once, and SDKs inherit its defaults through the HTTP API.
Test integrations can then focus on names, outcomes, and trace context.

```yaml
version: 1
output_dir: artifacts
http_listen: 127.0.0.1:18000
proxy_listen: 127.0.0.1:18080
sut:
  id: orders
  url: ${SUT_BASE_URL}
  kind: service
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
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

The SUT `id` identifies the provided API. Dependency keys identify their APIs;
use stable IDs when comparing repositories. `kind` is `mock`, `service`, or
`unknown`. It describes the actual target used during the tests.

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

Finalization writes a complete bundle and its Markdown/HTML views automatically.
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
them, and `fail` if any enforced schema fails. `skipped` lists the omitted API
schema IDs. These exceptions never turn a validation failure into a pass.

For HTTP dependencies marked `kind: mock`, Wiretap always generates a separate OpenAPI
draft from captured requests and responses, including when a spec was supplied.
It appears in the bundle's Dependencies page and HTML dashboard. Omitted specs
still need `schema_required: false`; inferred drafts do not enforce contracts or
change coverage. See [inferred mock assumptions](okf-bundles.md#inferred-mock-assumptions).

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
| `wiretap report --bundle <path>` | Rebuild a bundle's Markdown, HTML, knowledge and coverage views offline. |
| `wiretap export --bundle <path> --format json` | Export a diagnostic coverage report to stdout; `--format yaml` and `--output <file>` are also supported. |
| `wiretap ca [--config path]` | Print the public proxy CA certificate, creating the local CA if needed. |

Normal capture already writes the bundle views. `report` is useful for copied
bundles or after a viewer update; it does not execute tests. See the
[bundle guide](okf-bundles.md#rebuild-or-export) for examples.
