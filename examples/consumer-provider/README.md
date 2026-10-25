# Catch a provider change before it breaks a consumer

This example captures a consumer tested against an inventory mock and a provider
tested independently. Both suites pass their own assertions. Wiretap compares
their captured traffic, frozen contracts and generated Postman collections to
check whether the mock still agrees with the provider.

The complete workflow uses Go's standard HTTP client, server and test runner.
It starts services on available local ports, captures four separate archives,
and checks the same consumer evidence against three provider captures:

| Provider capture | Response and provider spec | Consumer check | Provider check |
| --- | --- | --- | --- |
| `original` | `{"quantity":3}` | Pass, exit `0` | Pass, exit `0` |
| `breaking` | `{"availableQuantity":3}` | Mismatch, exit `1` | Mismatch, exit `1` |
| `fixed` | `{"quantity":3}` restored | Pass, exit `0` | Pass, exit `0` |

The breaking provider has an updated OpenAPI document and a passing test for its
new response. The consumer's mock still returns `quantity`, so its test also
passes. Its application reads that original field to decide whether an order
is ready. Renaming it would make the application read zero instead.

Wiretap detects the disagreement without running the applications together.
The correction restores the provider's original field and contract; the
consumer archive is unchanged. Each check verifies the actual exit code and
JSON result, including the response mismatch in the breaking phase. The outer
workflow passes only when this entire pass/fail/pass sequence behaves as shown.

## Run it

From the repository root with Go 1.26+:

```sh
go build -o dist/wiretap ./cmd/wiretap
WIRETAP_BINARY="$PWD/dist/wiretap" go test -count=1 -v ./examples/consumer-provider
```

In PowerShell:

```powershell
go build -o dist/wiretap.exe ./cmd/wiretap
$env:WIRETAP_BINARY = (Resolve-Path dist/wiretap.exe).Path
go test -count=1 -v ./examples/consumer-provider
```

`WIRETAP_BINARY` is required for this example, even if `wiretap` is on `PATH`.
An ordinary `go test ./...` without that variable **skips** this workflow; it is
not an acceptance run. No package installation or external service is needed.

## Follow the code

- [app.go](app.go) contains the two small HTTP applications. The inventory
  handler also supplies the consumer's mock response.
- [workflow_test.go](workflow_test.go) contains the real consumer and provider
  assertions, their Go SDK hooks, and the workflow that runs each suite in its
  own process before invoking `wiretap check`.
- [consumer.yaml](consumer.yaml) declares the mocked dependency and its mapping
  to the provider application/API. The consumer does not copy the provider's
  spec. Its downstream call uses serial test attribution; the tests do not run
  in parallel.
- [provider.yaml](provider.yaml) selects either the
  [original spec](inventory.openapi.yaml) or the
  [renamed-field spec](inventory-renamed.openapi.yaml). Each archive freezes the
  spec used by that capture.

The successful checks report `openapi_3_0_provider_contract`: both concrete
exchanges validate against the provider's frozen spec and a passing provider
test supports the mock response. This does not prove compatibility for requests
the provider tests have not exercised. See [assumption checks](../../docs/assumption-checks.md).

## Inspect the evidence

Every invocation creates and prints its own directory under
`examples/consumer-provider/artifacts/workflow-*/`. Files remain available on
success and failure. Set `WIRETAP_OUTPUT_DIR` to an absolute path to choose a
different parent directory.

```text
workflow-<unique-id>/
  consumer/<run-id>/
  original/<run-id>/
  breaking/<run-id>/
  fixed/<run-id>/
  original-consumer-check.json
  original-provider-check.json
  breaking-consumer-check.json
  breaking-provider-check.json
  fixed-consumer-check.json
  fixed-provider-check.json
```

Each `<run-id>/` contains `bundle.json`, captured tests and contracts,
`last-run.json`, `wiretap.log`, `exports/coverage.json`, `exports/coverage.txt`
and `exports/postman/`. The test log prints each archive's canonical receipt
path and exact coverage and Postman output paths. Preserve the complete archive
so checks can validate its Postman evidence against the captured traffic.

To inspect a result yourself, replace the two paths with the exact archive
paths printed by the workflow:

```sh
dist/wiretap check consumer \
  --consumer <consumer-archive> --provider <breaking-provider-archive> \
  --dependency inventory
```

The text result identifies the differing response fields. Replace `consumer`
with `provider` to ask the same compatibility question from the provider's
viewpoint, or select the `fixed` archive to see it pass. These commands operate
offline; the original test servers have already stopped.
