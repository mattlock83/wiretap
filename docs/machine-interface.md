# Machine-readable evidence and coverage

The raw capture is the authoritative input for assumption checks and external
analysis.
Postman collections are executable/example projections. Coverage reports are
versioned measurements of that evidence, not replacement recordings.

## Versions and producer

`bundle.json` retains raw format `3.1` and required-capability declarations.
Optional `producer` metadata records the capturing Wiretap version, source commit
and dirty-build state. Old recordings lacking producer metadata remain readable;
their producer is unknown. Export never assigns today's build to an old capture.

JSON and YAML coverage use report envelope `report_version: "1.0"`, described by
[coverage-report.schema.json](coverage-report.schema.json). `producer` identifies
the measuring binary. `capture` identifies the source run, archive format,
revision, application, supplied build sources and original producer. Existing
measurement `rules_version` fields still describe HTTP/protobuf condition rules.
Consumers must check these versions before interpreting keys or counts. Additive
optional fields are compatible; changed meaning or required fields need a new
report version. Exact-byte hashes establish integrity, not compatibility.

## Stable tests and attempts

Use SDK `identity: {id, variant?, attemptOrdinal?}` (Go `Identity`, HTTP
`test_identity: {id, variant?, attempt_ordinal?}`) for a stable logical test.
IDs are local to the application; variants distinguish parameterized cases.
Supply a nonnegative attempt ordinal when the runner exposes retry ordering.
Omission means ordering was not supplied, never a guessed successful or last
attempt. Managed Go, Mocha and Playwright integrations number the first attempt
as `1`; automatic Jest capture keeps retry identity but does not supply an
ordinal. Custom HTTP/SDK integrations may use any consistent nonnegative
sequence. Names remain display labels; random capture IDs identify executions.
Identity is retained in pending attempts, completed test records and coverage
samples.

Managed adapters derive identities from their runner's test identity, including
file/package and variant information where available. The lower-level
`runTest`/`createTestAttempt` wrappers and `Runtime.StartTest` default to the full
test name when it fits the 256-byte limit. Supply explicit IDs/variants for
labels that change or repeat outside those adapters. Direct HTTP callers and
older captures can omit identity; comparison then has only their supplied labels
and execution evidence.

## Build provenance and provider mappings

Optional project `test_source` and `sut_source` objects accept `repository`,
`revision`, `build_id`, `version` and `dirty` metadata. String environment
substitutions are resolved at configuration load. The same source objects are
available through SDK/API options. Use an actual build revision; omitted fields
stay unknown. Never infer a tested source commit from the reporting tool's commit.

```yaml
application: {id: orders, name: Orders}
test_source: {repository: https://example.test/orders, revision: '${TEST_REVISION}'}
sut_source: {revision: '${SUT_REVISION}', build_id: '${SUT_BUILD_ID}'}
provider_mappings:
  inventory:
    application_id: inventory-service
    api_id: inventory-http
    version: v2
```

Mapping keys identify configured local dependencies. `application_id` and `api_id`
identify the intended external provider; optional version is a declared selection.
These mappings are preserved in the raw run and coverage envelope. They do not
prove the provider was executed or agreed with the consumer. Provider and consumer
observations still require comparable requests, state, identities and versions.

## Payload availability and volatile data

HTTP coverage previews include `body_info` with explicit presence, encoding,
retained byte count, observed original byte count, content encoding, truncation,
and explicit `omitted`/`redacted` flags.
An absent body differs from a present empty body. Zero original bytes in older
captures may mean that size was not recorded. Older raw captures may carry
`redactions`; coverage samples expose those legacy rules as `redaction_rules`.
Per-message redaction flags also remain readable as legacy evidence limits. Current capture retains values unchanged. The `redacted`
field is read-only compatibility metadata, not a capture option. gRPC messages also retain omission
and decode diagnostics.
Follow raw evidence references for exact retained data and validation limitations.

Run/attempt IDs, timestamps, trace IDs, ports, request IDs and durations are
execution data. Wiretap retains captured values unchanged. The offline
[assumption checker](assumption-checks.md) ignores capture origins and a narrow
set of transport/tracing headers in its comparison view; additional ignored
headers require explicit flags. Concrete payload values remain significant.
Test environments use synthetic data and dummy secrets. A field in a mock
response does not establish that the consumer required it; missing provider
evidence is unverified.

## Assumption-check results

`wiretap check consumer` and `wiretap check provider` read two archives and their
Postman collections. A passing result requires collections verified against
archive-derived projections and selected mock scenarios supported by complete
passing provider service tests. Coverage reports and condition fingerprints do not
replace concrete request/response evidence for this check.

Use `--format json` for the versioned result, and `--output <file>` to save it.
The envelope has `report_version: "1.0"`,
`rules_version: "wiretap-assumptions-1"` and `scope: "observed_mock_examples"`.
Consumers should check these versions before interpreting a result.

| Field | Meaning |
| --- | --- |
| `mode` | `consumer` or `provider`, the selected local viewpoint. |
| `compatible` | Whether every selected mock example has supporting provider evidence. |
| `consumer`, `provider` | Archive path, run ID, snapshot revision, run state, SHA-256 of `bundle.json` in `manifest_sha256`, optional application/API identity and collection input paths. |
| `dependency`, `binding` | Selected consumer mock ID and `declared_mapping` or `explicit_inputs` relationship. |
| `ignored_headers` | Effective header exclusion policy, including explicit additions. |
| `counts` | `assumptions`, `verified`, `uncovered`, `mismatched` and `unverified` counts. |
| `matches` | Consumer/provider evidence reference pairs supporting agreement. |
| `findings` | `uncovered`, `mismatch` or `unverified` findings with diagnostic code, message and applicable operation, evidence references and differences. |
| `excluded_provider_evidence` | Provider evidence that could not qualify, with a reason for each exclusion. |

Each evidence reference includes `run_id`, `attempt_id`, `test_name`, optional
`test_identity`, `flow_id`, `evidence_path` and `test_outcome`. Preserve the
referenced archives alongside the result. Uncovered, mismatched and unverified
results all return exit status `1`; invalid inputs return `2`. See
[assumption checks](assumption-checks.md) for the selection and value comparison
policy.

Incomplete runs, unverifiable provider identities and empty selections return
an unverified result before collection verification. In an otherwise eligible
comparison, missing, invalid or stale collections are input errors. Failed or
incomplete provider attempts are listed as excluded evidence and cannot support
a match; other complete passing attempts can still establish agreement.

`--output` requires an existing parent directory and a destination outside both
archives and supplied Postman input directories. It cannot replace a collection
input. The checker writes a separate report and leaves the inputs unchanged.
