# Advanced Go integration

Start with the [managed test quickstart](../README.md). Both managed and external
runtimes use the same `Session`, `TestOptions` and control API.

## Managed suite options

`wiretap.Run(m)` owns one runtime for the current Go test package. It calls
`m.Run()` synchronously, then finalizes capture, writes the selected exports and
stops its owned process. It returns the exit code; it never calls `os.Exit` or
installs a signal handler.

Pass `RunOptions` when you need to choose the binary, configuration, output parent
or owned HTTP handler:

```go
func TestMain(m *testing.M) {
    os.Exit(wiretap.Run(m, wiretap.RunOptions{
        Binary:        "/absolute/path/to/wiretap",
        Config:        "/absolute/path/to/.wiretap.yaml",
        OutputDir:     "/absolute/path/to/artifacts",
        Handler:       newAPIHandler(),
        FinishTimeout: 2 * time.Minute,
    }))
}
```

`Dir` selects the runtime's working directory. `StartupTimeout`, `FinishTimeout`
and `ShutdownTimeout` bound the owned process lifecycle, defaulting to 30 seconds,
30 seconds and 5 seconds respectively. `Outputs` optionally
overrides the configuration's export selection:

```go
Outputs: &wiretap.OutputOptions{
    Coverage: []string{"json", "text"},
    Postman:  true,
    Arazzo:   true,
},
```

Use the per-run `last-run.json` receipt for exact archive and export paths.
Exports that succeed remain available even when another export fails. Preserve
the whole output parent when several test packages run, rather than selecting
the newest directory.

`Run` returns only after its handler and runtime have stopped. If existing
`TestMain` setup uses deferred cleanup, put that lifecycle in a function that
returns before calling `os.Exit`, since Go does not run defers from `os.Exit`:

```go
func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) int {
    stopMocks := startMocks()
    defer stopMocks()
    return wiretap.Run(m)
}
```

## Per-test setup and tracing

Package-level `wiretap.StartTest(t)` requires an active `wiretap.Run(m)`. Supply
optional overrides with `wiretap.StartTest(t, wiretap.TestOptions{...})`. It
infers the test name from `testing.TB` and registers cleanup that runs after
subtests and later registered user cleanups. Keep capture at one level: avoid
starting another serial session in a subtest while its parent session is active.

`StartTest` uses an independent cleanup context, so a cancelled request context
does not prevent teardown. Setup defaults to a 30-second deadline. Increase
`TestOptions.SetupTimeout` for larger or slower contract discovery, for example
`2 * time.Minute`. `DrainTimeout` defaults to five seconds and accepts a positive
duration up to one minute; teardown allows the drain plus ten seconds. The
attempt's root span ends after teardown.

`Session.HTTPClient` clones the supplied standard client and wraps its transport
with `otelhttp`, without mutating shared clients. A nil client uses standard HTTP
defaults. Request cancellation, deadlines and context values are preserved.
Supply a client whose transport is not already instrumented. For an existing
`otelhttp` client, keep it and create requests with the session context:

```go
request, err := http.NewRequestWithContext(session.Context, http.MethodGet,
    session.SUTURL + "/orders", nil)
if err != nil {
    t.Fatal(err)
}
response, err := existingInstrumentedClient.Do(request)
```

The global tracing provider is used when available, with a local fallback for a
no-op global setup. No exporter or collector is required; unsampled contexts work.
For an explicit provider, use the external `Runtime.TracerProvider` field.

## Parallel tests and subtests

Concurrent attempts require explicit session ownership. Enable it for the
managed package and call `StartTest` after `t.Parallel()`:

```go
func TestMain(m *testing.M) {
    os.Exit(wiretap.Run(m, wiretap.RunOptions{ExplicitSession: true}))
}

func TestOrders(t *testing.T) {
    t.Parallel()
    session := wiretap.StartTest(t)
    client := session.HTTPClient(existingClient)
    // Use client and session.SUTURL for this test's requests.
}
```

You can also set `TestOptions.ExplicitSession` on individual attempts. The SUT
must propagate W3C trace context into captured downstream requests so Wiretap can
associate them with the correct test. Without propagation, keep tests and SUT
requests sequential, or isolate each worker's SUT, Wiretap and dependency routes.
See [tracing and adoption](../../../docs/otel-adoption.md).

One runtime has one reverse SUT target. Overlapping per-test
`httptest.NewServer` instances require distinct runtimes. A managed package can
host one shared handler with `RunOptions.Handler`; application state and mocks
must themselves support the chosen concurrency.

For table-driven subtests, call `StartTest` inside each `t.Run` callback instead
of the parent test so the archive records each case separately. Keep the same
test names across runs for stable identities. `TestOptions.Identity` allows an
explicit identity, variant and attempt ordinal when your harness needs them.
Default identities include the package and test name. Repeated executions such
as `-count=2` receive successive attempt ordinals without changing that identity.

## gRPC and dependency capture

For gRPC, configure `protocol: grpc` and reflection or local Protobuf sources in
`.wiretap.yaml`. `Session.SUTURL` and `Session.Dependencies` contain direct capture
origins for ordinary generated gRPC clients. Pass `Session.Context` into RPCs and
use the application's existing OTel gRPC instrumentation for parallel ownership.
See the [gRPC guide](../../../docs/grpc-user-guide.md).

