# Go test integration

Import `github.com/phrond/evermock/sdk/go` as `phrond`. The helper uses the root
Go module and standard OTel APIs. Start Phrond with your existing orchestration;
this helper does not launch or stop a shared runtime.

```go
runtime := phrond.Runtime{BaseURL: os.Getenv("PHROND_BASE_URL")}

t.Run("creates an order", func(t *testing.T) {
    t.Parallel() // only with working SUT propagation / isolated runtimes
    session := runtime.StartTest(t, phrond.TestOptions{
        SUTBaseURL: sut.URL, // shared httptest.NewServer or existing service
        Schemas: []any{map[string]any{
            "type": "openapi",
            "source": map[string]any{"type": "file", "path": "schemas/orders.yaml"},
        }},
        ExplicitSession: true,
    })
    client := session.HTTPClient(existingClient)
    response, err := client.Get(session.SUTURL + "/orders")
    if err != nil { t.Fatal(err) }
    defer response.Body.Close()
    // Keep existing assertions and mock verification here.
})
```

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

`TestOptions.Dependencies` accepts the API dependency registration objects
(`asset_id`, `upstream_url`, `schema`/`schemas`). The returned `Dependencies` map
contains capture URLs. Configure the SUT's dependency endpoints once to stable
`/__phrond/dep/<asset-id>` URLs for traced sessions. `GenerateSchema: true` is the
explicit alternative to SUT schemas; dependencies still require their schemas.
`DrainTimeout` defaults to five seconds; use a positive duration up to one minute.

One runtime has one reverse SUT target. Distinct per-test `httptest.NewServer`
instances need distinct runtimes if they overlap. Direct handler tests using
`httptest.NewRecorder` bypass the network and cannot be captured. Without SUT
propagation, keep tests and SUT requests sequential or isolate each worker's SUT,
Phrond, and dependency routes. See [adoption notes](../../docs/otel-adoption.md).
