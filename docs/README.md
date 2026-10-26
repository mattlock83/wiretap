# Wiretap documentation

Wiretap captures HTTP API boundaries during existing Jest tests, validates supplied OpenAPI contracts and writes portable run archives. Arazzo workflows carry the supported JSON/REST scenarios; coverage reports measure the evidence. Offline assumption checks compare captured mocks with provider test examples.

Start with [Getting started](getting-started-existing-tests.md) or the [Express/Jest demo](../examples/existing-jest/README.md). Use synthetic data and dummy credentials: retained traffic is unchanged.

## Set up Jest capture

| Guide | Purpose |
| --- | --- |
| [Getting started](getting-started-existing-tests.md) | Install, configure Jest, capture and check evidence. |
| [Jest SDK](../sdk/js/README.md) | Configure the supported adapter. |
| [Jest details](../sdk/js/docs/jest.md) | Handler hosting, existing APIs, fixtures, retries and concurrency. |
| [Configuration](configuration.md) | Project fields, environment settings and CLI commands. |
| [Tracing](otel-adoption.md) | Correlate tests through application dependencies. |
| [HTTPS trust](https-trust.md) | Trust the capture proxy certificate. |
| [Runtime API](runtime-api.md) | Inspect the underlying lifecycle endpoints. |
| [CI example](ci-example.md) | Preserve complete evidence on success and failure. |
| [Troubleshooting](troubleshooting.md) | Resolve routing, schema, export and check failures. |

The main HTTP API requires OpenAPI by default. Dependency contracts are optional; supply one for validation and coverage. A later assumption check can use the provider's frozen contract, but still needs a matching complete passing provider test. See [schema defaults](configuration.md#schema-defaults-by-boundary).

## Read, export and compare

| Guide | Purpose |
| --- | --- |
| [Artifacts and exports](artifacts.md) | Read raw archives, coverage and Arazzo workflows. |
| [Offline HTML dashboard](artifacts.md#offline-html-dashboard) | Browse coverage by API and test, or rebuild from JSON, specs and Arazzo. |
| [Assumption checks](assumption-checks.md) | Compare consumer/provider examples and exercised rules. |
| [Shared snapshots](assumption-checks.md#share-provider-snapshots) | Map providers and select unambiguous snapshots. |
| [Contract coverage rules](contract-coverage-rules.md) | Understand condition keys, counts and limits. |
| [Bundle format](bundle-evidence.md) | Parse inventories, attempts, contracts and payloads. |
| [Machine interface](machine-interface.md) | Consume versioned evidence and reports. |
| [Run sets](run-sets.md) | Pin completed shards from one build. |
| [Archive loading](archive-loading.md) | Configure bounded reads and integrity checks. |
| [Architecture](architecture.md) | Understand runtime and Jest responsibilities. |
| [Support](support.md) | Supported integration and format policies. |
| [Local builds and installation](local-release.md) | Prepare, verify and install binaries and the Jest package. |
| [Dependency maintenance](dependency-security.md) | Audit the runtime and Jest package. |
| [Roadmap](roadmap.md) | Planned work. |

A run contains test attempts. Each attempt contains observed exchanges. A contract condition is a declared case such as an enum value or an omitted optional field; one exchange can cover many conditions. Coverage does not establish consumer/provider compatibility on its own. See [check limits](assumption-checks.md#what-the-check-will-not-establish).
