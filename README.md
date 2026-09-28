# Phrond Recorder

Phrond captures network traffic between tests and an already-running system
under test (SUT), and between that SUT and its externally owned dependencies or
mock servers. It writes one portable OKF bundle per run, with versioned JSON
evidence and readable API, dependency, test, and coverage pages. It validates
both traffic directions against supplied OpenAPI schemas. HTML,
Open Workflow Specification, and generated OpenAPI remain explicit exports.

Phrond deliberately does not start or configure the SUT, and it is not a mock
server. Use the application harness and mocking tools that already fit your
stack; route test→SUT and optional SUT→dependency traffic through the same
Phrond capture proxy.

## Quick start

```bash
npm ci --prefix sdk/js
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
      upstreamKind: "mock",
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
test trace and inferred request relationships are stored in the run bundle.

Phrond uses W3C `traceparent`/`tracestate`, with a fresh root per test attempt.
Ordinary HTTP clients work with standard test-side instrumentation; shared-SUT
parallel capture requires the SUT to propagate context. See the
[adoption guide](docs/otel-adoption.md) for Node, Jest, Go, and serial fallback.

## Capture behavior

- HTTP reverse and forward proxy capture are supported.
- Existing mock servers and dependencies remain independently owned; declaring
  a dependency adds capture-only forwarding on the existing proxy listener.
- Downstream flows use `channel: sut_to_dependency`, include their stable
  `asset_id`, and point to the originating inbound flow with `parent_flow_id`.
- HTTPS capture uses a locally generated MITM CA; see
  [HTTPS trust setup](docs/https-trust.md).
- Request and response bodies stream through unchanged. The bundle retains at
  most 4 MiB per body by default; set `PHROND_MAX_CAPTURE_BYTES=0` for unlimited
  capture.
- Upstream connection and TLS failures return and record HTTP 502 responses,
  with the original failure in `transport_error`.
- OpenAPI schemas are required and enforced by default. Teams without one can
  explicitly opt into `generateSchema` to create a standard OpenAPI 3.1 draft
  from their captured SUT tests. Each declared dependency always supplies its
  own OpenAPI schema, including when the SUT schema is being generated.

## Bundle and optional exports

```bash
go run ./cmd/phrond-coverage --bundle ./artifacts/<run-id>
go run ./cmd/phrond-coverage --bundle ./artifacts/<run-id> --format openapi --output generated.openapi.yaml
```

The runtime creates `./artifacts/<run-id>/viewer/index.html` and `index.md`
automatically; set `PHROND_OUTPUT_DIR` to choose the parent directory. Open the
HTML dashboard directly in a browser, or use the OKF pages. The first command
rebuilds both views from JSON evidence and prints the OKF entry path. Standalone
HTML and Workflow remain available with explicit `--format html` and
`--format workflow` exports.
The [bundle guide](docs/okf-bundles.md) explains coverage and dependency
assumptions.

Generated OpenAPI contains inferred paths, parameters, bodies, content types,
and responses from passing tests, but never captured values as examples. Review
it before moving the file into `schemas` and using normal strict validation.

## Documentation

- [Capture bundles and reports](docs/okf-bundles.md)
- [OKF bundle design and implementation notes](docs/okf-bundle-implementation-plan.md)
- [Standard test tracing: design decision](docs/otel-test-tracing.md)
- [Test tracing: user adoption notes](docs/otel-adoption.md)
- [Standard test tracing: implementation handoff](docs/otel-implementation-plan.md)
- [Getting started with existing tests and a mock server](docs/getting-started-existing-tests.md)
- [JavaScript SDK](sdk/js/README.md)
- [Runtime HTTP API](docs/runtime-api.md)
- [Bundle evidence schema](docs/bundle-evidence.schema.json)
- [Legacy artifact schema](docs/artifact-openapi.yaml)
- [Design](docs/testing-tool-requirements-and-design.md)
- [Roadmap](docs/roadmap-capture-fidelity.md)
