# Machine-readable evidence and coverage

The raw capture is the authoritative input for assumption checks and external
analysis.
Arazzo workflows are example projections. Coverage reports are
versioned measurements of that evidence, not replacement recordings.

## CLI output and exit status

Use `wiretap --help` for the command list and `wiretap <command> --help` for
options. Reports and exports write their selected format to stdout unless an
output path is supplied. Exports with companion files require `--output` or
`--output-dir` so their relative references remain portable. Flag and input errors
are printed once on stderr.

| Exit | Meaning |
| --- | --- |
| `0` | Command succeeded, checks passed, or help was requested. |
| `1` | A compatibility or setup check failed; capture, export or output writing can also fail with this status. |
| `2` | Invalid command/flags, missing required arguments, or unreadable, corrupt or incompatible inputs. |

`doctor` writes its diagnostic report and returns `1` when local setup checks
fail, including a missing configuration. An invalid invocation returns `2`.
Offline report commands return `2` for a missing or corrupt capture,
without replacing existing outputs. Keep stderr and the exit status alongside
machine reports; a failed command does not make a pre-existing report current.

Text reports show bounded samples with omitted counts. Use JSON for complete
check diagnostics and coverage data. Display
limits do not change measured totals or check verdicts.

HTML is a derived offline dashboard.
`wiretap report --coverage-json <json> --format html --output report.html`
renders the complete coverage JSON envelope;
optional repeated `--spec` files and `--arazzo` add supplementary embedded
sources. This mode does not remeasure coverage or verify compatibility, fetch
references or execute workflows. See [dashboard inputs](artifacts.md#offline-html-dashboard).

## Versions and producer

`bundle.json` retains raw format `3.1` and required-capability declarations.
Optional `producer` metadata records the capturing Wiretap version, source commit
and dirty-build state. Old recordings lacking producer metadata remain readable;
their producer is unknown. Export never assigns today's build to an old capture.

JSON coverage uses report envelope `report_version: "1.0"`, described by
[coverage-report.schema.json](coverage-report.schema.json). `producer` identifies
the measuring binary. `capture` identifies the source run, archive format,
revision, application, supplied build sources and original producer. Existing
measurement `rules_version` fields still describe HTTP condition rules.
Consumers must check these versions before interpreting keys or counts. Additive
optional fields are compatible; changed meaning or required fields need a new
report version. Exact-byte hashes establish integrity, not compatibility.

## Stable tests and attempts

Automatic Jest capture derives stable test identities. The HTTP spelling is `test_identity: {id, variant?, attempt_ordinal?}`. IDs are local to the application; variants distinguish parameterized cases.
Supply a nonnegative attempt ordinal when the runner exposes retry ordering.
Omission means ordering was not supplied, never a guessed successful or last
attempt. Automatic Jest capture keeps retry identity but does not supply an ordinal. Direct HTTP callers may use a consistent nonnegative sequence. Names remain display labels; random capture IDs identify executions.
Identity is retained in pending attempts, completed test records and coverage
samples.

Jest derives identities from the file and full test identity. Direct HTTP callers and older captures can omit identity; comparison then has only their supplied labels and execution evidence.

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
prove the provider was executed or agreed with the consumer. The checker compares
the supplied provider archive's application/API IDs with the mapping and, when
declared, its `sut_source.version` with the mapped `version`. The caller must
choose comparable fixture state and tested builds. The checker does not verify
hidden state or require matching test IDs.

## Payload availability and volatile data

HTTP coverage previews include `body_info` with explicit presence, encoding,
retained byte count, observed original byte count, content encoding, truncation,
and explicit `omitted`/`redacted` flags.
An absent body differs from a present empty body. Zero original bytes in older
captures may mean that size was not recorded. Older raw captures may carry
`redactions`; coverage samples expose those legacy rules as `redaction_rules`.
Per-message redaction flags also remain readable as legacy evidence limits. Current capture retains values unchanged. The `redacted`
field is read-only compatibility metadata, not a capture option.
Follow raw evidence references for exact retained data and validation limitations.

Run/attempt IDs, timestamps, trace IDs, ports, request IDs and durations are
execution data. Wiretap retains captured values unchanged. The offline
[assumption checker](assumption-checks.md) ignores capture origins and a narrow
set of transport/tracing headers in its comparison view; additional ignored
headers require explicit flags. Concrete payload values remain significant.
Test environments use synthetic data and dummy secrets. A field in a mock
response does not establish that the consumer required it. A concrete request
without a qualifying provider example is `uncovered`; unavailable run, identity
or retained evidence can make a result `unverified`.

## Assumption-check results

`wiretap check --consumer <run> --provider <run>` compares an explicitly selected
consumer/provider pair. For separate suites in the same build, use
`--consumer-set` and `--provider-set` together. Each pair
reads complete archives and Arazzo workflows from `exports/arazzo/arazzo.json` with their source companions. Explicit pairs can override those paths with `--consumer-arazzo` and `--provider-arazzo`. A passing result requires inputs verified against archive-derived
projections and selected mock scenarios supported by complete
passing provider service tests. With only the provider's frozen OpenAPI 3.0
contract it validates both concrete exchanges under that schema. With both
contracts it also requires conservative equality of exercised validation rules
and checks each exchange under its own schema. Coverage reports and
condition keys do not replace concrete request/response evidence.
Arazzo selections cannot silently omit unsupported traffic from a boundary. The checker verifies the Arazzo document and source
companions, then uses the archives for actual response bodies and headers,
passing-test provenance, mappings and frozen schemas. It does not execute Arazzo
workflow expressions or success criteria.

Use `--format json` for the versioned result, and `--output <file>` to save it.
Pair and run-set results have `report_version: "1.0"` and
`rules_version: "wiretap-assumptions-4"`. Check both versions and `scope` before
interpreting a result. JSON/REST workflow eligibility is checked before the comparison uses the complete retained response evidence.

### Run-set result

With `--consumer-set` and `--provider-set`, the envelope uses
`scope: "same_build_run_sets"`. `consumers` and `providers` retain each member's
source identity and manifest digest. `counts`, `matches` and `findings` describe
the selected dependency across the sets; each match still names one complete
provider exchange. See [run sets](run-sets.md) for membership checks, evidence
selection and aggregation limits.

### Pair result

An explicit pair returns a report with
`scope: "observed_mock_examples_and_exercised_contracts"`:

| Field | Meaning |
| --- | --- |
| `compatible` | Whether every selected mock example has supporting provider evidence. |
| `consumer`, `provider` | Archive path, run ID, snapshot revision, run state, SHA-256 of `bundle.json` in `manifest_sha256`, optional application/API identity and selected projection input paths. |
| `dependency`, `binding` | Selected consumer mock ID and `declared_mapping` or `explicit_inputs` relationship. |
| `ignored_headers` | Effective header exclusion policy, including explicit additions. |
| `counts` | Selected exchange count in `assumptions`, matched exchange count in `verified`, and finding counts in `uncovered`, `mismatched` and `unverified`. |
| `matches` | Consumer/provider evidence reference pairs supporting agreement, with a `contract_check` assurance marker. |
| `findings` | `uncovered`, `mismatch` or `unverified` findings with diagnostic code, message and applicable operation, evidence references and differences. |
| `excluded_provider_evidence` | Provider evidence that could not qualify, with a reason for each exclusion. |

Each source retains `arazzo`, the selected document path, and after verification `arazzo_files`, its companion paths. Run-set member sources use the same fields; an empty shard with no eligible workflows may omit its default document and path. The source also pins the archive revision and manifest digest.

Each evidence reference includes `run_id`, `attempt_id`, `test_name`, optional
`test_identity`, `flow_id`, `evidence_path` and `test_outcome`. Preserve the
referenced archives alongside the result. Uncovered, mismatched and unverified
results all return exit status `1`; invalid inputs return `2`. See
[interpreting results](assumption-checks.md#interpreting-results) for a worked
example and the selection/value comparison policy.

Use `compatible` as the verdict instead of deriving one from a count or the
presence of `matches`. Run-level or identity findings can make a result
unverified independently of the number of selected exchanges. The finding
counts therefore need not partition `assumptions`. A reported match preserves
one qualifying witness; it is not a list of every possible provider match.

Each match's `contract_check` is one of:

| Value | Assurance |
| --- | --- |
| `openapi_3_0_validation_rules` | Exercised HTTP request/response rules agree; each response and each request associated with a status below `400` validates against its own frozen schema. |
| `openapi_3_0_provider_contract` | Consumer dependency schema is absent or explicitly omitted; both actual exchanges validate under the provider's frozen OpenAPI 3.0 contract, with the same negative-request exception. No independent consumer declarations are compared. |
| `unavailable` | Both HTTP schemas are absent or explicitly omitted, so this is an example-only match. |

The text report states how many verified matches are example-only. JSON
consumers can count `matches` whose `contract_check` is `unavailable` when their
policy requires contract assurance. An available consumer schema without a
provider schema, or an unsupported required check, is unverified. A provider
schema without a consumer dependency schema instead uses
`openapi_3_0_provider_contract`.

HTTP dependencies are schema-optional by default. Their consumer archive may
therefore have unknown contract coverage while a later check successfully
validates the examples under the provider's contract. That result neither
changes the original coverage nor supplies missing provider test evidence:
matching complete passing provider examples remain mandatory.
Schema-free captures supply no inferred enum sets, length limits, requiredness
or generated OpenAPI contract. This assurance cannot detect an independent
consumer declaration changing while the captured examples stay valid. Both
HTTP schemas are needed for declared-rule drift checks.

### Finding codes

`kind` is the broad classification. Use `code` for the specific diagnostic
under `wiretap-assumptions-4`:

| Code | Kind | Meaning |
| --- | --- | --- |
| `provider_request_uncovered` | `uncovered` | No complete passing provider example matches this concrete request. This does not prove the request would fail at runtime. |
| `provider_response_mismatch` | `mismatch` | Matching requests exist, but none has the mock's full response under the comparison policy. |
| `http_contract_mismatch` | `mismatch` | Declared validation rules differ on the exercised HTTP operation, selected status or media types, even if both examples satisfy them. |
| `http_contract_violation` | `mismatch` | A checked HTTP response or qualifying request violates the applicable own or provider frozen schema. |
| `http_contract_unavailable` | `unverified` | Required HTTP schemas/rules are unavailable, unsupported or cannot be checked reliably. |
| `consumer_evidence_unavailable` | `unverified` | A selected consumer exchange cannot be checked because its attempt or retained evidence does not qualify. |
| `consumer_run_incomplete`, `provider_run_incomplete` | `unverified` | A source run is not complete or still has pending attempts. |
| `provider_not_service` | `unverified` | The supplied provider SUT is not declared as a service. |
| `provider_identity_mismatch` | `unverified` | The supplied provider application/API does not match the consumer's declared mapping. |
| `provider_version_unverified` | `unverified` | The provider's captured `sut_source.version` does not establish the mapped version. |
| `no_consumer_assumptions` | `unverified` | The selected mock has no observed exchanges, so compatibility cannot be established. |

`differences` identifies response paths, exercised rules under
`contract/request/...` or `contract/response/...`, `contract/validation` for a
value-validation failure, or `contract` for unavailable HTTP assurance. It does not
classify a change as behaviorally breaking.
Extra optional response fields, enum additions or relaxed bounds can fail this
strict policy even when a consumer tolerates them. Findings retain up to ten
provider evidence references, and differing paths are bounded to twenty against
a candidate. Follow the raw exchanges for the full context. See
[issues the check finds](assumption-checks.md#issues-the-check-will-find) and
[what it cannot establish](assumption-checks.md#what-the-check-will-not-establish)
before using these diagnostics as a release policy.

HTTP rule comparison resolves references and retains nested constraints,
optional properties and composition under the exercised operation. It ignores
documentation, examples, defaults and enum/required ordering. It is neither a
whole-document hash check nor a proof that alternate schema expressions are
semantically equivalent. Unexercised operations, unselected media and other
statuses receive no static comparison.
The loader first checks the full frozen document graph for validity and reliable
precision. Malformed, unsupported or precision-unsafe rules outside the
exercised selection can therefore cause `http_contract_unavailable` before
selected-rule comparison.
Explicit media-type `encoding` rules, unsupported schema dialects and exhausted
rule-projection budgets are unverified rather than ignored. Explicit
discriminator mappings are retained conservatively, including their declared
schema target identities.
OpenAPI `content`-encoded parameters/response headers, complex numeric object
parameter/header serialization and path/operation-level server overrides also
remain unsupported required checks. Captured headers with multiple differently
cased keys for one name, or multiple `Content-Type` values, prevent a reliable
contract check as well. Nullable schemas combined with enum,
`not` or composition are unverified because of validator ambiguity for null.

### Input errors and report files

Incomplete runs, unverifiable provider identities and empty selections return
an unverified result before projection verification. In an otherwise eligible
comparison, missing, invalid or stale Arazzo documents or Arazzo
source companions are input errors. Failed or incomplete provider attempts cannot
support a match; other complete passing attempts can establish agreement when
projection verification succeeds. Arazzo regenerates strictly across the entire
run: incomplete or unrepresentable eligible JSON traffic can cause an input
error even on an unselected boundary or an excluded provider attempt.
Input errors are diagnostics on stderr rather than `findings`; they may occur
before any JSON report is written. A report write failure can also leave no new
output. Consumers should preserve the exit status and stderr, and use a fresh
report path rather than trusting an existing file after a failed invocation.

`--output` requires an existing parent directory and a destination outside both
archives and supplied
Arazzo input directories. Keep output outside the containing export
tree even when selecting a document file explicitly; the tree's source companions
are also protected, including when an early unverified result skips projection
verification. Run-set output must be outside every member archive and cannot
replace either manifest. Output cannot replace a projection input. The checker
writes a separate report and leaves the inputs unchanged.

## Check reports, run sets and completion receipts

[Assumption check schemas](check-report.schema.json) distinguish an explicit
pair and same-build run sets by `scope`.
All have independent report/rules versions and producer metadata. Response
mismatches include up to 20 field paths with bounded `expected` and `actual`
excerpts from the first listed provider candidate. The exact comparison uses
the complete retained values, including precise large JSON numbers.

[Run-set manifests](run-set.schema.json) pin original archive manifests; the
[run-set guide](run-sets.md) describes the aggregate coverage envelope and source
evidence. [Control response schemas](control-api.schema.json) describe capability
discovery, completion and export receipts. See the [compatibility policy](support.md#format-compatibility)
for additive fields and the distinction between producer and tested build IDs.
