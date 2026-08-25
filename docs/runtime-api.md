# Runtime API

The SDK uses this capture-only HTTP API. External orchestration owns the SUT and
any dependency or mock-server lifecycles.

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

The response contains `sut_url`, the reverse proxy address tests should call,
and `dependency_base_url`, the reserved dependency gateway on that same proxy.
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
  }],
  "dependencies": [{
    "asset_id": "inventory",
    "asset_name": "Inventory Service",
    "upstream_url": "http://127.0.0.1:9090",
    "schema": {
      "id": "inventory-openapi",
      "type": "openapi",
      "source": {"type": "file", "path": "schemas/inventory.openapi.yaml"}
    }
  }]
}
```

The response includes `session_id`, `sut_url`, `proxy_listen`, and a
`dependencies` object mapping asset IDs to their capture URLs. Send inbound
calls to `sut_url`. Configure the SUT's existing dependency base URL to the
returned inventory URL, for example:

```text
http://127.0.0.1:19000/__phrond/dep/inventory
```

Requests to `/__phrond/dep/inventory/products/123` are forwarded to
`http://127.0.0.1:9090/products/123`. Request methods, headers, bodies, query
parameters, responses, and trailers retain the existing proxy's bounded-capture
semantics. Phrond does not start, configure, stub, or verify the upstream.
Every dependency requires a readable, valid OpenAPI schema. SDK helpers attach
`X-Phrond-Session-Id` to inbound test requests automatically.

Only one unlabeled zero-configuration session can be active. Set
`explicit_session: true` and attach the returned session ID header for parallel
tests. Explicit sessions return
`/__phrond/session/<session-id>/dep/<asset-id>` dependency URLs to prevent
cross-test attribution.

`POST /v1/assertDependency` checks selected fields on an already captured
dependency response and links the resulting assertions to that flow:

```json
{
  "session_id": "session-...",
  "asset_id": "inventory",
  "method": "GET",
  "path": "/inventory/product-123",
  "provider_states": [{
    "name": "product exists and is available",
    "params": {"productId": "product-123"}
  }],
  "response": {
    "$.productId": "product-123",
    "$.available": true
  }
}
```

`method`, `path`, and `flow_id` are optional selectors; the latest matching
captured dependency exchange is used. Response fields use JSONPath properties
and array indexes. Success returns `status`, `flow_id`, and `assertions`. A
mismatch returns HTTP 422, records the failed assertion, and marks the owning
artifact as failed even if teardown requests `status: pass`. The complete
captured response remains in the artifact.
Optional `provider_states` are attached to that flow for standard Pact provider
verification; they do not configure or change the existing upstream server.

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
  "consumer_name": "orders-service",
  "provider_names": {"inventory": "inventory-service"},
  "collection_path": "artifacts/coverage.postman_collection.json",
  "workflow_path": "artifacts/coverage.workflow.yaml",
  "html_path": "artifacts/coverage.html",
  "pact_dir": "artifacts/pacts"
}
```

When captured dependency schemas are present, Pact V3 files are generated in
`pact_dir` or the default `artifacts/pacts`. The response includes `pact_dir`,
`pact_paths`, `consumer_name`, and detected `consumer_version`/`branch` when
available. Each file contains one consumer/provider contract, deduplicated
passing downstream interactions, and assertion-selected response fields. The
normal Postman, Open Workflow, and HTML reports remain unchanged.

## Lower-level endpoints

- `POST /v1/testcases/start`, `/v1/testcases/finish`
- `GET /v1/testcases/status`, `/v1/artifact`
- `POST /v1/schemas`, `/v1/redactions`, `/v1/expectations`
- `POST /v1/proxy/start`, `/v1/proxy/mode`
- `GET /v1/proxy/ca`
- `POST /v1/lifecycle/start`, `/v1/lifecycle/stop`

There are no response queue, stub, mock lifecycle, or verification endpoints.
The dependency gateway is a reserved forwarding route on the existing capture
proxy, not a mock server or a separate control API. Mocking belongs to
consumer-selected tools outside Phrond.
