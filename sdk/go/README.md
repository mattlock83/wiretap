# Go test integration

Import `github.com/phrond/wiretap/sdk/go` as `wiretap`. The helper uses the root
Go module and standard OTel APIs. Put addresses and specs in `.wiretap.yaml` and
start `wiretap` alongside the SUT and mocks using your existing harness. Run tests
normally with `go test`. The SDK inherits runtime defaults over HTTP; it never
invokes the CLI or launches test processes.

```go
runtime := wiretap.Runtime{} // WIRETAP_BASE_URL or http://127.0.0.1:18000

t.Run("gets an order", func(t *testing.T) {
    t.Parallel() // only with working SUT propagation / isolated runtimes
    session := runtime.StartTest(t, wiretap.TestOptions{ExplicitSession: true})
    client := session.HTTPClient(existingClient)
    response, err := client.Get(session.SUTURL + "/orders")
    if err != nil { t.Fatal(err) }
    defer response.Body.Close()
    // Keep existing assertions and mock verification here.
})
```

Finish the run from suite cleanup after every test has completed. For a runtime
owned by one Go test package, use its existing `TestMain`:

```go
func TestMain(m *testing.M) {
    code := m.Run() // StartTest starts capture lazily; testing owns execution.
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    runtime := wiretap.Runtime{}
    if err := runtime.FinishRun(ctx); err != nil {
        fmt.Fprintln(os.Stderr, err)
        if code == 0 { code = 1 }
    }
    cancel()
    os.Exit(code)
}
```

`FinishRun` calls `/v1/finishRun` to stop capture and finalize the bundle; the
HTTP control process remains available. For multiple packages/workers sharing
a runtime, finalize once from the outer suite/harness after all packages finish,
instead of each package's `TestMain`. The method returns an error if attempts
are still active. Use a bounded context and propagate finalization errors.

`StartTest` creates a fresh root and registers cleanup. Call it after
`t.Parallel()` and before registering user cleanups so capture teardown runs
last. Cleanup records the final test result, handles `t.Fatal`/skip, drains
accepted traffic, and fails the test if capture/schema validation fails.
It uses an independent cleanup context so a cancelled request context does not
prevent teardown. Setup defaults to a 30-second deadline; set
`TestOptions.SetupTimeout` to allow a larger first-test contract discovery budget,
for example `2 * time.Minute`. Cleanup allows the configured drain plus ten
seconds. An explicitly supplied `Runtime.Client` retains its own timeout, which
must allow both setup and cleanup windows. The root span ends after teardown.

`Session.HTTPClient` clones the supplied standard client and wraps its transport
with `otelhttp`. It binds requests to the test without mutating the shared client.
Cancellation and existing request context values are preserved. Supply a client
whose transport is not already instrumented. For an existing `otelhttp` client,
keep it and create requests using `http.NewRequestWithContext(session.Context,
...)` instead. Set `Runtime.TracerProvider` to reuse an explicit provider; absent
one, the global provider is used, with a local fallback for a no-op global setup.
No exporter or collector is needed; unsampled contexts work.

For gRPC projects, configure `protocol: grpc` and reflection or local Protobuf
sources in `.wiretap.yaml`. `Session.SUTURL` and `Session.Dependencies` contain
direct capture origins for ordinary generated gRPC clients. Pass
`Session.Context` into RPCs and use the application's existing OTel gRPC
instrumentation for parallel ownership. See the [gRPC guide](../../docs/grpc-user-guide.md).

`TestOptions.Schemas` and `TestOptions.Dependencies` accept typed `Schema` and
`Dependency` overrides. `SchemaSource` supports file, URL, and inline content. The returned `Dependencies` map
contains capture URLs. Configure the SUT's dependency endpoints once to stable
`/__wiretap/dep/<asset-id>` URLs for traced sessions. Each API requires its spec unless its
`SchemaRequired` pointer is explicitly false; supplied specs always validate.
HTTP dependencies marked `UpstreamKind: "mock"` always get a separate inferred OpenAPI
draft in the bundle, including with a supplied spec. It documents observed mock
traffic and never changes enforcement. The bundle's Dependencies view links to it.
`DrainTimeout` defaults to five seconds; use a positive duration up to one minute.

Each runtime run writes an OKF bundle automatically. `Session.RunID`,
`Session.BundlePath`, `Session.IndexPath`, and `Session.ViewerPath` identify it.
Open `ViewerPath` for the offline HTML dashboard. Test cleanup commits the
attempt's JSON evidence and regenerates both views. Set
`WIRETAP_OUTPUT_DIR` on the runtime process to choose the output parent. See
the [bundle guide](../../docs/okf-bundles.md) for coverage and optional exports.

One runtime has one reverse SUT target. Distinct per-test `httptest.NewServer`
instances need distinct runtimes if they overlap. Direct handler tests using
`httptest.NewRecorder` bypass the network and cannot be captured. Without SUT
propagation, keep tests and SUT requests sequential or isolate each worker's SUT,
Wiretap, and dependency routes. See [adoption notes](../../docs/otel-adoption.md).

## Exploration inside a Go test

