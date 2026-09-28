# Project configuration and runtime

Check in one `.phrond.yaml` at the project root. Phrond discovers it in the
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

`PHROND_HTTP_LISTEN`, `PHROND_PROXY_LISTEN`, `PHROND_OUTPUT_DIR`, and
`PHROND_PROXY_CA_DIR` override file settings. Relative environment paths are
relative to the process's working directory. Existing capture, timeout, logging,
and proxy-mode environment settings still apply. Per-test SDK/API values override
project test defaults; an explicitly supplied dependency list replaces that list.

The SUT `id` identifies the provided API. Dependency keys identify their APIs;
use stable IDs when comparing repositories. `kind` is `mock`, `service`, or
`unknown`. It describes the actual target used during the tests.

## Start the runtime

Build and start Phrond alongside the SUT and mocks using your existing service
harness, container setup, or a separate terminal:

```bash
go build -o /tmp/phrond ./cmd/phrond
/tmp/phrond --config ./test/.phrond.yaml
```

Without `--config`, the runtime discovers `.phrond.yaml`. It hosts the HTTP
control API and waits for SDK/API lifecycle calls to start capture. It does not
launch test processes or choose their framework, ordering, concurrency, or exit
status. Use your usual test command separately, such as `npm test` or `go test`.
Phrond has one reverse SUT target per runtime.

Set `PHROND_BASE_URL` in the test process when the control API is not at its
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
through the SDK or API. Phrond process usage/configuration errors return `2` and
runtime failures return `1`; those are independent of the test command's status.

## Specs remain required

Both the SUT and each dependency require an OpenAPI spec by default. The SUT
can use `generate_schema: true` instead of `schema` to draft a contract from
passing tests. Export the draft after the run and review it before adoption.

For an API that genuinely has no spec, make the exception explicit on that API:

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

The API spelling is also `schema_required`; JavaScript uses `schemaRequired` and
Go uses `SchemaRequired *bool`. An attempt's validation status is `partial` when
some APIs have specs and others explicitly omit them, `skipped` when all omit
them, and `fail` if any enforced schema fails. `skipped` lists the omitted API
schema IDs. These exceptions never turn a validation failure into a pass.

## Inspect and export

```bash
phrond report --bundle artifacts/<run-id>
phrond export --bundle artifacts/<run-id> --format openapi --output api.yaml
phrond export --bundle artifacts/<run-id> --format workflow --output workflow.yaml
phrond export --bundle artifacts/<run-id> --format json --output coverage.json
phrond ca > phrond-ca.pem
```

`report` is an optional offline repair/rebuild tool for copied bundles. It
rebuilds OKF pages and the offline dashboard from verified evidence.
`export` uses the same report implementation for explicit OpenAPI, Workflow,
or diagnostic JSON/YAML output. Normal capture already writes the bundle views.
`ca` prints the public CA certificate used by that runtime configuration; it
creates the local CA if necessary. Trust setup is described in the
[HTTPS guide](https-trust.md).
