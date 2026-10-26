# Capture artifacts and exports

Wiretap writes one portable directory per run under `./artifacts` by default.
Set `output_dir` in `.wiretap.yaml` or `WIRETAP_OUTPUT_DIR` to choose another
parent directory. SDK/API responses return the exact `bundle_path`.

Each test teardown commits an immutable JSON recording. Managed SDK adapters
finalize the run and configured exports after test cleanup, then stop their
Wiretap process. With a project-owned runtime, one suite owner calls
`/v1/finishRun` after every worker finishes. Keep the complete directory when sharing it or
uploading it to CI so its contracts and payload references remain valid. Managed
runs also write `last-run.json` with exact archive/export paths and a bounded
`wiretap.log` inside each run directory. This isolates receipts for concurrent
Go packages, JavaScript owners and Python workers. JavaScript also writes an
advisory latest receipt in the output parent; use the archive-local receipt
when selecting a particular run. Preserve the complete run directory.

After successful finalization, the Go, Jest, Mocha, Playwright and pytest runner
adapters print the archive and exact configured coverage, Postman and Arazzo paths from the
completion receipt. JavaScript and Go use stderr; pytest uses its terminal
reporter, including summaries from parallel workers. Low-level SDK calls return
their receipts without printing these summaries. Failed tests keep their original
outcome even when capture and export finish successfully.

```text
artifacts/<run-id>/
  bundle.json             run identity, API/dependency inventory and test references
  contracts/              frozen contracts, imports and integrity manifests
  tests/                  immutable JSON recording per test attempt
  payloads/               retained body bytes
```

Read `bundle.json` for `provided_apis`, `dependencies`, `tests`, pending attempts,
run state and source provenance. Follow each test's `path` to its recording.
Configured but uncalled boundaries remain in the inventory. Each recording
contains the test outcome, traffic, validation results, diagnostics and references
to retained payloads. See the [format reference](bundle-evidence.md) for integrity
rules and protocol-specific fields.

Application identity is separate from API identity. The format can represent
provider-only, consumer-only and mixed systems, while the current runtime setup
remains centred on a SUT. Empty inventories describe configured and observed
scope; they do not prove that no other APIs or dependencies exist.

## Verify consumer/provider examples

Use `wiretap check consumer` or `wiretap check provider` with a local run and
`--artifacts <root>` to discover mapped peers beneath application folders. You
can also select an explicit pair with `--consumer` and `--provider`, or explicit
[same-build run sets](run-sets.md) with `--consumer-set` and `--provider-set`. Every run
needs its complete archive and Postman exports. The checker verifies the collections
against their raw evidence, then requires each selected mock request and paired
response to have compatible evidence from a passing provider service test.
The provider's frozen OpenAPI 3.0 contract can validate both concrete exchanges
without a consumer dependency spec; that match is marked
`openapi_3_0_provider_contract`. With both contracts present, exercised rules
must also agree and each exchange validates under its own schema. Native gRPC
also compares the selected method's reachable message/enum declarations and
cross-decodes observed wire messages. With both HTTP schemas absent or omitted,
the report marks a verified match as example-only. Uncovered scenarios fail the
check. See [assumption checks](assumption-checks.md) for command examples,
dependency selection, JSON results and limits.
Provider-contract validation does not rewrite the consumer archive's original
unknown contract coverage. A matching passing provider test is still required.

## Generate coverage

`report` and `export` read and verify captured evidence, frozen contracts and
retained payloads. They work offline without the original workspace or servers.
Both default to JSON coverage. Start with the compact text summary, and use
JSON/YAML for machine processing:

```bash
wiretap report --bundle ./artifacts/<run-id> --format text
wiretap report --bundle ./artifacts/<run-id> --output coverage.json
wiretap export --bundle ./artifacts/<run-id> --format yaml --output coverage.yaml
```

Omit `--output` to write to stdout. The report includes operation coverage,
validation diagnostics and detailed condition results under `contract_coverage`.
These reports are derived from the raw artifact and never become evidence inputs.
`POST /v1/report` exposes the same JSON coverage calculation through the runtime.

Text limits contract, operation and condition detail lists and names the omitted
counts. JSON/YAML retain the complete report; truncation never changes totals.

