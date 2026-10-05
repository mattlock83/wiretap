# Wiretap

Wiretap turns existing HTTP and gRPC API tests into a portable record of your system's
APIs and the assumptions your tests make about its dependencies. It captures requests,
responses and their sequence, checks them against OpenAPI or Protobuf contracts, and writes an
OKF bundle for people and agents to read.

```text
Existing tests → Wiretap → Your application → Wiretap → Existing mocks or services
                   └──────────────┬──────────────┘
                              OKF bundle
                   APIs · assumptions · tests · coverage
```

Keep your test framework, HTTP or gRPC client, assertions and mocks. Start Wiretap as
another service in your test environment, route traffic through it, and call
its JavaScript SDK, Go SDK or HTTP API from test lifecycle hooks.

## Try it

From this checkout, with Go 1.24+, Node.js 20.12+, Bash and curl available:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The example captures an existing test, then calls `wiretap.explore()` inside that
test to generate invalid-input checks from its happy path. It prints one bundle
containing both authored and generated checks. Open
`docs/viewer.html` for the offline dashboard or `index.md` for the linked
Markdown pages. No report server is needed.

[More examples](examples/README.md) cover parallel Jest tests, database fixtures,
the Speedscale Microsvc Transactions service with authentication hooks, and
[native gRPC with a mock dependency](examples/grpc-boundaries/README.md).

## Add Wiretap to your tests

1. Put the application URL, dependency URLs and OpenAPI or Protobuf contracts in `.wiretap.yaml`.
2. Start Wiretap alongside your application and mocks using your existing harness.
3. Point test requests and the application's dependency endpoints at Wiretap.
4. Connect suite/test setup and cleanup through an SDK or the HTTP API.
5. Run your tests normally and keep the generated bundle as a build artifact.

Follow the [existing-tests guide](docs/getting-started-existing-tests.md) for a
complete setup, or go directly to the [JavaScript](sdk/js/README.md),
[Go](sdk/go/README.md) or [HTTP API](docs/runtime-api.md) reference.

OpenAPI specs are required by default for HTTP; individual HTTP APIs can explicitly
opt out. gRPC requires a Protobuf contract: reflection is the default source,
with local `.proto` files or a descriptor set as alternatives. See the
[gRPC guide](docs/grpc-user-guide.md). Unary and all three streaming shapes support capture and structural coverage.
Native gRPC exploration generates invalid-input checks from supported standard
Protovalidate annotations, including recorded streaming requests. See the
[gRPC exploration example](examples/grpc-exploration/README.md).
Parallel tests sharing one application need working W3C trace propagation from
its server to its outbound clients. Otherwise run sequentially or isolate
workers. The application does not need to propagate a Wiretap-specific ID.
See [configuration](docs/configuration.md) and [tracing](docs/otel-adoption.md).

## Understand what has been tested

| Bundle view | What it tells you |
| --- | --- |
| `bundle.json` | The provided APIs and dependencies, their contracts and the recorded tests that exercised them. |
| APIs and dependencies | Which boundaries and operations were observed, with requests, responses and inferred OpenAPI drafts for HTTP mocks. |
| Contract coverage | Which specified cases were exercised, such as enum values, optional fields and boundaries; gaps and uncertain results remain visible. |
| Test runs | What executed and what happened, with authored tests separate from generated checks and links to the conditions they exercised. |
| System overview and verification work | How the recorded APIs fit together and which assumptions, mismatches or gaps need further checks. |
| `docs/knowledge.json` | The API map, verification tasks and evidence references for a knowledge base or agent. |

One test can cover many contract conditions. A mock response records a test
assumption; it does not prove the real provider behaves that way. Cross-repository
comparison is not yet implemented.

The [bundle format](docs/bundle-evidence.md) keeps `bundle.json`, `contracts/`,
`tests/` and `payloads/` as the source data, with an OKF `index.md` and rebuildable
views under `docs/`. Recorded tests are also the input to exploration; hooks and
environment setup remain in your project.

The bundle format supports consumer-only systems such as UIs, provider-only
systems without mocks, and mixed systems. Application identity is independent of
the provided/dependency inventories. Its event representation can retain
bidirectional gRPC streams, publish/consume messaging, Kafka/Avro and GraphQL
subscriptions, alongside frozen AsyncAPI or other contract packages. Kafka/Avro, AsyncAPI and GraphQL remain format capabilities. Live capture,
validation and coverage adapters currently
support HTTP and native gRPC, including bidirectional streams. The existing runtime configuration remains centred
on a SUT. See the [format's support boundaries](docs/bundle-evidence.md).

The bundle contains authoritative JSON evidence, frozen schemas and retained
payloads. Its Markdown, HTML and knowledge views are generated automatically
and can be rebuilt with `wiretap report`. See the [bundle guide](docs/okf-bundles.md)
for reading, sharing and exporting results.

## Explore invalid inputs

Call exploration from the test that has just exercised a happy path:

```javascript
await wiretap.explore({
  operations: ["POST /orders"],
  hooks: {
    beforeCase: async () => resetFixtures(),
    checkCase: async event => checkDatabaseEffects(event),
    afterCase: async () => cleanUpFixtures()
  }
});
```

The SDK calls Wiretap's HTTP API and invokes these callbacks in your test process.
Wiretap checks a fresh baseline, then generates supported invalid requests from
the captured traffic and OpenAPI contract. The call fails your ordinary test if
any generated check fails. Authentication failures and server errors never count
as successful validation checks. All cases contribute evidence to the same run
bundle; fixtures, credentials and callback code stay in your project. See
[exploration and hooks](docs/exploration.md) for setup and supported mutations.

## Documentation

Start with the [documentation index](docs/README.md) for setup, SDKs, commands,
bundle formats, CI and troubleshooting.

The [landing page guide](site/README.md) covers previewing and publishing the
Wiretap website with GitHub Pages.
