# Phrond Recorder

Phrond is a test harness that captures SUT traffic and mock traffic and writes a per-test YAML artifact. It supports HTTP/HTTPS with a mock server and a capture proxy, with schema validation against OpenAPI.

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

Generate a spec coverage report from the artifacts (YAML/JSON):

```bash
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format yaml --output coverage.yaml
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
