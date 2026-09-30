# Wiretap

Wiretap turns existing HTTP API tests into a portable record of your system's
APIs and the assumptions your tests make about its dependencies. It captures requests,
responses and their sequence, checks them against OpenAPI specs, and writes an
OKF bundle for people and agents to read.

```text
Existing tests → Wiretap → Your application → Wiretap → Existing mocks or services
                   └──────────────┬──────────────┘
                              OKF bundle
                   APIs · assumptions · tests · coverage
```

Keep your test framework, HTTP client, assertions and mocks. Start Wiretap as
another service in your test environment, route traffic through it, and call
its JavaScript SDK, Go SDK or HTTP API from test lifecycle hooks.

## Try it

From this checkout, with Go 1.24+, Node.js 20.12+, Bash and curl available:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The example captures an existing test, then uses `wiretap explore` to generate
invalid-input checks from its happy path. It prints two bundle locations. Open
`viewer/index.html` for the offline dashboard or `index.md` for the linked
Markdown pages. No report server is needed.

[More examples](examples/README.md) cover parallel Jest tests, database fixtures,
and the Speedscale Microsvc Transactions service with authentication hooks.

## Add Wiretap to your tests

1. Put the application URL, dependency URLs and OpenAPI specs in `.wiretap.yaml`.
2. Start Wiretap alongside your application and mocks using your existing harness.
3. Point test requests and the application's dependency endpoints at Wiretap.
4. Connect suite/test setup and cleanup through an SDK or the HTTP API.
5. Run your tests normally and keep the generated bundle as a build artifact.

Follow the [existing-tests guide](docs/getting-started-existing-tests.md) for a
complete setup, or go directly to the [JavaScript](sdk/js/README.md),
[Go](sdk/go/README.md) or [HTTP API](docs/runtime-api.md) reference.

OpenAPI specs are required by default; individual APIs can explicitly opt out.
Parallel tests sharing one application need working W3C trace propagation from
its server to its outbound clients. Otherwise run sequentially or isolate
workers. The application does not need to propagate a Wiretap-specific ID.
See [configuration](docs/configuration.md) and [tracing](docs/otel-adoption.md).

## Understand what has been tested

| Bundle view | What it tells you |
| --- | --- |
| APIs and dependencies | Which boundaries and operations were observed, with requests, responses and inferred OpenAPI drafts for mocks. |
| Contract coverage | Which specified cases were exercised, such as enum values, optional fields and boundaries; gaps and uncertain results remain visible. |
| Test runs | What executed and what happened, with authored tests separate from generated checks and links to the conditions they exercised. |
| System overview and verification work | How the recorded APIs fit together and which assumptions, mismatches or gaps need further checks. |
| `knowledge.json` | The API map, verification tasks and evidence references for a knowledge base or agent. |

One test can cover many contract conditions. A mock response records a test
assumption; it does not prove the real provider behaves that way. Cross-repository
comparison is not yet implemented.

The bundle contains authoritative JSON evidence, frozen schemas and retained
payloads. Its Markdown, HTML and knowledge views are generated automatically
and can be rebuilt with `wiretap report`. See the [bundle guide](docs/okf-bundles.md)
for reading, sharing and exporting results.

## Explore invalid inputs

The optional `wiretap explore` runner takes a captured happy-path provider test,
checks a fresh baseline, then generates bounded invalid requests. Project-owned
hooks provide authentication, fixtures, cleanup and additional assertions.

```bash
wiretap explore --bundle ./artifacts/<source-run> \
  --test "creates an order" --config .wiretap.yaml
```

It writes a new bundle with rejection results and coverage. Authentication
failures and server errors do not count as successful validation checks. Hooks
stay in your project; the source bundle remains unchanged. See
[provider exploration](docs/exploration.md) for setup and supported mutations.

## Documentation

Start with the [documentation index](docs/README.md) for setup, SDKs, commands,
bundle formats, CI and troubleshooting.
