# Go SDK

Add API capture to standard Go tests with two hooks: `wiretap.Run(m)` in
`TestMain`, and `wiretap.StartTest(t)` in each test or shared setup helper. Keep
your existing requests, assertions and mocks. Wiretap starts on available ports,
records each test's final outcome, generates coverage and Postman exports, and
stops when the package finishes.

## Configure once

Import `github.com/mattlock83/wiretap/sdk/go` as `wiretap`. The SDK is part of
Wiretap's root Go module and requires Go 1.26+. To use a local checkout in another
Go module, add a replacement and run `go mod tidy` after adding the imports:

```sh
go mod edit -replace=github.com/mattlock83/wiretap=/absolute/path/to/wiretap
go mod tidy
```

Put the locally built `wiretap` executable on `PATH`, or set `WIRETAP_BINARY` to
its absolute path. See [local installation](../../docs/local-release.md).
The SDK does not download or build binaries.

Put `.wiretap.yaml` at the project root:

```yaml
version: 1
application: {id: my-api}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text], postman: true}
sut:
  id: my-api
  kind: service
  url: http://127.0.0.1:3000 # Your existing API test server.
  schema: openapi.yaml
```

Configuration is discovered from the test package's directory up to the nearest
parent containing `.wiretap.yaml`. Schema and output paths are relative to that
file. Use synthetic data and dummy credentials; captured traffic is retained
unchanged.

The main HTTP API requires its OpenAPI spec by default. HTTP dependencies and
mocks need only their URL and kind; add a dependency `schema` when you want
validation and contract coverage. No opt-out flag is needed for schema-free
dependency capture. Supplied specs always validate. gRPC still needs a Protobuf
descriptor, discovered through reflection when no source file is supplied.

For an HTTP inventory mock, add this alongside `sut` in the same file:

```yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
```

Point the application's inventory client at `session.Dependencies["inventory"]`
so its calls are captured. See [HTTP dependency schema options](docs/advanced.md#http-dependency-schema-options)
for typed overrides and requiring a spec. Without a spec, calls and Postman
examples remain available, while that dependency's contract coverage is unknown.

## Connect your tests

Add the suite hook to your existing test package:

```go
import (
    "os"
    "testing"

    wiretap "github.com/mattlock83/wiretap/sdk/go"
)

func TestMain(m *testing.M) {
    os.Exit(wiretap.Run(m))
}
```

Start capture in each test, or in the shared helper your tests already call:

```go
func TestOrders(t *testing.T) {
    session := wiretap.StartTest(t)
    client := session.HTTPClient(existingClient)

    response, err := client.Get(session.SUTURL + "/orders")
    if err != nil {
        t.Fatal(err)
    }
    defer response.Body.Close()
    // Keep your existing assertions and mock verification.
}
```

`HTTPClient` clones your standard HTTP client and attaches the test's tracing
context. Use the returned `SUTURL` so requests pass through capture. Tests that
call a handler directly with `httptest.NewRecorder` bypass the network and need
an HTTP client/server boundary to be captured.

Run your usual test command with caching disabled to get fresh artifacts:

```sh
go test -count=1 ./...
```

`Run` preserves the test result and returns a failure code if startup, capture
finalization or shutdown fails. `StartTest` records failed assertions, `t.Fatal`,
skips after capture begins, and failing cleanup hooks. Filtered tests and tests
skipped before `StartTest` do not create attempts. Register it before your own
`t.Cleanup` calls so Wiretap observes their outcomes. API contract violations are separate evidence:
deliberate invalid requests can still pass their test assertions.

## Let Wiretap host an HTTP handler

If your application exposes an `http.Handler`, pass it to `Run` and omit `sut.url`
from the configuration:

```go
func TestMain(m *testing.M) {
    os.Exit(wiretap.Run(m, wiretap.RunOptions{Handler: newAPIHandler()}))
}
```

Wiretap serves the handler on an available local port and keeps it alive through
test cleanup and export. Existing suites can instead keep their own servers and
mocks. See the [runnable Go example](../../examples/go-suite/README.md).

## Keep the outputs

Each test package owns its Wiretap process and a unique run archive, even when
`go test ./...` executes packages concurrently. Retain the complete configured
output directory in CI on success and failure. Normal suite completion writes
`last-run.json` with exact artifact/export paths and a `wiretap.log` inside the
run directory, alongside captured traffic, contracts, test outcomes and exports.

Normal test failures still finalize exports. A hard panic, `go test -timeout`,
or process termination can leave an interrupted archive without completed
exports; the owned runtime stops when its test process disappears.

Tests run serially by default. For `t.Parallel()`, enable `ExplicitSession` and
propagate W3C trace context through captured application dependencies. Call
`StartTest` after `t.Parallel()`. See [parallel capture and advanced options](docs/advanced.md).

The existing `Runtime` API remains available for an externally managed process,
shared suite ownership, custom tracing providers and manual finalization. See
the [advanced guide](docs/advanced.md), [gRPC guide](../../docs/grpc-user-guide.md)
and [configuration reference](../../docs/configuration.md).
