# Runtime API

The SDK uses this capture-only HTTP API. External orchestration owns the SUT.

## Suite lifecycle

`POST /v1/startServer` starts the capture proxy:

```json
{
  "proxy": {
    "listen_addr": "127.0.0.1:19000",
    "target_url": "http://127.0.0.1:18090"
  }
}
```

The response contains `sut_url`, the reverse proxy address tests should call.
`target_url` is only a routing destination; Phrond never starts, configures,
waits for, or stops that application.

`POST /v1/killServer` clears active capture sessions and stops the proxy.

## Test lifecycle

`POST /v1/setupTest` creates a named capture session. It also starts the proxy
if needed, so lower-level consumers do not have to call `startServer` first.
An OpenAPI schema is required and checked for readability and validity before
the session starts.

```json
{
  "name": "hello world happy flow",
  "tags": ["smoke"],
  "meta": {"suite": "hello"},
  "sut_base_url": "http://127.0.0.1:18090",
  "explicit_session": false,
  "schemas": [{
    "id": "hello-openapi",
    "role": "sut",
    "type": "openapi",
    "source": {"type": "file", "path": "schemas/hello.openapi.yaml"}
  }]
}
```

The response includes `session_id`, `sut_url`, and `proxy_listen`. Send calls
to `sut_url`. SDK helpers attach `X-Phrond-Session-Id` automatically.

Only one unlabeled zero-configuration session can be active. Set
`explicit_session: true` and attach the returned session ID header for parallel
tests.

`POST /v1/teardownTest` finalizes and writes the artifact:

```json
{"session_id": "session-...", "status": "pass"}
```

The response contains `artifact`, `path`, and `schema_validation`. OpenAPI
violations change an otherwise passing artifact to `fail`.

## Schema generation

Teams without an OpenAPI document must explicitly mark each bootstrap session:

```json
{
  "name": "hello world happy flow",
  "sut_base_url": "http://127.0.0.1:18090",
  "generate_schema": true
}
```

After the final test, `POST /v1/generateSchema` combines the captured artifacts
into a deterministic standard OpenAPI 3.1 document:

```json
{
  "artifacts_dir": "artifacts",
  "output_path": "artifacts/hello.generated.openapi.yaml",
  "title": "Hello API",
  "version": "1.0.0",
  "force": false
}
```

Passing tests are used for type and shape inference; failing tests are excluded,
and captured values are never emitted as examples. Numeric and UUID path
segments are conservatively converted to path parameters. Generated object
schemas reject unobserved properties and require properties seen in every
relevant capture. Existing files are not overwritten unless `force` is true.

Supplying both `schemas` and `generate_schema` is rejected. Supplying neither
is also rejected; generation is the explicit onboarding exception to strict
schema enforcement.

## Reports

`POST /v1/report` reads an artifact directory and writes all report formats:

```json
{
  "artifacts_dir": "artifacts",
  "sut_name": "Hello SUT",
  "collection_path": "artifacts/coverage.postman_collection.json",
  "workflow_path": "artifacts/coverage.workflow.yaml",
  "html_path": "artifacts/coverage.html"
}
```

## Lower-level endpoints

- `POST /v1/testcases/start`, `/v1/testcases/finish`
- `GET /v1/testcases/status`, `/v1/artifact`
- `POST /v1/schemas`, `/v1/redactions`, `/v1/expectations`
- `POST /v1/proxy/start`, `/v1/proxy/mode`
- `GET /v1/proxy/ca`
- `POST /v1/lifecycle/start`, `/v1/lifecycle/stop`

There are no response queue, stub, dependency gateway, or verification
endpoints. Mocking belongs to consumer-selected tools outside Phrond.
