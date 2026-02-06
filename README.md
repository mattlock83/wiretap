# Phrond Recorder

Phrond is a test harness that captures SUT traffic and mock traffic and writes a per-test YAML artifact. It supports HTTP/HTTPS and gRPC, a mock server, and a capture proxy, with schema validation against OpenAPI and Protobuf.

## Quick Start

```bash
# HTTP example
./examples/run-e2e.sh

# HTTP payment example (multi-service)
./examples/run-payment-e2e.sh

# gRPC example (proto-based)
./examples/run-grpc-e2e.sh
```

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

Proto example (file):

```json
{
  "id": "grpc-sut",
  "role": "sut",
  "type": "protobuf",
  "source": { "type": "file", "path": "examples/grpc/examplepb/example.proto" }
}
```

### Proto imports and descriptor sets

- Use `source.import_paths` for proto imports.
- For descriptor sets, set `source.format` to `descriptor` and point `source.path` or `source.url` to the `.desc` file.

Descriptor helper:

```bash
./examples/grpc/gen-descriptor.sh
PROTO_DESCRIPTOR=examples/grpc/examplepb/example.desc ./examples/run-grpc-e2e.sh
```

## JS SDK

See `sdk/js/README.md` for API details. The SDK enforces schemas on `startTestCase` and returns `schemaValidation` in `finishTestCase` responses.

## Coverage Report CLI

Generate a spec coverage report from the artifacts (YAML/JSON):

```bash
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format yaml --output coverage.yaml
go run ./cmd/phrond-coverage --artifacts ./artifacts --sut-name "Example SUT" --format html --output coverage.html
```

The report includes:
- Provided API specs: exercised + not covered paths/services
- Dependent API specs: faked paths/services + asserted fields

## Docs

- Requirements and design: `docs/testing-tool-requirements-and-design.md`
- Artifact OpenAPI schema: `docs/artifact-openapi.yaml`
- HTTPS trust: `docs/https-trust.md`
