# Getting started with Wiretap

Wiretap turns your existing HTTP and native gRPC tests into **recorded API
evidence, Postman collections, JSON/REST Arazzo workflows and API coverage**. It
also compares the assumptions in your mocks with the provider's own test evidence. Keep your test framework,
clients, assertions and mock server; add capture around the API calls they
already make.

This guide follows an Orders service that calls an Inventory mock during its
tests. The Inventory team captures tests against its real service. Both teams
share their artifacts, then check whether the mock agrees with what the
Inventory tests actually demonstrate.

```text
Orders tests → Wiretap → Orders service → Wiretap → Inventory mock
Inventory tests → Wiretap → Inventory service

Each suite produces a complete run archive and selected projection exports.
wiretap check compares those saved artifacts offline.
```

Use isolated test environments with synthetic data and dummy credentials.
Wiretap retains captured traffic unchanged. Your framework owns test outcomes;
Wiretap records contract validation separately, so a negative test can pass
while retaining evidence of its deliberately invalid request.

- [Install the runtime and SDK](#1-install-the-runtime-and-sdk)
- [Configure your application and mocks](#2-configure-your-application-and-mocks)
- [Connect your existing test suite](#3-connect-your-existing-test-suite)
- [Read the artifacts](#4-read-the-artifacts)
- [Share complete runs by application](#5-share-complete-runs-by-application)
- [Compare consumer and provider evidence](#6-compare-consumer-and-provider-evidence)
- [Add the workflow to CI](#7-add-the-workflow-to-ci)

## 1. Install the runtime and SDK

If you already have prepared binary archives and an SDK tarball, follow
[local release installation](install-local-release.md), then continue with
configuration below. To prepare them yourself, use the checkout instructions here.

Build the executable from the Wiretap checkout with Go 1.26+:

```sh
go build -mod=readonly -o ./dist/wiretap ./cmd/wiretap
```

Put that executable on `PATH` so tests and the later CLI commands can find it.
Alternatively, set `WIRETAP_BINARY` to its absolute path for the SDK and invoke
the executable by that path for CLI commands. On Windows, build
`./dist/wiretap.exe`. The SDK does not download or build the executable.

For JavaScript, use Node.js 22+ and install the locally packed SDK alongside your
existing runner. From the Wiretap checkout:

```sh
npm pack ./sdk/js --pack-destination ./dist
```

Then, from your test project:

```sh
npm install --save-dev /absolute/path/to/wiretap/dist/wiretap-sdk-0.1.0.tgz
```

For Go, import `github.com/mattlock83/wiretap/sdk/go` as `wiretap`. To use the
checkout from another Go module, add a local replacement and run `go mod tidy`
after adding the SDK imports:

```sh
go mod edit -replace=github.com/mattlock83/wiretap=/absolute/path/to/wiretap
go mod tidy
```

For Python 3.9+, install the local SDK with the optional pytest integration into
your test project's virtual environment:

```sh
python -m pip install "/absolute/path/to/wiretap/sdk/python[pytest]"
```

The [Python SDK guide](../sdk/python/README.md) covers fixture setup and explicit
client routing. The SDK uses the same local `wiretap` executable.

See [local installation](local-release.md) for versioned binary archives and
package checks, and the [JavaScript SDK](../sdk/js/README.md) for supported runner
versions. These installation steps require no package publication.

To try a complete suite first, choose the [Go example](../examples/go-suite/README.md),
[existing Express/Jest suite](../examples/existing-jest/README.md),
[Playwright Todo API](../examples/playwright-runner/README.md),
or [pytest suite](../examples/pytest-suite/README.md). The
[consumer/provider example](../examples/consumer-provider/README.md) demonstrates
the complete capture, passing check, provider breakage and repair workflow.

## 2. Configure your application and mocks

Put `.wiretap.yaml` in the Orders test project:

```yaml
version: 1
application: {id: orders-service, name: Orders}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text], postman: true}
sut:
  id: orders-http
  url: http://127.0.0.1:8080
  kind: service
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    url: http://127.0.0.1:9090
    kind: mock
provider_mappings:
  inventory:
    application_id: inventory-service
    api_id: inventory-http
```

Replace the addresses and schema path with your existing fixtures. The SUT is
the system under test. Paths are relative to this configuration file. Your
harness starts the application and mock server; these URLs are the real targets
to which Wiretap forwards captured requests.

| Setting | Meaning in this example |
| --- | --- |
| `application.id: orders-service` | Stable application identity; also its top-level artifact folder. |
| `sut.id: orders-http` | API exposed by Orders. |
| `dependencies.inventory` | Local name of the mocked Inventory boundary. |
| `provider_mappings.inventory` | Connects that mock to application `inventory-service`, API `inventory-http`. |

Choose application IDs that fit a single folder name and keep them stable across
runs. Mappings are saved during capture; folder names alone cannot establish
which provider a mock represents. For inbound-only capture, omit `dependencies`
and `provider_mappings`.

The main HTTP API requires its OpenAPI spec by default; use OpenAPI 3.0 for
assumption checking. HTTP dependency specs are optional: without one, calls and
Postman examples are retained, while contract coverage is unknown. The later
comparison can use the provider's frozen spec. Add a dependency `schema` when
you also want local validation, coverage and comparison of independently
declared rules. A supplied invalid spec fails setup.

Native gRPC requires Protobuf descriptors from reflection, `.proto` files or a
descriptor set. Follow the [gRPC guide](grpc-user-guide.md) for client and contract
setup; the capture/share/check workflow is the same.

## 3. Connect your existing test suite

Choose one integration. Managed adapters start Wiretap on available ports,
record test outcomes, finalize exports and stop it around your usual command.

| Runner | Integration changes |
| --- | --- |
| [Jest](../sdk/js/docs/jest.md) | Wrap the config with `withWiretap`; point the shared client at `WIRETAP_SUT_URL`. |
| [Playwright Test](../sdk/js/docs/playwright.md) | Wrap the config and use the supplied `test` fixture; keep relative API requests and existing server fixtures. |
| [Mocha](../sdk/js/docs/mocha.md) | Wrap the config; point the shared client at `WIRETAP_SUT_URL`. |
| [Python / pytest](../sdk/python/README.md) | Use the `wiretap` fixture or stdlib context managers with explicit capture URLs and trace headers. |
| [Plain JavaScript / Node](../sdk/js/docs/javascript.md) | Use the lifecycle API and a project-owned parent harness; the [complete Node example](../examples/node-suite/README.md) handles startup, tracing and multi-file cleanup. |
| [Go `testing`](../sdk/go/README.md) | Add `wiretap.Run(m)` in `TestMain` and `wiretap.StartTest(t)` in each test or shared setup helper. |

### Example: an existing Jest suite

With the already hosted application configured above, wrap your configuration:

```js
// jest.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');

module.exports = withWiretap({
  // Keep your existing testMatch, transforms, reporters and setup files here.
});
```

Change the target in your shared client. For example, with Supertest:

```js
const request = require('supertest');
const api = request(process.env.WIRETAP_SUT_URL);
```

Keep the requests and assertions against `api`, and run your normal Jest command.
If your project exports an Express/Node handler instead of hosting a server,
omit `sut.url` and pass `{ app: '<rootDir>/app.js' }` as the second argument to
`withWiretap`. The [Jest guide](../sdk/js/docs/jest.md) covers hosting, existing
mocks and dynamic fixtures.

### Example: standard Go tests

For the already hosted application, add or adapt `TestMain`:

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

Start capture in each test or shared setup helper before your own cleanup hooks:

```go
session := wiretap.StartTest(t)
client := session.HTTPClient(existingClient)
// Use client for existing requests and session.SUTURL as their API base URL.
```

Run the selected integration-test package with `go test -count=1` for fresh
artifacts. The [Go guide](../sdk/go/README.md) also shows how Wiretap can host your
`http.Handler`. Each package creates its own archive; `go test -count=1 ./...`
does not combine them. Start with one integration-test package for the workflow
below. Use explicit [run sets](run-sets.md) to group packages from the same build.

### Route downstream calls too

Declaring a mock URL in YAML does not redirect the application's client.
Configure its Inventory client to call the dependency capture URL:

| Integration | URL to give the application's Inventory client |
| --- | --- |
| JavaScript runner session | `session.dependencyUrls.inventory` |
| Plain JavaScript capture handle | `capture.dependencyUrl('inventory')` |
| Go session | `session.Dependencies["inventory"]` |
| Python attempt | `attempt.dependencies["inventory"]` |

Apply that URL through the runner's session setup hook or fixture before the
test makes requests; Jest provides an
[`onSession` hook](../sdk/js/docs/jest.md#dynamic-fixtures-and-selective-capture).
Wiretap forwards calls to the configured mock. Your mock tool owns its stubs
and assertions.

The session hook can change an existing client only if your application or
fixture supports reconfiguration. If it reads dependency URLs once at process
startup, have your harness obtain the capture URLs before launching the
application. Use the [custom lifecycle guide](../sdk/js/docs/javascript.md)
or [advanced Go integration](../sdk/go/docs/advanced.md) for that ownership.

Capture needs a network boundary. Requests sent directly to the original server,
handler calls through `httptest.NewRecorder`, and mocks intercepted entirely
inside the process do not pass through Wiretap. Use an HTTP/gRPC mock server for
downstream assumptions you want to compare.

Begin with sequential tests. For parallel tests sharing one application, enable
explicit sessions and propagate W3C trace context through its server and outbound
clients; see [tracing and concurrency](otel-adoption.md). Await requests and
expected dependency work before the attempt ends, and keep fixtures available
through capture cleanup. Managed adapters own finalization. Python callers pass
the attempt's `headers` explicitly; JavaScript and Go provide the tracing
helpers described in their SDK guides. A custom harness owns both steps.

## 4. Read the artifacts

Use the exact run path returned by finalization or recorded in `last-run.json`.
Managed Go, JavaScript and Python owners put the canonical receipt and bounded
`wiretap.log` inside each run archive. JavaScript also retains an advisory
root-level latest receipt for compatibility; it can race between concurrent suites. Forced termination may leave no new receipt or completed exports, so
check the current command's outcome before using those paths.

A normally finalized run contains these artifacts, with contract and payload
directories present when needed:

| Artifact | What it contains | What to do with it |
| --- | --- | --- |
| `bundle.json` | Run identity, application/API inventory, mappings and captured-file references. | Use its containing directory as input to reports and checks. |
| `tests/` | Per-attempt outcomes, requests, responses and validation evidence. | Follow test/flow references here when investigating a gap or mismatch. |
| `contracts/` | Frozen API contracts and their referenced files. | Keep alongside the run so later checks use the tested contract. |
| `payloads/` | Retained bodies and protocol data stored outside attempt JSON. | Preserve paths and bytes. |
| `exports/coverage.txt` | Compact coverage summary. | Read first for outcomes, exercised operations, gaps and unknowns. |
| `exports/coverage.json` | Operation and contract-condition coverage. | Feed reporting or coverage gates; this measures API coverage, not source-code coverage. |
| `exports/postman/` | HTTP/gRPC collections with captured examples for the provided API and mocked dependencies. | Inspect in Postman; retain originals for assumption checks. |
| `exports/arazzo/` | JSON/REST Arazzo workflows and OpenAPI source companions when enabled. | Keep together; use with `check --input-format arazzo`. |
| `exports/result.json` | Exact export paths and any export errors. | Confirm exports completed and preserve errors. |

API coverage can show whether `GET /orders/{id}` was exercised and which declared
response or enum conditions were observed. It does not show whether an Inventory
mock agrees with Inventory's tests; step 6 answers that question. Unknown
coverage is not a pass.

Regenerate a summary offline using the exact run directory (`run-123` here
stands for the actual recorded directory):

```sh
wiretap report --bundle ./artifacts/wiretap/run-123 --format text
```

To regenerate standard coverage, Postman and eligible Arazzo exports from an intact archive:

```sh
wiretap export --bundle ./artifacts/wiretap/run-123 --format all
```

Keep complete runs on success **and failure**. Failed attempts remain useful
diagnostics, although they cannot establish compatible provider evidence.
Postman collections, Arazzo workflows or coverage reports alone are insufficient
for comparison. Edit copies when exploring requests and preserve the original exports
with the archive. See [artifacts and exports](artifacts.md) for formats, gRPC
import setup and limits.

## 5. Share complete runs by application

The Inventory team adds Wiretap to its tests using the same integration steps.
Its configuration must identify the provider named by Orders:

```yaml
version: 1
application: {id: inventory-service, name: Inventory}
output_dir: artifacts/wiretap
outputs: {coverage: [json, text], postman: true}
sut:
  id: inventory-http
  url: http://127.0.0.1:8081
  kind: service
  schema: schemas/inventory.openapi.yaml
```

Inventory tests must exercise the real service boundary and record complete
passing exchanges. Coordinate synthetic fixtures: if Orders mocks
`GET /stock/widget-1` returning `{"quantity":3}`, Inventory needs a passing test
covering that concrete request and response. A schema that permits the response
is not sufficient by itself.

Upload each **complete run directory**, including `exports/postman/` or the
selected `exports/arazzo/` tree, to your existing artifact store, such as Artifactory. A useful remote layout is
`<application-id>/<api-id>/<tested-revision>/<run-id>/`. Record the tested build
in `sut_source`; see [shared provider snapshots](assumption-checks.md#share-provider-snapshots)
for version pins and CI provenance.

Before checking, download and extract the selected runs locally. The top-level
folder must match `application.id`; folders below it can organize APIs, builds
and runs. For this example:

```text
api-evidence/
  orders-service/
    orders-run/
      bundle.json
      tests/
      contracts/
      payloads/
      exports/postman/
  inventory-service/
    inventory-run/
      bundle.json
      tests/
      contracts/
      payloads/
      exports/postman/
```

Copy whole archives, preserving their internal files and paths. The descriptive
`orders-run` and `inventory-run` folders above hold your selected runs; their
recorded run identities stay in `bundle.json`. For Arazzo inputs, retain
`exports/arazzo/` instead of `exports/postman/` in each run.

Your remote store can keep history. Prepare an unambiguous local set for each
check: start with one intended run per application/API. Wiretap rejects multiple
matching runs instead of choosing the newest or pooling their evidence. Optional
mapped provider versions narrow selection but must match `sut_source.version`.
Keep the local set stable while checking. Wiretap discovers local extracted
artifacts; your existing tooling handles remote upload and download.

## 6. Compare consumer and provider evidence

**The comparison commands are `wiretap check consumer` and
`wiretap check provider`.** They read the saved archives, frozen contracts and
matching projection exports offline. Postman is the default; JSON/REST projects
can select Arazzo on both sides. No application server or desktop runner session
is needed, and the check does not replay requests, execute scripts or evaluate
Arazzo expressions.

From Orders, check all mock dependencies against their mapped providers:

```sh
wiretap check consumer \
  --consumer ./api-evidence/orders-service/orders-run \
  --artifacts ./api-evidence
```

From Inventory, check whether its tests support the mocks used by mapped
consumers present in the artifact set:

```sh
wiretap check provider \
  --provider ./api-evidence/inventory-service/inventory-run \
  --artifacts ./api-evidence
```

Both enforce the same relationship: every selected mock example needs a matching
complete passing provider exchange. Consumer mode finds providers using recorded
mappings; provider mode finds consumers whose mappings name its API. Consumer
mode requires a mapping and provider for every selected mock; provider mode
requires at least one mapped consumer. Ambiguous snapshots and empty comparisons
fail. Consumers absent from your artifact set cannot be discovered.

For a JSON/REST workflow using Arazzo instead of Postman, select
`outputs: {coverage: [json, text], arazzo: true}` in both projects, retain each
run's `exports/arazzo/` directory, and add `--input-format arazzo`:

```sh
wiretap check consumer \
  --consumer ./api-evidence/orders-service/orders-run \
  --artifacts ./api-evidence --input-format arazzo
```

No Postman export is required for that mode. Complete archives and all Arazzo
source companions remain required. Explicit pairs and same-build run sets also
accept `--input-format arazzo`; see [Arazzo inputs](assumption-checks.md#use-arazzo-instead-of-postman-for-jsonrest)
for path overrides. Mixed formats are not supported, and a selected boundary
with unsupported traffic cannot pass by exporting only its JSON/REST subset.

The check verifies each run's own projection exports against raw evidence and
compares examples and supported contract rules. Without a consumer HTTP
dependency spec, it validates both exchanges against the provider's frozen
OpenAPI 3.0 contract. Supplying both specs also checks exercised declarations
for drift. Native gRPC checks selected Protobuf declarations and observed wire
compatibility.

| Result | Example or action |
| --- | --- |
| Verified | A passing provider exchange supports the mock request and response, with the reported contract assurance. |
| Uncovered | Orders calls `/stock/widget-2`, but provider tests only captured `/stock/widget-1`. Align fixtures or add provider coverage. |
| Mismatch | The mock returns a string quantity while the provider returns a number, or a checked value violates an enum or length constraint. |
| Unverified | Evidence is incomplete, a required contract cannot be checked, or a provider version cannot be established. Resolve the blocker. |
| Input error | Selected archives or projection exports are missing, invalid, stale or ambiguous. Restore the correct documents and source companions. |

Agreement uses concrete requests and responses after documented normalization.
Coordinate fixture IDs, payloads and significant headers. The check cannot infer
which response fields a consumer reads, prove behavior for untested requests,
or infer unobserved enum/length rules from samples. Requests paired with HTTP
error responses skip request-value validation to allow negative tests;
contract-backed responses are still validated. The [checking guide](assumption-checks.md)
details findings and limitations.

Strict comparison can flag an added response field or, when both specs are
supplied, a relaxed rule that a running consumer would tolerate. Investigate a
mismatch before treating it as a proven runtime break.

Add `--dependency inventory` to scope the consumer command to one mock. For a
selected pair, omit `--artifacts` and supply both paths:

```sh
wiretap check consumer \
  --consumer ./api-evidence/orders-service/orders-run \
  --provider ./api-evidence/inventory-service/inventory-run \
  --dependency inventory
```

Comparison leaves the original coverage report unchanged. Capture coverage and
cross-application agreement answer different questions.

## 7. Add the workflow to CI

Run the usual test command, preserve its exit status, and upload complete outputs
even when tests fail. In the comparison job, restore the intended peer snapshots
and save the check report **outside** the artifact root:

```sh
mkdir -p check-results
wiretap check consumer \
  --consumer ./api-evidence/orders-service/orders-run \
  --artifacts ./api-evidence \
  --format json \
  --output ./check-results/orders-check.json
```

The shared-artifact JSON report has an overall `compatible` verdict, aggregate
counts and one pair report in `comparisons` for each selected
consumer/mock/provider relationship. Pair reports identify source manifests and
the tests and flows behind matches or findings.

| Exit status | Meaning |
| --- | --- |
| `0` | All selected comparisons passed. |
| `1` | A comparison failed or could not be verified; operational failures can also return `1`. |
| `2` | Invalid inputs or flags. |

Keep suite and check results as separate gates. Preserve the check exit status
and stderr alongside its report and inputs. Input errors produce no new JSON
report; use a fresh result path so an older file cannot be mistaken for the
current verdict.

The [CI recipe](ci-example.md#gate-consumerprovider-assumptions-offline) provides
unconditional uploads and failure handling. Use the
[machine interface](machine-interface.md#assumption-check-results) to consume
results programmatically, and [troubleshooting](troubleshooting.md#assumption-checks)
when a comparison fails.
