# Using Wiretap with gRPC

Keep your generated clients, test framework and mock servers. Point their network
connections through Wiretap and use the existing test lifecycle integration.
Wiretap captures the RPCs, associates them with named tests, and produces a
JSON artifact describing your provided APIs and dependency observations.

Every gRPC boundary needs a Protobuf contract. For standalone development and
test instances, Wiretap uses server reflection by default. If reflection is
disabled, supply local `.proto` files or a descriptor set instead. Reflection is
a convenient source of the contract, not a requirement.

## Start with reflection

This example has an Orders SUT on port `50051` and an existing Inventory mock on
port `50052`. The test calls Wiretap on `18080`; the SUT calls the dependency
capture listener on `18081`.

```yaml
version: 1
output_dir: artifacts
http_listen: 127.0.0.1:18000
proxy_listen: 127.0.0.1:18080

sut:
  id: orders
  protocol: grpc
  url: http://127.0.0.1:50051
  kind: service
  services: [orders.v1.OrdersService]

dependencies:
  inventory:
    protocol: grpc
    url: http://127.0.0.1:50052
    capture_listen: 127.0.0.1:18081
    kind: mock
    services: [inventory.v1.InventoryService]
```

Omitting the `schema` field selects reflection against that boundary's upstream `url`.
It does not enable reflection on the application: the SUT and mock must already
expose it, or you must choose a local contract source.
Explicit `schema: null`, `schema: {}` and bare strings are rejected; they do not
accidentally opt an API into discovery.

With reflection, `services` is optional. If omitted, Wiretap discovers all
advertised services except `grpc.health.v1.Health`,
`grpc.reflection.v1.ServerReflection` and
`grpc.reflection.v1alpha.ServerReflection`. The bundle records this selection
and the exclusions; it describes what that target advertises, not a claim about
every service in the system. Specify `services` when you want to measure only
part of a server, or explicitly include an infrastructure service. Methods in
the selected services appear in the inventory even if no test calls them.

### Enable reflection in a Go development server

If you own the server, register reflection after your application services and
before calling `Serve`. For example, add the import and registration to your
existing server startup:

```go
import "google.golang.org/grpc/reflection"

// Application services have already been registered on server.
if enableDevelopmentReflection {
    reflection.Register(server)
}
```

