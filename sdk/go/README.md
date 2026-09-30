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
prevent teardown. Setup has a 30-second deadline; cleanup allows the configured
drain plus ten seconds. An explicitly supplied `Runtime.Client` retains its own
timeout, which must allow that cleanup window. The root span ends after teardown.

`Session.HTTPClient` clones the supplied standard client and wraps its transport
with `otelhttp`. It binds requests to the test without mutating the shared client.
Cancellation and existing request context values are preserved. Supply a client
whose transport is not already instrumented. For an existing `otelhttp` client,
keep it and create requests using `http.NewRequestWithContext(session.Context,
...)` instead. Set `Runtime.TracerProvider` to reuse an explicit provider; absent
one, the global provider is used, with a local fallback for a no-op global setup.
No exporter or collector is needed; unsampled contexts work.

`TestOptions.Schemas` and `TestOptions.Dependencies` accept typed `Schema` and
`Dependency` overrides. `SchemaSource` supports file, URL, and inline content. The returned `Dependencies` map
contains capture URLs. Configure the SUT's dependency endpoints once to stable
`/__wiretap/dep/<asset-id>` URLs for traced sessions. Each API requires its spec unless its
`SchemaRequired` pointer is explicitly false; supplied specs always validate.
Dependencies marked `UpstreamKind: "mock"` always get a separate inferred OpenAPI
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
