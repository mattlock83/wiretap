# Wiretap

Wiretap captures existing HTTP and native gRPC API tests, checks traffic against
OpenAPI or Protobuf contracts, and writes portable JSON artifacts with frozen
contracts and retained payloads. Coverage reports show which declared operations
and supported contract conditions your tests exercised.

```text
Existing tests → Wiretap → Your application → Wiretap → Existing mocks or services
                   └──────────────┬──────────────┘
                            Capture artifacts
                     JSON · contracts · payloads
```

Keep your test framework, clients, assertions and mocks. Start Wiretap alongside
your application, route traffic through it, and call its JavaScript SDK, Go SDK
or HTTP API from test lifecycle hooks.

## Try it

From this checkout, with Go 1.24+, Node.js 20.12+, Bash and curl available:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The example prints its artifact directory. Read `bundle.json` and the referenced
JSON test recordings. Enable `outputs: {coverage: [json], postman: true}` in your
configuration to produce coverage and Postman collections during suite finalization.
To export an existing archive offline:

```bash
wiretap export --bundle ./artifacts/<run-id> --format all
```

[More examples](examples/README.md) cover parallel Jest tests, database fixtures,
Microsvc Transactions with authenticated tests, and
[native gRPC with a mock dependency](examples/grpc-boundaries/README.md).

## Add Wiretap to your tests

1. Put application/dependency URLs and OpenAPI or Protobuf contracts in `.wiretap.yaml`.
2. Start Wiretap alongside your application and mocks using your existing harness.
3. Point test requests and the application's dependency endpoints at Wiretap.
4. Connect suite/test setup and cleanup through an SDK or the HTTP API.
5. Run your tests normally and keep the complete artifact directory.

Follow the [existing-tests guide](docs/getting-started-existing-tests.md), or the
[JavaScript](sdk/js/README.md), [Go](sdk/go/README.md) and
[HTTP API](docs/runtime-api.md) references.

HTTP requires an OpenAPI spec by default; individual APIs can explicitly opt out.
gRPC requires a Protobuf contract: reflection is the default, with local `.proto`
files or descriptor sets as alternatives. Unary, server-streaming,
client-streaming and bidirectional calls support capture and structural coverage.
See the [gRPC guide](docs/grpc-user-guide.md).

Parallel tests sharing one application need W3C trace propagation from its server
to its outbound clients. Otherwise run sequentially or isolate workers. See
[configuration](docs/configuration.md) and [tracing](docs/otel-adoption.md).

## Read and export results

| Artifact or report | Contents |
| --- | --- |
| `bundle.json` | Run identity, configured/observed APIs and dependencies, frozen contracts and test references. |
| `tests/*.json` | Per-attempt requests/responses, test outcomes, validation and capture diagnostics. |
| `contracts/`, `payloads/` | Frozen contracts and retained, redacted payload bytes with integrity hashes. |
| JSON/YAML coverage | Operation coverage, supported contract conditions, observations, combinations and explicit gaps/limits. |
| Postman collections | Captured HTTP and gRPC interactions grouped by test, for the SUT and mocked dependencies. |

One test can cover many contract conditions. A mock response records a test
assumption; it does not prove real-provider agreement. Missing, unsupported or
incomplete evidence remains visible. Cross-repository comparison is not yet
implemented. Wiretap does not generate or execute additional tests.

```bash
wiretap export --bundle ./artifacts/<run-id> --format yaml --output coverage.yaml
wiretap export --bundle ./artifacts/<run-id> --format postman --output-dir postman
```

Postman HTTP collections use v2.1 JSON; native gRPC collections use v3 JSON with
saved unary/streaming conversations, metadata, trailers and RPC status. Real
dependencies are skipped. See the [artifact and export guide](docs/artifacts.md).

The [format reference](docs/bundle-evidence.md) documents the raw capture format,
including generic event storage for future transports. Live capture, validation
and coverage currently support HTTP and native gRPC. Runtime configuration
remains centred on a SUT.

## Documentation

Start with the [documentation index](docs/README.md) for setup, SDKs, commands,
formats, CI and troubleshooting. The [landing page guide](site/README.md) covers
the website preview and GitHub Pages setup.
