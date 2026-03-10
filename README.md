# Phrond Recorder

Phrond is a test harness that captures SUT traffic and mock traffic and writes a per-test YAML artifact. It supports HTTP/HTTPS with a mock server and a capture proxy, with schema validation against OpenAPI.

## Quick Start

```bash
# Basic REST example
./examples/run-basic-rest-e2e.sh

# Complex REST example
./examples/run-complex-rest-e2e.sh

# Standalone Petstore-style mock API example
./examples/run-standalone-petstore.sh
```

Example tests now live under:
- `examples/basic-rest`
- `examples/complex-rest`

Demo application binaries for the examples live under:
- `examples/demo-apps`

Artifacts are written to `./artifacts` by default.

## Standalone Mode (No Test Sessions)

Run mock-only from template files (no `setupTest` / no artifact recording):

```bash
./phrond --standalone --standalone-templates "examples/standalone/default.json,examples/standalone/scenario.json"
```

Use header `X-Phrond-Scenario` (or override with `--standalone-scenario-header`) to select scenario-specific templates.

## Schema Validation (Required)

Every test case must include at least one schema. Validation runs at test finish and is stored under `artifact.schema_validation` and returned in the finish response as `schema_validation`.

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

See `sdk/js/README.md` for SDK API details.

## Coverage Report CLI

Generate a spec coverage report from the artifacts:

```bash
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format postman --output coverage.postman_collection.json
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format html --output coverage.html
```

The report includes:
- Provided API specs: exercised + not covered paths
- Dependent API specs: faked paths + asserted fields

## Docs

- Requirements and design: `docs/testing-tool-requirements-and-design.md`
- Artifact OpenAPI schema: `docs/artifact-openapi.yaml`
- HTTPS trust: `docs/https-trust.md`
- Runtime HTTP API: `docs/runtime-api.md`
- Mock template format: `docs/mock-template-format.md`