Save output files outside the capture archive or under its `exports/` directory.
Reports, Postman and Arazzo exports reject destinations that replace raw recordings,
contracts, retained payloads or the manifest, including symlink and hardlink
aliases. The archive's `exports/` directory must be a real directory; a source
file stored there through an archive alias remains protected. These checks also
apply to automatic exports and `--format all` regeneration.

The text summary keeps operation counts and condition families separate. For
example, this illustrative excerpt makes an untested operation visible without
reading a full JSON report:

```text
Attempts: 2 (2 passed, 0 failed, 0 skipped)
Captured interactions: 2 (2 HTTP, 0 gRPC, 0 other)

Provided APIs
  orders: 1/2 operations exercised
    Untested: POST /orders

Contract conditions (covered / gaps / unknown / unsupported / excluded)
  orders (provided, service)
    enum: 1 / 1 / 0 / 0 / 0
```

The summary warns when recorded tests captured no network traffic and points to
client routing and trace propagation. This is a diagnostic, not a new test
failure. If attempt details are missing, the count is explicitly unavailable.

The JSON report retains all details behind those counts. A condition gap means
no qualifying observation was found; an unknown means the evidence could not
support a decision. The summary does not collapse them into one percentage.

Offline loading uses finite [archive loading budgets](archive-loading.md),
including limits on individual files, cumulative bytes and file reads.
[Coverage policies and comparisons](report-policies.md) provide explicit CI gates
and baseline diffs; [run sets](run-sets.md) group shards from the same build.

Operation coverage shows which declared operations were observed. Contract
coverage measures finite, supported cases from supplied specs, such as an enum
value, optional-field absence or a declared numeric boundary. One test attempt
can exercise many conditions, and multiple attempts can contribute observations
to one condition. Conditions observed together remain linked by message; this is
not exhaustive coverage of every possible combination.

| State | Meaning |
| --- | --- |
| Covered | A complete passing attempt supplied valid evidence for the condition. |
| Gap | No qualifying observation covers the condition. |
| Unknown | Evidence or evaluation is inconclusive. |
| Unsupported | The measurement rules cannot evaluate this condition. |
| Excluded | The target is outside applicable scope or has been shown infeasible. |

A documented, valid error response can earn response-condition coverage.
Framework test outcomes remain independent of contract-validation findings, so
a deliberate invalid request and asserted `400` can be a passing test.
Invalid requests do not earn valid-request coverage. An observed error status
alone does not establish that the test asserted rejection. Incomplete, truncated
or undecodable messages cannot earn successful payload coverage. The same limit
applies to legacy-redacted evidence.
See [contract coverage rules](contract-coverage-rules.md) for exact semantics.

A dependency response records what the declared `mock`, `service` or `unknown`
target returned. A mock response is a consumer assumption; it does not prove
agreement with the real provider or identify which fields the application used.
Wiretap captures and reports existing tests; it does not generate or execute
additional cases.

Capture retains up to 4 MiB per HTTP body by default. Retained payloads can still
be truncated; older archives may also contain redaction metadata. Current
capture retains synthetic data and dummy secrets unchanged.
Exporting a report cannot restore omitted bytes. See
[troubleshooting](troubleshooting.md) for capture limits and integrity errors.

## Arazzo workflows

Export captured JSON/REST requests as an Arazzo document and portable OpenAPI
source files:

```bash
wiretap export --bundle ./artifacts/<run-id> --format arazzo \
  --output-dir ./arazzo
```

The directory contains `arazzo.json` and its source companions. Keep them
together so relative source URLs resolve. `--output ./arazzo/orders.json` chooses
the document's filename and writes companions beside it. Use either `--output`
or `--output-dir`. Stdout is available only when no source companions are needed.
`wiretap report --format arazzo` accepts the same options.

Each recorded attempt becomes a workflow for each eligible boundary: the SUT
and dependencies marked `kind: mock`. Steps retain captured request order,
request values and response status expectations. Retries remain separate
workflows. These are observed test sequences; exporting them does not run them
or infer data dependencies between steps.

Workflow input defaults retain captured literals, including strings that resemble
Arazzo expressions. Steps also retain the complete request headers in
`x-wiretap-request-headers` and the captured URL in `x-wiretap-url`. Standard step
parameters cover query values and supported declared path, header and cookie
parameters. Configure authentication, content negotiation and server addresses
in the consuming runner; frozen sources retain their declared server settings.
Repeated or structured parameters, undeclared query parameters in frozen
operations, and incomplete or altered JSON captures fail export with a diagnostic.

