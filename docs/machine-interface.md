# Machine-readable evidence and coverage

The raw capture is the authoritative input for external analysis and comparison.
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
execution data. Comparison requires an explicit policy for volatile fields in
a derived view; Wiretap does not normalize captured values. Test environments
use synthetic data and dummy secrets. A field in a mock response does not
establish that the consumer required it; missing provider evidence is unverified.