### HTTP dependency schema options

For HTTP, `TestOptions.Schemas` and `TestOptions.Dependencies` accept typed
`Schema` and `Dependency` overrides. Nil slices inherit the project's defaults;
non-nil empty slices (`[]wiretap.Schema{}` or `[]wiretap.Dependency{}`) clear them
for that test. Clearing the SUT schemas also requires `SchemaRequired` set to
false when the project requires a schema. `SchemaSource` supports file, URL and
inline content. The
returned `Dependencies` map contains capture URLs. Point the SUT's downstream
clients at those URLs in your shared test setup; declaring a dependency does not
reroute an application's existing clients. Managed runtimes choose available
ports, so read the returned URLs instead of assuming a fixed proxy port. HTTP's
`/__wiretap/dep/<asset-id>` routes remain stable for traced sessions in that run;
native gRPC uses the returned direct origin without a gateway path. The main
HTTP API requires a spec by default; its `TestOptions.SchemaRequired` pointer
can explicitly opt out. HTTP dependencies need no spec or opt-out flag. Add
`Dependency.Schema`/`Schemas` to opt into validation and coverage, or set its
`SchemaRequired` pointer to true to enforce availability. A nil pointer uses
the boundary default; explicit true and false are preserved. Supplied specs
always validate. An omitted dependency spec leaves its contract coverage
unknown while calls and Postman examples remain captured. gRPC boundaries
always require their configured Protobuf contracts and cannot be created or retargeted per test. See
[boundary configuration](../../../docs/configuration.md).

With the main SUT's required OpenAPI spec configured in `.wiretap.yaml`, an
existing test can opt into strict inventory schema validation like this:

```go
requireInventorySpec := true
session := wiretap.StartTest(t, wiretap.TestOptions{
    Dependencies: []wiretap.Dependency{{
        AssetID:        "inventory",
        UpstreamURL:    "http://127.0.0.1:9090",
        UpstreamKind:   "mock",
        SchemaRequired: &requireInventorySpec,
        Schema: &wiretap.Schema{
            ID:   "inventory",
            Type: "openapi",
            Source: wiretap.SchemaSource{
                Type: "file",
                Path: "/project/schemas/inventory.yaml",
            },
        },
    }},
})
// Configure your existing inventory client with session.Dependencies["inventory"].
```

For ordinary schema-free dependency capture, omit `Schema` and `SchemaRequired`
from that entry. The main API still inherits its required spec from the project.
These per-test dependency entries replace the project dependency list; include
every dependency that test should capture. API file paths resolve on the runtime
host.

A schema-free dependency retains calls and Postman examples with unknown
contract coverage in the consumer archive. A later
[assumption check](../../../docs/assumption-checks.md) can validate its examples
under the provider's frozen contract, while leaving that original coverage
unchanged. It still needs a matching complete passing provider test; the
provider spec alone is insufficient.

### gRPC harness ownership

If your application must receive dynamic gRPC dependency listener URLs before
it can start, let its existing outer harness own the runtime and bootstrap order.
The [native gRPC](../../../examples/grpc-boundaries/README.md) and
[streaming gRPC](../../../examples/grpc-streaming/README.md) examples demonstrate
that arrangement. Their shell harnesses finalize once; their test packages use
`Runtime.StartTest` and do not also call managed `Run`.

## Externally managed runtimes

Keep the existing `Runtime` API if your harness or container setup owns Wiretap.
It connects over HTTP and never launches a process:

```go
var capture = wiretap.Runtime{} // WIRETAP_BASE_URL or http://127.0.0.1:18000

func TestOrders(t *testing.T) {
    session := capture.StartTest(t, wiretap.TestOptions{})
    client := session.HTTPClient(existingClient)
    // Existing requests and assertions use client and session.SUTURL.
}
```

`Runtime.BaseURL`, `Runtime.Client` and `Runtime.TracerProvider` override its
connection and tracing settings. An explicitly supplied HTTP client retains its
own timeout, which must allow both the setup and cleanup windows.

Finish once, after every test and worker sharing the runtime has completed. For
a runtime owned by one package, use the package's existing `TestMain`:

```go
func TestMain(m *testing.M) {
    code := m.Run()
    ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
    if err := capture.FinishRun(ctx); err != nil {
        fmt.Fprintln(os.Stderr, err)
        if code == 0 {
            code = 1
        }
    }
    cancel()
    os.Exit(code)
}
```

For multiple packages or workers sharing one runtime, finalize once from the
outer harness after they all finish. `FinishRun` stops capture and finalizes the
archive while leaving the external process available. It returns an error if
attempts remain active. Use a bounded context and propagate finalization errors.

`FinishRunWithOutputs(ctx, options)` additionally returns the exact archive and
available export paths. A nil `options` inherits configured outputs. Preserve
both its result and error: a successful export remains available when another
fails. Test cleanup commits attempt evidence, frozen contracts and retained
payloads; `Session.RunID` and `Session.BundlePath` identify the archive. See the
[archive guide](../../../docs/bundle-evidence.md).
