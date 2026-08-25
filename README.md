# Phrond Recorder

Phrond captures network traffic between tests and an already-running system
under test (SUT), and between that SUT and its externally owned dependencies or
mock servers. It writes one correlated YAML artifact per test, validates both
traffic directions strictly against supplied OpenAPI schemas, and exports HTML,
Postman, Open Workflow Specification, Pact V3, and generated OpenAPI documents.

Phrond deliberately does not start or configure the SUT, and it is not a mock
server. Use the application harness and mocking tools that already fit your
stack; route test→SUT and optional SUT→dependency traffic through the same
Phrond capture proxy.

## Quick start

```bash
./examples/run-basic-rest-e2e.sh
```

The JavaScript consumer API is intentionally small:

```js
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("@phrond/sdk");

const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [{
    id: "hello-api",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "schemas/hello.openapi.yaml" }
  }],
  dependencies: {
    inventory: {
      upstreamUrl: process.env.WIREMOCK_BASE_URL,
      schema: {
        id: "inventory-api",
        type: "openapi",
        source: { type: "file", path: "schemas/inventory.openapi.yaml" }
      }
    }
  }
});

phrond.test("hello world happy flow", async () => {
  const response = await phrond.http.get("/hello");
  assert.equal(response.status, 200);
});
```

The SUT and mock server must already be running. Configure the SUT's inventory
base URL to `http://<phrond-proxy>/__phrond/dep/inventory`; Phrond forwards
those calls unchanged to `WIREMOCK_BASE_URL`. `phrond.dependencyUrl("inventory")`
returns the same capture URL once the suite has started. Dependencies are
optional; omit `dependencies` when only inbound traffic is needed. The exact
test name, pass/fail status, inbound request, downstream request, and their
causal relationship are stored in one artifact.

## Capture behavior

- HTTP reverse and forward proxy capture are supported.
- Existing mock servers and dependencies remain independently owned; declaring
  a dependency adds capture-only forwarding on the existing proxy listener.
- Downstream flows use `channel: sut_to_dependency`, include their stable
  `asset_id`, and point to the originating inbound flow with `parent_flow_id`.
- Optional `phrond.assertDependency(assetId, { method, path, response })`
  checks selected captured downstream response fields and records precise
  flow-linked assertions without managing the existing mock server.
- HTTPS capture uses a locally generated MITM CA; see
  [HTTPS trust setup](docs/https-trust.md).
- Request and response bodies stream through unchanged. Artifacts retain at
  most 4 MiB per body by default; set `PHROND_MAX_CAPTURE_BYTES=0` for unlimited
  capture.
- Upstream connection and TLS failures return and record HTTP 502 responses,
  with the original failure in `transport_error`.
- OpenAPI schemas are required and enforced by default. Teams without one can
  explicitly opt into `generateSchema` to create a standard OpenAPI 3.1 draft
  from their captured SUT tests. Each declared dependency always supplies its
  own OpenAPI schema, including when the SUT schema is being generated.

## Reports

```bash
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format openapi --output generated.openapi.yaml
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format workflow --output coverage.workflow.yaml
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format postman --output coverage.postman_collection.json
go run ./cmd/phrond-coverage --artifacts ./artifacts --consumer-name orders-service --format pact --output ./artifacts/pacts
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format html --output coverage.html
```

HTML, Postman, and Workflow formats report both test→SUT and SUT→dependency
traffic, together with provided and dependent API coverage. The HTML report
can be explored by API operation or test case and includes the captured
request/response headers, trailers, payloads, dependency identity, and parent
request. The workflow export uses Open Workflow Specification DSL 1.0.3;
Postman remains an interoperability option.

Pact export writes one deterministic Pact Specification V3 JSON file per
consumer/provider pair from successful SUT→dependency exchanges. Optional
`assertDependency` calls select the response fields that actually matter; when
none are present, the full captured dependency response is included with
OpenAPI-derived matchers. Identical interactions are deduplicated, sensitive
and capture-specific headers are omitted, and provider states can be attached
without giving Phrond ownership of the existing mock server. Publish the files
with the standard Pact Broker CLI; see [Pact export](docs/pact-export.md).

Generated OpenAPI contains inferred paths, parameters, bodies, content types,
and responses from passing tests, but never captured values as examples. Review
it before moving the file into `schemas` and using normal strict validation.

## Documentation

- [Getting started with existing tests and a mock server](docs/getting-started-existing-tests.md)
- [JavaScript SDK](sdk/js/README.md)
- [Runtime HTTP API](docs/runtime-api.md)
- [Pact export and Pact Broker publishing](docs/pact-export.md)
- [Artifact schema](docs/artifact-openapi.yaml)
- [Design](docs/testing-tool-requirements-and-design.md)
- [Roadmap](docs/roadmap-capture-fidelity.md)
