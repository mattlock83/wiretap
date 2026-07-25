# Phrond Recorder

Phrond is a test harness that captures SUT traffic and mock traffic and writes a per-test YAML artifact. It supports HTTP/HTTPS with a mock server and a capture proxy, with schema validation against OpenAPI.

Design goals:
- **Consumer-owned application lifecycle**: Phrond never starts, stops, waits
  for, or configures the SUT. Existing orchestration owns the application;
  Phrond receives its base URL and owns only capture, mocks, correlation, and
  artifacts.
- **No workflow changes**: declare each dependency once with a stable `asset_id`; inject its stable mock URL into the SUT at startup; call the SUT through the reverse-capture proxy with any HTTP client. Zero-config session correlation means no per-test rewiring (parallel suites opt into explicit sessions).
- **Bring your own mock**: select `mock.mode: external` per dependency to keep
  WireMock or another HTTP mock server while Phrond remains the capture and
  correlation gateway. Built-in and external dependencies can coexist.
- **Bounded capture**: request and response bodies stream through unchanged
  while artifacts retain at most 4 MiB per body by default. Truncated bodies
  carry their complete observed size. Set `PHROND_MAX_CAPTURE_BYTES=0` for
  unlimited capture.
- **Broker-ready artifacts**: flows carry dependency `asset_id` (ground truth
  attribution), `parent_flow_id` (causality), and shape `fingerprint`s for
  dedup. Built-in mocks also retain `consumed_fields` and the full
  `mock_stub`; external mocks retain their observed source and transport
  evidence without inventing provider-specific match rules.

## Quick Start

```bash
# Basic REST example
./examples/run-basic-rest-e2e.sh

# Complex REST example
./examples/run-complex-rest-e2e.sh
```

Example tests now live under:
- `examples/basic-rest`
- `examples/complex-rest`

Demo application binaries for the examples live under:
- `examples/demo-apps`

Artifacts are written to `./artifacts` by default.

## Performance Mocking

Use normal runtime sessions with `record_traffic: false` when you want mock behavior without writing traffic artifacts:

```json
{
  "name": "payment-load-test",
  "record_traffic": false
}
```

## Bring Your Own Mock Server

Declare an external mock per dependency:

```json
{
  "asset_id": "inventory-service",
  "asset_name": "Inventory",
  "mock": {
    "mode": "external",
    "upstream_url": "http://127.0.0.1:18089"
  }
}
```

Continue injecting Phrond's returned `dependency_url` into the SUT. Phrond
strips its routing prefix, forwards the exchange to the external mock, returns
the response unchanged, and records the flow with `mock_mode: external`.
Phrond does not start, configure, reset, verify, or stop the external server.
See [Bring Your Own Mock Server](docs/byo-mock-server.md).

## Schema Validation (Optional)

Schemas are optional: a test case without any records `schema_validation.status: "skipped"` in its artifact, so teams can adopt phrond before they have OpenAPI specs. When schemas are attached, validation runs at test finish and is stored under `artifact.schema_validation` and returned in the finish response as `schema_validation`.

OpenAPI example (inline):

```json
{
  "name": "basic",
  "schemas": [
    {
      "id": "sut-openapi",
      "role": "sut",
      "type": "openapi",
      "source": {
        "type": "inline",
        "content": "openapi: 3.0.0\ninfo:\n  title: SUT\n  version: 1.0.0\npaths:\n  /process:\n    post:\n      responses:\n        \"200\": { \"description\": \"ok\" }\n"
      }
    }
  ]
}
```

## JS SDK

The `createNodeTestSuite` helper hides Phrond lifecycle/session ceremony while
preserving the test runner's name in `artifact.testcase.name`. The SUT must
already be running and configured by the caller's normal application harness.
See `sdk/js/README.md` for the basic consumer example and lower-level APIs.

## Coverage Report CLI

Generate a spec coverage report from the artifacts:

```bash
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format workflow --output coverage.workflow.yaml
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format postman --output coverage.postman_collection.json
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format html --output coverage.html
```

The workflow export uses Open Workflow Specification (formerly Serverless
Workflow) DSL 1.0.3. It includes schema-matched and unmatched captured HTTP
traffic, preserves test/mock grouping and correlation details, and stores
captured response examples under namespaced `metadata.phrond` fields. Postman
remains available for collection-based tooling.

The report includes:
- Provided API specs: exercised + not covered paths
- Dependent API specs: faked paths + asserted fields

## Docs

- Requirements and design: `docs/testing-tool-requirements-and-design.md`
- Artifact OpenAPI schema: `docs/artifact-openapi.yaml`
- HTTPS trust: `docs/https-trust.md`
- Runtime HTTP API: `docs/runtime-api.md`
- Product roadmap (capture fidelity, BYO mock gateway, Open Workflow export):
  `docs/roadmap-capture-fidelity.md`

## Mock responses

Mock responses are literal: `response.body` (an exact string) or `response.body_file`
(a file read verbatim from disk), plus `status`, `headers`, `delay_ms`, and fault
injection. There is no template/rendering engine — tests compute the exact bytes
they expect, which keeps recorded artifacts deterministic and diffable. For dynamic
or stateful behavior, queue the concrete responses your test needs (the response
stack is consumed in order).

## Persistent mock server

For a long-lived mock against a running dev environment, see
`examples/persistent-mock` — a single-file server that registers static default
routes using the built-in matcher and (optionally) forwards unmatched requests to
a real upstream.
