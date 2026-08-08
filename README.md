# Phrond Recorder

Phrond captures network traffic between tests and an already-running system
under test (SUT). It writes one YAML artifact per test, validates captured HTTP
against optional OpenAPI schemas, and exports HTML, Postman, and Open Workflow
Specification reports.

Phrond deliberately does not start or configure the SUT, and it is not a mock
server. Use the application harness and mocking tools that already fit your
stack; route only the test→SUT traffic you want recorded through Phrond.

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
  }]
});

phrond.test("hello world happy flow", async () => {
  const response = await phrond.http.get("/hello");
  assert.equal(response.status, 200);
});
```

The SUT must already be running. `sutBaseUrl` is only the reverse proxy target.
The exact test name and truthful pass/fail status are stored in the artifact.

## Capture behavior

- HTTP reverse and forward proxy capture are supported.
- HTTPS capture uses a locally generated MITM CA; see
  [HTTPS trust setup](docs/https-trust.md).
- Request and response bodies stream through unchanged. Artifacts retain at
  most 4 MiB per body by default; set `PHROND_MAX_CAPTURE_BYTES=0` for unlimited
  capture.
- Upstream connection and TLS failures return and record HTTP 502 responses,
  with the original failure in `transport_error`.
- Schemas are optional. Without one, validation is recorded as `skipped`.

## Reports

```bash
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format workflow --output coverage.workflow.yaml
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format postman --output coverage.postman_collection.json
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format html --output coverage.html
```

All formats report captured test→SUT traffic and provided-API coverage. The
HTML report can be explored by SUT API operation or test case and includes the
captured request/response headers, trailers, and payloads. The workflow export
uses Open Workflow Specification DSL 1.0.3; Postman remains an interoperability
option.

## Documentation

- [Getting started with existing tests and a mock server](docs/getting-started-existing-tests.md)
- [JavaScript SDK](sdk/js/README.md)
- [Runtime HTTP API](docs/runtime-api.md)
- [Artifact schema](docs/artifact-openapi.yaml)
- [Design](docs/testing-tool-requirements-and-design.md)
- [Roadmap](docs/roadmap-capture-fidelity.md)
