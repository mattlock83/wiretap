# Supported integrations and compatibility

Wiretap captures traffic associated with existing tests, measures supported
conditions in supplied contracts, and checks consumer mocks against provider
test evidence. Executable Postman collections carry the captured examples and
their provenance. Framework execution and assertions remain in your test suite.

| Integration | Supported surface | Ownership |
| --- | --- | --- |
| Go | Go 1.26+, standard `testing` | One managed runtime per package with `Run` and `StartTest`. |
| JavaScript | Node 22+; Jest 29/30, Playwright Test 1.64, Mocha 11/12, plain lifecycle API | Runner adapters or an outer managed harness. |
| Python | Python 3.9+; optional pytest 7.4–9 and pytest-xdist | Stdlib control client/context managers; one runtime per pytest worker. |
| Other runners/languages | HTTP control API | User-owned harness implementing the lifecycle contract. |

The repository CI exercises Go and JavaScript across Linux, macOS and Windows.
Python/pytest lanes cover Python 3.9, 3.12 and 3.14, including isolated workers,
with Python 3.12 also configured on macOS and Windows. Dependency ranges live in each SDK's package
metadata. The Python integration supplies explicit URLs and trace headers to
existing clients; it does not install a tracing provider or patch client code.
Application-side propagation is still required for shared concurrent services.

| Protocol/contract | Scope |
| --- | --- |
| HTTP/HTTPS + OpenAPI 3.0 | Boundary capture, supported contract conditions and exercised-rule assumption checks; configure HTTPS trust. |
| Native gRPC + Protobuf | Unary and client/server/bidirectional streaming, reflection or local descriptors/protos; documented TLS support. |
| OpenAPI 3.1 and unsupported schema constructs | Explicit measurement/check limits; no claim of complete semantic support. |

See [coverage rules](contract-coverage-rules.md), [gRPC scope](grpc-support.md)
and [assumption-check limits](assumption-checks.md#what-the-check-will-not-establish)
for precise semantics. A coverage percentage is not a compatibility proof.

## Format compatibility

| Contract | Current identifier | Reader behavior |
| --- | --- | --- |
| Control API | `api_version: "1.0"` at `/v1/capabilities` | Check required capabilities; tolerate documented optional fields. |
| Raw archive | `format_version: "3.1"` | Enforce required capabilities and archive integrity before reading evidence. |
| Coverage, assumption checks and completion/export receipts | `report_version: "1.0"` | Accept additive fields within a supported version; reject unsupported versions and unknown semantics needed by a decision. |
| Run set manifest | `run_set_version: "1.0"` | Validate all members and their pinned manifest hashes; reject unknown manifest fields. |
| Coverage policy | `policy_version: "1.0"` | Reject misspelled/unknown policy keys; gates are opt-in. |
| Measurement rules | Per-report/per-spec `rules_version` | Compare only matching known rule identities. Changed contract revisions stay distinct. |

Producer version and commit describe the binary writing a document. They are
independent of the tested application's revision in `sut_source`. New optional
fields may be added within an envelope version. Changes to target identity or
measurement meaning change the applicable rules version; incompatible document
shape changes require a new format version. Preserve unknown fields when copying
documents and the complete original archive when regenerating exports.

Schemas: [raw archive](bundle-evidence.schema.json),
[coverage](coverage-report.schema.json), [assumption checks](check-report.schema.json),
[run sets](run-set.schema.json), and [control/export responses](control-api.schema.json).
The latter exposes separate `$defs/capabilities`, `$defs/finish` and
`$defs/exports` definitions. Error-only HTTP responses remain ordinary API errors.

Assumption checks support [JUnit XML report output](report-policies.md#send-assumption-checks-to-ci)
for CI importers. This report format is independent of the test runner used to
capture the evidence.