Frozen OpenAPI contracts supply operation references when available. For a
schema-free JSON API, the exporter creates an OpenAPI source describing observed
operations and labels it as observed evidence. It does not infer contract
constraints from examples. Nonempty request and response bodies require a JSON
content type and valid JSON. Empty-body HTTP operations require a JSON content
type or a matching frozen OpenAPI operation.
Native gRPC and non-JSON traffic are excluded. A run with no eligible workflows
does not produce an automatic Arazzo output.

Select `outputs.arazzo: true` to generate `exports/arazzo/arazzo.json` at suite
finalization. `exports/result.json` returns its exact path in `arazzo` and source
paths in `arazzo_files`. `--format all` includes Arazzo when eligible JSON/REST
traffic is present. Export errors are reported independently from coverage and
Postman errors.

## Postman collections

Export a capture run as separate Postman JSON collections. HTTP uses the
existing Collection v2.1 format; native gRPC uses Collection v3.0.0:

```bash
wiretap export --bundle ./artifacts/<run-id> --format postman \
  --output-dir ./postman
```

The directory contains separate collections for the SUT and each dependency
marked `kind: mock` (API: `upstream_kind: mock`) with recorded traffic. Real
services and unknown target kinds are excluded from dependency collections.
HTTP filenames normally use `sut-<application-id>.postman_collection.json` and
`mock-<dependency-id>.postman_collection.json`. gRPC uses
`sut-<application-id>.grpc.postman_collection.json` and
`mock-<dependency-id>.grpc.postman_collection.json`. Unusual or conflicting IDs
receive stable, unique filenames. A gRPC-only run does not add an empty SUT HTTP
collection. HTTP-only exports retain their previous filenames and request encoding.

The SUT collection name is `<application.name> - <application.id>`, for example
`Orders - orders-app`. If the application ID is absent, the API ID is used; if
either part is absent, the available name or ID is used. Mock collection names
use `<dependency.name> - <dependency-id>`, or just the ID when no name is recorded.
Set a dependency's optional `name` in `.wiretap.yaml`, or pass `asset_name` in its
SDK/API registration. `--sut-name` overrides only the SUT collection name.

`--output orders.postman_collection.json` names the primary collection and
places the remaining collection files alongside it. The primary is the SUT
HTTP collection when present, otherwise the SUT gRPC collection, or the first
dependency collection for a dependency-only run. The chosen filename must not
collide with another collection filename. Use either `--output` or
`--output-dir`. Omitting both writes to stdout only when the run produces one
collection.

`wiretap report --format postman` accepts the same options. The export reads and
verifies the run's captured evidence and retained payloads, so the original
workspace and running systems are not required.

For both protocols, each test attempt has its original attempt ID as the folder
ID. Unique test names remain unchanged. Repeated names get an explicit
`[attempt <id>]` suffix; collisions with literal names are resolved deterministically.
Every retry is retained. The original recording names and IDs remain unchanged.
HTTP also retains empty folders when a system had no HTTP requests in that test. The SUT collection contains requests from tests to
the SUT; each mock collection contains the SUT's requests to that dependency.
Requests are ordered by request start time. If a request lacks a start time,
the folder retains capture order and notes that limit. A recorded response is
saved as an example, with a basic Postman test that checks its HTTP status code.
Requests without a recorded response status have a skipped status check.
Request and response bodies use the retained capture and its capture limits.
For compatibility, exports also inspect older archives for redacted evidence.
They reject affected interactions or omit them with an explicit diagnostic when
that policy is selected. Binary request bodies are embedded as base64 file
content that Postman decodes when sending. Binary response examples are labeled and
saved as base64.
Postman's cookie jar, automatic URL encoding and optional generated headers are
disabled so replay uses the captured URL and application headers. Postman still
calculates transport headers such as Host and Content-Length for the request.
Raw bodies use text mode with the recorded Content-Type header, preserving even
malformed JSON used by negative tests.

gRPC v3 collections contain one folder per test with retained gRPC interactions.
Unary, server-streaming, client-streaming and bidirectional calls preserve their
decoded protobuf JSON messages. Saved examples contain outgoing and incoming
messages in the order Wiretap observed their completion, plus request metadata,
response metadata, trailers and final RPC status. Binary metadata values use
canonical padded base64; rich errors retain the verified original
`grpc-status-details-bin` envelope. The first outgoing message is also the
request's initial `message`, as required to describe the invocable request; it
is not an additional send in the saved conversation.

