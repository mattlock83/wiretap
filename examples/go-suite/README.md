# Standard Go test suite

This example uses the application's ordinary HTTP handler, an existing HTTP
client, and Go's standard `testing` package. `TestMain` starts Wiretap and the
handler, then finalizes coverage and Postman exports after all test cleanup.

From the repository root:

```sh
go build -o dist/wiretap ./cmd/wiretap
WIRETAP_BINARY="$PWD/dist/wiretap" go -C examples/go-suite test -count=1 ./...
```

On PowerShell:

```powershell
go build -o dist/wiretap.exe ./cmd/wiretap
$env:WIRETAP_BINARY = (Resolve-Path .\dist\wiretap.exe).Path
go -C examples/go-suite test -count=1 ./...
```

For an existing project, copy the pattern in [main_test.go](main_test.go), pass
your handler to `Run`, point [.wiretap.yaml](.wiretap.yaml) at your API schema, and add
`wiretap.StartTest(t)` to each test or shared test setup helper. Use the returned
session's URL and client wrapper for existing requests. Register user cleanup
after `StartTest` so its requests and final failures are captured.

Run tests with `-count=1` to produce fresh evidence instead of reusing Go's test
cache. Each test package owns its process and archive, so `go test -count=1 ./...`
can run packages concurrently without mixing their results. This example keeps
tests within each package sequential because its handler does not propagate
trace context to downstream services.

Find `last-run.json`, `wiretap.log`, coverage and the Postman collection inside
each new archive under `examples/go-suite/artifacts`. The deliberately invalid
request passes its assertion and remains recorded as failed schema validation.

See the [Go SDK guide](../../sdk/go/README.md) for external servers, gRPC,
parallel tests and output options. The acceptance check
`node scripts/go-sdk-smoke.mjs` copies this example into a clean external Go
module and checks concurrent packages, repeated tests, skips, failures and
exports. It does not publish packages.