The condition above is your application's development configuration. The
`reflection.Register` function registers the v1 and v1alpha reflection services.
See the [grpc-go reflection documentation](https://pkg.go.dev/google.golang.org/grpc/reflection#Register).
For other server libraries, use their standard reflection support. Alternatively,
keep the server unchanged and provide local contracts as described below.

### Reflection credentials and timeout

Use an explicit source when reflection requires metadata or a different timeout:

```yaml
schema:
  source: reflection
  timeout: 10s
  headers:
    authorization: '${TOKEN}'
```

This block goes under the relevant SUT or dependency. Set `TOKEN` in the Wiretap
process's environment; include any required authentication scheme in its value.
The default discovery timeout is ten seconds per boundary; an explicit timeout
must be greater than zero and at most one minute. Reflection reuses that
boundary's upstream TLS settings. Allow enough time in the harness for all
contracts to resolve: Go's `TestOptions.SetupTimeout` overrides its 30-second
setup default, and JavaScript's optional `timeoutMs` controls HTTP API deadlines.

These headers authenticate Wiretap's discovery requests only. Your tests and SUT
still supply their normal RPC credentials. Discovery credentials are private
runtime settings and are excluded from bundle contents and diagnostics.

## Run through your existing test lifecycle

Managed Go, Jest, Mocha and Playwright integrations own their Wiretap process and
finalize after per-test cleanup. Keep your existing SUT and mock setup, or use
the SDK's HTTP handler hosting where applicable. See the
[SDK integration guide](getting-started-existing-tests.md) for runner setup.

For a project-owned Wiretap process, the startup order is:

1. Start Wiretap with `wiretap --config .wiretap.yaml`.
2. Call `POST /v1/startRun`. It binds capture listeners, returns the SUT and
   dependency addresses, and reports that contract inventory is unresolved.
   It does not wait for reflection or for your applications to start.
3. Start your mocks and SUT. Configure the SUT's Inventory client to call
   `127.0.0.1:18081`; configure the test's Orders client to call
   `127.0.0.1:18080`. Wait for the applications to be ready using your harness.
4. Call `setupTest` through your SDK or the HTTP API. The first call resolves and
   freezes contracts for **all registered boundaries**, including dependencies
   this particular test might not call. A discovery or contract error admits no
   test. Do not send the test's RPCs until setup succeeds.
5. Run the existing calls and assertions. Await their completion, then call
   `teardownTest` with the framework's final outcome.
6. After every test is torn down, call `finishRun` to finalize the bundle.

The first successful setup establishes an immutable contract snapshot for the
run. Later tests reuse it. gRPC targets and contract sources are fixed for the
run; changing either requires a new run. Also start a new run if a contract
changes at the same target. If a run has no test attempts, `finishRun` still
resolves outstanding contracts before it can
mark the bundle complete; failed discovery cannot become a complete empty report.
Define gRPC boundaries in `.wiretap.yaml` or the `startRun.boundaries` object
described in the [runtime API](runtime-api.md). The SDK/API inherits their
protocol, target and schema source. Per-test overrides cannot switch a boundary to gRPC,
replace its target or remove a configured gRPC dependency during a run.

For manual serial integration, these are the control endpoints:

```bash
curl --fail-with-body -sS http://127.0.0.1:18000/v1/startRun \
  -H 'Content-Type: application/json' -d '{}'

# Start and wait for the SUT and mocks before this request.
curl --fail-with-body -sS http://127.0.0.1:18000/v1/setupTest \
  -H 'Content-Type: application/json' \
  -d '{"name":"creates an order"}'
```

Use the returned `session_id` when the test finishes. The value below is a
placeholder; send `"fail"` if your framework reports failure:

```bash
curl --fail-with-body -sS http://127.0.0.1:18000/v1/teardownTest \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"REPLACE_WITH_RETURNED_SESSION_ID","status":"pass"}'

curl --fail-with-body -sS http://127.0.0.1:18000/v1/finishRun \
  -H 'Content-Type: application/json' -d '{}'
```

SDK lifecycle calls perform the same control requests. The [runtime API](runtime-api.md) and [SDK integration guide](getting-started-existing-tests.md)
describe lifecycle ownership. The [runnable example](../examples/grpc-boundaries/README.md)
shows native Go clients, reflection and an existing mock.

### Use the returned addresses

For gRPC, `sut_url` and the `dependencies` map contain direct origins such as
`http://127.0.0.1:18081`, with no path prefix. Native gRPC clients normally take
the `host:port` part and a separate TLS/insecure credential setting. Their
generated code continues to use `/inventory.v1.InventoryService/GetItem`.

Do not append the HTTP dependency gateway's `/__wiretap/dep/...` prefix to a
native gRPC target. If you choose `capture_listen: 127.0.0.1:0`, read the allocated
address from `startRun` before starting the SUT. An in-process mock such as a Go
`bufconn` server must expose a network listener for an external Wiretap process
to observe it.

## Supply local contracts instead

Choose one source for each boundary. Sources may differ between the SUT and
dependencies. An explicit local source makes no reflection requests and never
falls back to reflection if a file is missing or invalid.

### Existing `.proto` files

```yaml
sut:
  id: orders
  protocol: grpc
  url: http://127.0.0.1:50051
  kind: service
  services: [orders.v1.OrdersService]
  schema:
    source: proto
    files: [orders/v1/orders.proto]
    import_paths: [./proto, ./third_party]
```

`files` contains logical proto filenames, resolved within `import_paths`.
Here Wiretap can read `./proto/orders/v1/orders.proto`. An import such as
`acme/types/v1/money.proto` must be available under one of those roots, for
example `./third_party/acme/types/v1/money.proto`.

Import roots are relative to `.wiretap.yaml`. Supply third-party imports locally,
including definitions for custom options such as validation annotations.
Standard Protobuf imports are built in. Wiretap compiles the supplied source
in-process; it does not require Buf or protoc, download imports, invoke plugins,
or resolve a Buf registry dependency automatically.

### A descriptor set from your build

```yaml
sut:
  id: orders
  protocol: grpc
  url: http://127.0.0.1:50051
  kind: service
  services: [orders.v1.OrdersService]
  schema:
    source: descriptor_set
    path: build/api.protoset
```

The path is relative to `.wiretap.yaml`. Supply a binary Protobuf
`FileDescriptorSet` containing the selected services and their complete import
closure, including option definitions. From an existing Buf project, your build
can create one with:

```bash
mkdir -p build
buf build --as-file-descriptor-set -o build/api.protoset
```

Keep imports included. This is an optional build step; Wiretap itself does not
run Buf. See the [Buf build command](https://buf.build/docs/reference/cli/buf/build/).

Both local modes require an explicit, nonempty `services` list. A descriptor set
or source tree can contain services outside the boundary you are measuring;
Wiretap should not count all of them merely because their definitions are present.

The existing `schema: path/to/openapi.yaml` string remains valid for HTTP APIs.
For `protocol: grpc`, use one of the source objects above; a bare string is
rejected instead of guessing its format. `schema_required: false` is not allowed
for a gRPC boundary. If its contract cannot be obtained, fix the source before
running the tests.

## TLS and tracing

Use `http://` for cleartext HTTP/2, or `https://` for HTTP/2 over TLS. A gRPC
upstream URL has an origin only: no credentials, query, fragment or base path.
Optional settings on each boundary are:

```yaml
capture_tls:
  cert_file: certs/wiretap.crt
  key_file: certs/wiretap.key
upstream_tls:
  ca_file: certs/development-ca.crt
  server_name: orders.test
```

`capture_tls` configures the listener your test or SUT calls. Configure that
client's normal trust settings accordingly. `upstream_tls` configures Wiretap's
connection to the real SUT or mock, including reflection. Paths are relative to
the configuration file. Both capture certificate and key are required together;
certificate checks are not disabled automatically. mTLS is not supported. Certificate/key files and discovery credentials do not enter bundles.

For parallel tests sharing one SUT, standard W3C trace context must travel from
the test through the SUT to its dependency calls. Reflection discovers contracts;
it does not provide test correlation. Each attempt needs its own trace, and
sharing one gRPC connection does not identify the calling test.

- **Go:** keep your generated client and pass the Wiretap session's `Context` to
  its RPC methods. Existing standard OTel gRPC instrumentation should be reused.
  Where needed, `otelgrpc.NewClientHandler` integrates through
  `grpc.WithStatsHandler`; configure W3C propagation consistently with the server
  and outbound clients. See [OTel Go gRPC instrumentation](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc).
- **Node:** run calls inside the SDK's test context and load standard gRPC
  instrumentation before importing the gRPC client. HTTP/Undici instrumentation
  does not cover `@grpc/grpc-js`; the OTel
  [`@opentelemetry/instrumentation-grpc` package](https://github.com/open-telemetry/opentelemetry-js/tree/main/experimental/packages/opentelemetry-instrumentation-grpc)
  supplies that integration. Reuse an existing installation and tracing setup.

No special Wiretap RPC client, collector or Wiretap-specific trace metadata is
required. If the application does not propagate context, run one test at a time
or give each parallel worker its own runtime, SUT and mocks. Await the test's
RPCs and background work before teardown.

## Read the result and its limits

The raw artifact contains the frozen contract, selected service/method inventory,
captured calls and payloads, test references and dependency observations. Generate
JSON/YAML coverage or Postman collections with `wiretap report` or `export`. A
copied artifact works without running servers, reflection, source proto files or
a schema registry. See [capture artifacts and exports](artifacts.md).

Contract comparison uses the `wiretap-protobuf-1` normalization profile: the
complete descriptor set and selected services, with file ordering and source
comments/locations removed from the identity. Unrelated declarations can change
the revision. Source mode, host addresses and local file paths do not affect it;
different compilers or source-retained options can still produce different
revisions. A supplied descriptor set is preserved byte-for-byte for integrity.
Each boundary also records acquisition time, source kind, the normalization
profile, the source compiler version when applicable, and the reflection
version/advertised inventory/default exclusions when reflection was used.
Selected services remain explicit on that boundary's schema reference.
Credentials and private source paths are not included.

Reflection on a mock tells Wiretap what **the mock advertises**. It does not
prove agreement with the real provider. To check your mock traffic against a
provider-published contract, explicitly supply that provider's proto files or
descriptor set for the dependency. Keep the dependency marked `kind: mock`:
conformance to a contract still is not evidence that the provider actually
implements those behaviours.

Rich RPC errors such as `google.rpc.BadRequest` appear in response
`status_details` within the JSON recording. Custom `Any` types use the boundary's frozen protos;
standard error types absent from those protos use a separately frozen helper
descriptor set. Unknown types remain opaque with a reason. Wiretap does not
fetch type URLs, change the service contract or change errors delivered to your
client. Error details describe observed behaviour; they are not new coverage
targets or proof that a mocked error matches a real provider.

Use synthetic error details and dummy credentials. Wiretap retains messages,
metadata, trailers and status details unchanged within its capture limits.

Status-detail retention shares the response's 64 KiB metadata budget, with at
most 256 details and a recursion depth of 100. Details outside those limits are
labelled truncated or unavailable. The terminal gRPC status remains the outcome,
including when an envelope is malformed or contradicts it.

Native gRPC capture supports unary, server-streaming, client-streaming and
bidirectional RPCs. Requests and responses are forwarded unchanged while Wiretap
observes frames incrementally. Each invocation stays one exchange and belongs to
one test, including when send and receive overlap on a shared channel.

Messages carry their original wire index and monotonic start/completion offsets.
The JSON evidence retains cross-direction observation order, metadata, half-close
and terminal status; observation order does not prove business causality. Direction
summaries show frames observed, retained and omitted, observed bytes, EOF,
partial frames and whether counts are known. Byte and 1,024-message retention
limits bound each direction; later frames are counted without being retained.
An early response does not fabricate request EOF. Teardown can commit an
immutable incomplete snapshot, and run shutdown cancels remaining streams.

Validation checks selected methods, framing, Protobuf decoding and the cardinality
declared by each method. A non-streaming request side requires one message; a
non-streaming response permits at most one and requires one on OK. A streaming
side permits zero or more. A zero-byte Protobuf message is one message; an empty
stream contains none. Expected non-OK outcomes remain valid test observations.

Unavailable capture or decoding produces **partial** validation with explicit
reasons. It does not turn an otherwise passing test into a schema failure.
Malformed wire data and contract violations still fail validation, independently
of the framework outcome. A deliberate rejection test can pass its assertions.
Truncated or otherwise incomplete evidence never gains assurance by being
merely readable. Compatibility handling for legacy-redacted archives preserves
the same limit.

Contract coverage uses `wiretap-protobuf-2`: methods, explicit field presence,
oneof choices, declared enum values, booleans, collection emptiness and supported
nested fields. Complete passing streams can contribute conditions from each
message, with exact message references. An empty stream can exercise a method
without covering payload conditions. There is no invented denominator for stream
lengths, cross-message combinations, allowed error codes or business invariants.
Descriptor normalization remains `wiretap-protobuf-1`; measurement and descriptor
identity are separate profiles.

Protobuf does not normally specify HTTP-style ranges, regexes or response-code
alternatives. Wiretap does not invent those rules. Arbitrary CEL/Protovalidate
annotations are not part of passive condition measurement; recursive/dynamic
`Any` condition expansion also remains explicitly unmeasured; known `Any` values can still be decoded for inspection. See the
[coverage rules](contract-coverage-rules.md).

Try the [streaming example](../examples/grpc-streaming/README.md) or the
[native Node example](../examples/grpc-boundaries/node/README.md). Connect's own
wire protocol, gRPC-Web and mTLS are not supported. Existing clients,
mocks and test framework hooks remain the integration model.

## If setup fails

| Problem | What to check |
| --- | --- |
| Reflection unavailable or times out | Ensure all upstreams are running and ready, enable reflection on the selected development instance, or select a local contract source. |
| Reflection authentication fails | Set the reflection headers for that boundary and ensure the Wiretap process receives the referenced environment variables. These are separate from test RPC credentials. |
| An import cannot be resolved | Add its directory to `import_paths`, supply the third-party proto locally, or regenerate the descriptor set with imports included. |
| A selected service is missing | Use its fully qualified Protobuf name and verify that the chosen source describes the intended server/version. |
| TLS or HTTP/2 negotiation fails | Match the URL scheme, CA, certificate hostname and listener credentials. A plain HTTP/1 server is not a native gRPC upstream. |
| Parallel calls have ambiguous ownership | Verify W3C propagation through the SUT or switch to sequential tests/isolated worker instances. |
| A test closes with calls still running | Await/cancel the test's RPCs before teardown; an incomplete capture is not successful coverage. |

Contract resolution fails atomically before admitting the first test. There is
no fallback to a schema-free gRPC capture when discovery, imports or validation
fail. Before a snapshot is established, you can repair a missing file or make
the configured server available and retry setup. Changing the configured source
or target requires a new run. An established contract snapshot changes only
when you start a new run; restart Wiretap too if you edit its configuration file.