gRPC output retains attempt IDs for folder identity. Execution results, timestamps
and durations remain excluded. An RPC error status is still a recorded interaction.
Complete negative RPC responses are exported.
The v3 schema cannot represent explicit half-close events or raw protobuf
message bytes; those are not added as invented messages. Timing is excluded.

### Incomplete captures

The CLI and automatic Postman export are strict by default: incomplete captures,
truncated bodies, transport failures, undecoded gRPC messages, missing terminal
status, and request trailers that v3 cannot represent fail the export before
replacing any collection. Compatibility checks also reject legacy-redacted
evidence. A complete negative API response remains exportable.

To retain healthy exchanges from a mixed run, use:

```bash
wiretap export --bundle ./artifacts/<run-id> --format postman \
  --postman-omit-incomplete --output-dir ./postman
```

Every omitted interaction is reported to stderr and recorded in
`wiretap-postman-diagnostics.json` beside the collections. Valid interactions in
other tests, dependencies, or protocols remain available. This opt-in does not
modify the source archive. Corrupt archives or invalid configuration always fail;
they cannot be made valid by requesting omissions.

### gRPC replay targets and source schemas

An offline archive does not provide a portable server address or original source
`.proto` files. The default export leaves those fields unspecified. Supply
explicit replay settings with `--postman-config ./postman.json`:

```json
{
  "grpc": {
    "sut": {
      "url": "http://localhost:50051",
      "proto_root": "./proto",
      "proto_file": "inventory/v1/inventory.proto"
    },
    "dependency:payments": { "url": "https://payments.example.test:443" }
  }
}
```

Keys are `sut` or `dependency:<exact dependency ID>`. Keys match original
recorded export-eligible boundaries, including boundaries whose interactions
were all explicitly omitted. Unknown keys fail so a typo cannot silently drop
a replay override. `http`/`grpc` selects plaintext and
`https`/`grpcs` selects TLS; credentials, URL query strings and endpoint paths are
rejected. These settings affect derived collections only.

`proto_root` is relative to the configuration file and `proto_file` is relative
to that root. Supply both together. The exporter compiles the local source
package and verifies the recorded methods are present, then copies source bytes
under `schemas/` and sets Postman's native `schema.source: file` and `location`.
The copied package contains its local `.proto` files and is limited to 256 files
and 16 MiB. A frozen reflection descriptor is
never turned into invented `.proto` source. If original source is unavailable,
configure the schema manually in Postman or use a server with reflection.

Keep the collection and copied `schemas/` tree together. Postman's v3 file schema
locations are relative to its working directory; use the export directory as the
working directory, or select the copied root proto after importing in the desktop
app. Source schema companions require `--output` or `--output-dir`.

### Automatic exports

The same options work in `.wiretap.yaml` for suite-finalization exports:

```yaml
outputs:
  coverage: [json, text]
  postman: true
  arazzo: true
  postman_options:
    omit_incomplete: true
    grpc:
      sut:
        url: http://localhost:50051
        proto_root: ./proto
        proto_file: inventory/v1/inventory.proto
```

Here `proto_root` is relative to `.wiretap.yaml`. The automatic output receipt
`exports/result.json` includes `postman` collection paths, `postman_files` copied
source paths, and `postman_omissions` with exact attempt/flow identities. Export
errors remain under `errors`; coverage can succeed when Postman export fails.
`coverage: [json, text]` writes both `coverage.json` and the compact `coverage.txt`.
`--format all` defaults to those two formats, Postman collections and eligible
Arazzo workflows.
`--format all` also accepts `--postman-config` and `--postman-omit-incomplete` for
explicit regeneration with these settings.

### Compatibility checks

The exporter emits Postman's official single-document JSON representation rather
than the native Git YAML directory layout. HTTP v2.1 and native gRPC v3 exports
are checked against their pinned official
[HTTP](https://schema.postman.com/collection/json/v2.1.0/draft-07/collection.json) and
[gRPC](https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json)
JSON schemas, with concrete expectations for captured request/response content.
The [compatibility harness](../tests/postman-compat/README.md) covers RPC shapes,
binary data, rich errors and retries using actual exporter output. It does not
exercise the Postman SDK parser or claim desktop import/export or server invocation
has been verified. Desktop import and imported-collection invocation are outside
the automated compatibility checks.
