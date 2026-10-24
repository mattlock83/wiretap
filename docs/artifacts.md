# Capture artifacts and exports

Wiretap writes one portable directory per run under `./artifacts` by default.
Set `output_dir` in `.wiretap.yaml` or `WIRETAP_OUTPUT_DIR` to choose another
parent directory. SDK/API responses return the exact `bundle_path`.

Each test teardown commits an immutable JSON recording. Suite cleanup finalizes
the run through `/v1/finishRun`. Keep the complete directory when sharing it or
uploading it to CI so its contracts and payload references remain valid.

```text
artifacts/<run-id>/
  bundle.json             run identity, API/dependency inventory and test references
  contracts/              frozen contracts, imports and integrity manifests
  tests/                  immutable JSON recording per test attempt
  payloads/               retained, redacted body bytes
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

## Generate coverage

`report` and `export` read and verify captured evidence, frozen contracts and
retained payloads. They work offline without the original workspace or servers.
Both default to JSON coverage; use YAML when useful for another tool:

```bash
wiretap report --bundle ./artifacts/<run-id>
wiretap report --bundle ./artifacts/<run-id> --output coverage.json
wiretap export --bundle ./artifacts/<run-id> --format yaml --output coverage.yaml
```

Omit `--output` to write to stdout. The report includes operation coverage,
validation diagnostics and detailed condition results under `contract_coverage`.
These reports are derived from the raw artifact and never become evidence inputs.
`POST /v1/report` exposes the same JSON coverage calculation through the runtime.

Offline loading uses finite [archive loading budgets](archive-loading.md),
including limits on individual files, cumulative bytes and file reads.

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
| Uncertain | Evidence or evaluation is inconclusive. |
| Unsupported | The measurement rules cannot evaluate this condition. |
| Excluded | The target is outside applicable scope or has been shown infeasible. |

A documented, valid error response can earn response-condition coverage.
Invalid requests do not earn valid-request coverage. An observed error status
alone does not establish that the test asserted rejection. Incomplete, redacted,
truncated or undecodable messages cannot earn successful payload coverage.
See [contract coverage rules](contract-coverage-rules.md) for exact semantics.

A dependency response records what the declared `mock`, `service` or `unknown`
target returned. A mock response is a consumer assumption; it does not prove
agreement with the real provider or identify which fields the application used.
Wiretap captures and reports existing tests; it does not generate or execute
additional cases.

Capture retains up to 4 MiB per HTTP body by default. Retained payloads can still
be redacted or truncated; exporting a report cannot restore omitted bytes. See
[troubleshooting](troubleshooting.md) for capture limits and integrity errors.

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
collection. HTTP-only exports keep their previous files and encoding.

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

For HTTP, each captured test attempt becomes a folder named after its test. Repeated test
attempts remain separate folders, including empty folders when a system had no
HTTP requests in that test. The SUT collection contains requests from tests to
the SUT; each mock collection contains the SUT's requests to that dependency.
Requests are ordered by request start time. If a request lacks a start time,
the folder retains capture order and notes that limit. A recorded response is
saved as an example, with a basic Postman test that checks its HTTP status code.
Requests without a recorded response status have a skipped status check.
Request and response bodies use the retained capture, including its redactions
and capture limits. Binary request bodies are embedded as base64 file content
that Postman decodes when sending. Binary response examples are labeled and
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

gRPC output contains no execution result, attempt ID, timestamp or duration.
An RPC error status is still a recorded interaction. Repeated test names are
rejected rather than disambiguated using attempt IDs. Export also rejects
incomplete capture, undecoded messages, missing terminal status, and request
trailers that v3 cannot represent. Complete negative RPC responses are exported.
The v3 schema cannot represent explicit half-close events or raw protobuf
message bytes; those are not added as invented messages. Timing is excluded.

The captured gRPC records do not retain an endpoint URL or a portable source
`.proto` path, so the exporter omits `url` and `schema` rather than inventing
them. Supply contracts as separate files and configure a server and schema in
Postman for replay.
The exporter emits the official single-document JSON representation, not the
native-Git YAML directory layout. Its output is checked against the
[official v3 JSON schema](https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json);
a Postman desktop import/export round trip is not covered by the test suite.