Call `session.Explore(ctx, options)` after asserting the happy path, before test
cleanup. The current test remains open. Wiretap snapshots its completed requests,
runs a fresh baseline and supported invalid-input variations, and records each
generated attempt in the same OKF bundle. Your test framework owns the enclosing
result and any conventional test reports.

```go
session := runtime.StartTest(t, wiretap.TestOptions{})
// Send and assert your happy-path request using the existing HTTP client.

result, err := session.Explore(session.Context, wiretap.ExploreOptions{
    Operations: []string{"POST /orders"},
    Hooks: []wiretap.ExploreHook{
        {
            Event: wiretap.BeforeCase,
            Handler: func(ctx context.Context, in wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
                return wiretap.ExploreHookOutput{}, resetFixtures(ctx)
            },
        },
        {
            Event: wiretap.BeforeRequest,
            Operations: []string{"POST /orders"},
            Handler: func(ctx context.Context, in wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
                token, err := freshToken(ctx)
                return wiretap.ExploreHookOutput{Headers: map[string]string{"Authorization": token}}, err
            },
        },
        {
            Event: wiretap.CheckCase,
            Handler: func(ctx context.Context, in wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
                count, err := countOrders(ctx)
                want := 0
                if in.Mode == "baseline" { want = 1 }
                return wiretap.ExploreHookOutput{Checks: []wiretap.ExploreCheck{
                    {Name: "only a valid order is stored", Passed: count == want},
                }}, err
            },
        },
        {
            Event: wiretap.AfterCase,
            Handler: func(ctx context.Context, in wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
                return wiretap.ExploreHookOutput{}, removeFixtures(ctx)
            },
        },
    },
})
if err != nil { t.Fatal(err) }
t.Logf("%d generated checks passed", result.Passed)
```

The fixture, authentication and database functions are project-owned helpers.
The six hook events are `BeforeExplore`, `BeforeCase`, `BeforeRequest`,
`CheckCase`, `AfterCase` and `AfterExplore`. Case hooks include the baseline;
cleanup hooks run after failures or cancellation. Return an error or failed
named check to fail exploration after cleanup. Each `CheckCase` callback must
return at least one named check; other hooks must not return checks. Do not call `t.Fatal`,
`t.FailNow` or `t.SkipNow` inside a callback: callbacks run in cancellable worker
goroutines. Use the returned error in the outer test, as above.

`Operations` uses exact spec selectors, such as `POST /orders/{id}`. Omit it to
select the supported provider operations captured by this test. `FlowID`
disambiguates repeated calls to an operation. Each hook has its own optional
`Operations` scope; several matching hooks run in declaration order. Case hooks
match the operation being varied; `BeforeRequest` matches each request sent,
including prerequisites. Earlier setup requests may still run outside the
selected mutation scope.

There is no default case cap. Optional `MaxCases` limits the run; inspect
`result.Skipped`, `result.Complete` and `result.Notes` for omitted checks.
`ExplorationError` carries the partial `Result` and unwraps its cause.
`result.BundlePath` identifies the shared bundle.
Exploration supports HTTP/OpenAPI and native gRPC/Protobuf rejection checks.
For gRPC, explicit validation annotations supply constraints such as length and
numeric limits.

`RequestTimeout` defaults to 15 seconds, `HookTimeout` to 30 seconds, and
`CleanupTimeout` to a 30-second SDK cancellation drain. Pass a context tied to the
enclosing test's deadline. Hooks must observe their context: Go cannot forcibly
stop code that ignores cancellation. After cancellation, cleanup callbacks get
a fresh bounded context. Test cleanup also cancels an unfinished exploration and
fails that test.

Hook contexts carry the generated attempt's W3C trace context. Use that context
on requests made by existing OTel clients. `Session.HTTPClient` also honours an
explicit request context and falls back to the parent test for requests without
one. Returning `Bindings`, `Headers` and `State` supplies runtime values to later
hooks; hook code remains in your test project, outside the bundle.

`Runtime.StartExploration`, `NextExploration`, `RespondExploration` and
`CancelExploration` expose the corresponding HTTP endpoints and public protocol
types for custom dispatchers. Generation, request execution and recording stay
in the Wiretap process.

For native gRPC, scope operations using exact procedures such as
`Operations: []string{"/orders.v1.Orders/CreateOrder"}`. Optional
`ExpectedGRPCStatuses` defaults in the binary to `[]int{3}` (`INVALID_ARGUMENT`);
`[]int{3, 11}` additionally accepts `OUT_OF_RANGE`. Authentication, timeout and
internal failures do not satisfy rejection checks.

`ExploreHookInput.GRPCRequest` exposes the procedure, metadata, ProtoJSON
`Messages`, matching `MessagesBase64` protobuf payloads and stream
`ResponseGates`. Return `Headers` for fresh textual metadata. Body bindings use
ProtoJSON field names and JSON pointers; optional `MessageIndex` selects one
request message, while nil applies to every request message. Extractions use
message zero by default and also accept `In: "metadata"` and `In: "trailers"`.
See the runnable [gRPC exploration example](../../examples/grpc-exploration/README.md)
for ordinary grpc-go calls, fresh authentication, fixture reset and side-effect
checks inside a Go test.
