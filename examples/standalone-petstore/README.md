# Standalone Petstore Example

This example runs `phrond` in **standalone mode** with preconfigured templates that fake a subset of the Swagger Petstore API.

## Run A Quick Smoke Test

```bash
./examples/run-standalone-petstore.sh
```

The script:
- starts `phrond` in standalone mode,
- serves mocked responses directly from the mock server,
- validates default and scenario-specific template behavior,
- then exits.

## Run As A Long-Lived Demo

```bash
go run ./cmd/phrond \
  --standalone \
  --standalone-templates "examples/standalone-petstore/templates" \
  --standalone-scenario-header "X-Phrond-Scenario"
```

Then call mocked APIs directly:

```bash
curl "http://127.0.0.1:19090/pet/101"
curl -H "X-Phrond-Scenario: pet-not-found" "http://127.0.0.1:19090/pet/101"
```

## Scenario Header

- Header name: `X-Phrond-Scenario`
- Examples:
  - `pet-not-found` -> `/pet/101` returns 404 template
  - `pet-sold` -> `/pet/findByStatus?status=sold` returns sold list template
  - `inventory-low` -> `/store/inventory` returns low-stock inventory template

## Files

- Standalone template configs: `examples/standalone-petstore/templates/*.json`
- Response body templates: `examples/standalone-petstore/templates/bodies/*.tmpl`
- API reference used by this example: `examples/standalone-petstore/petstore-openapi.yaml`
