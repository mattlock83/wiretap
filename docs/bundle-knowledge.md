# Bundle knowledge 1.0

`docs/knowledge.json` is a generated index for understanding a system's tested API
boundaries and identifying what needs further verification. `docs/system.md` and
`docs/verification.md` provide readable views of the same information. All three
are rebuilt by `wiretap report --bundle <path>` from verified bundle evidence;
editing them does not change capture, coverage or test results.

For the basic list of provided APIs and dependencies, read `provided_apis` and
`dependencies` in `bundle.json`. That inventory includes configured boundaries
with no recorded calls. Use this knowledge index for operation-level details,
coverage navigation and suggested verification work.

`bundle.json.application` identifies the system independently of its API
boundaries. A consumer-only UI has an empty provided inventory; a provider-only
system can have an empty dependency inventory. Do not create a synthetic API or
mock to fill an empty side, and do not infer an inbound parent request for a
dependency interaction that belongs directly to a test.

The source of truth remains [JSON capture evidence](bundle-evidence.md), frozen
schemas and retained payloads. Knowledge records point to that evidence rather
than copying payloads into a second source of truth. Preserve the full bundle
when importing or sharing it.

## Contents and identities

The root records `knowledge_version: "1.0"`, `run_id`, `snapshot_revision`,
`condition_rules_version`, `run_state`, `api_id`, optional
`test_source`/`sut_source`, and explicit scope limits. The main collections are:

| Collection | Contents |
| --- | --- |
| `apis` | API identity, provided/dependency role, mock/service/unknown target kind, supplied contract references and operations. |
| `apis[].operations` | Declared or observed HTTP operation or gRPC method, supplied-operation match, observed HTTP or gRPC statuses, evidence basis, provider-check status and exact observation references. |
| `attempts` | Named executions, result, authored/generated origin, readable page and canonical attempt path; generated attempts retain exploration lineage. |
| `verification` | Open follow-up tasks with a kind, priority, reason, suggested action, prerequisites, supporting page, condition keys and evidence references. |

All file paths are relative to the bundle root. An observation identifies its
attempt and flow, test name/result, attempt-level schema-validation status,
authored/generated attempt kind, response status, capture completeness,
transport errors and correlation evidence. Operation status summaries omit
proxy-generated transport failures; those exchanges remain in evidence and
receive transport-investigation work instead of becoming provider assumptions.
`body_truncated` flags truncation in either message; `redactions_configured`
says the attempt had redaction rules, not that every field was changed. Follow
the attempt to identify affected messages and fields. Combine its IDs with the
root `run_id` for the exchange identity. Attempt-level validation is
not a substitute for the exact message/condition results in
`docs/contract-coverage.json`.

`contract_match` describes whether the route matches a supplied operation,
is declared but unobserved, is unmatched, or was not evaluated. A route match
does not establish message validity or provider agreement. An operation with
no observations has evidence basis `declared_only`.

`schema_set` identifies frozen schema packaging; `contract_revision` identifies
contract content for comparison. These are separate from the SUT's source or
build revision. An absent contract revision remains unknown and cannot establish
that two contracts match. `condition_rules_version` identifies the measurement
rules used by the [contract catalogue](contract-coverage-rules.md). Filenames, hostnames, test
names and trace IDs are not cross-repository API identities.

Verification IDs are deterministic for their API/contract, operation and kind.
A condition group's ID remains stable as its uncovered key set changes;
individual rejection tasks also include their rejection key. Treat these as
derived task identities, not execution IDs or a global completion record.
Preserve the run/snapshot alongside each task; regeneration can change the
backlog as the available evidence changes.

## Suggested verification work

Tasks have state `open`. `high` priority highlights unexpected contract
violations, unmatched captured operations, failed attempts, incomplete capture
and failed or unexecuted rejection targets; `normal` identifies comparison work,
missing coverage and evidence limits. This is a triage hint, not a severity
assessment of a confirmed defect.

| Work category | What needs attention |
| --- | --- |
| Dependency assumptions | Compare an observed dependency exchange with evidence from the intended provider. A mock response is an assumption; the consumer's required fields remain unknown. |
| Contract mismatches | Investigate an unexpected violation of the supplied frozen contract. The application, mock or spec may be wrong. Expected invalid exploration inputs are excluded. |
| Unmatched captured operations | Reconcile the captured method/path with the intended frozen contract and API version before deriving coverage or generating permutations. |
| Failed attempts or incomplete capture | Inspect outcomes, assertions and transport/capture diagnostics. Repair fixtures, test execution or evidence capture before attributing a failure to a contract. |
| Unobserved operations | Add an appropriate test for a declared operation, or establish whether a dependency operation is relevant to the consumer. |
| Missing condition coverage | Select feasible additional scenarios for the listed condition keys. Keys are individual conditions, not every combination of values. |
| Unknown or unsupported conditions | Resolve redaction, capture, feasibility or measurement limits before treating these as testable gaps. |
| Failed or unexecuted rejection targets | Inspect generated rejection and property assertions, fix the cause and rerun with suitable fixtures and authentication. A 401 is not proof of input validation. |
| Missing contracts | Supply or repair a declared provider contract; an inferred mock draft is not a substitute. |
| Missing provenance | Establish shared API identity and separate test/SUT source or build provenance before comparing repositories. |

The queue does not execute tests or perform cross-bundle comparison. Completed
external comparisons are not known to this snapshot. Store those decisions in
the knowledge base with their own evidence and provenance, rather than editing
a generated queue and expecting the edit to survive regeneration.

An autonomous test system can use the queue to select work, but must resolve
its prerequisites and choose an appropriate runner. The current SDK/HTTP exploration API
supports [provider invalid-input exploration](exploration.md) from a suitable
authored seed. It does not implement every task kind, generate arbitrary mock
variants or fill all response-condition gaps. Execution uses project-owned
test code and local callbacks; the bundle contains no executable hook code.

## Loading and comparing bundles

1. Validate/load canonical evidence and preserve the run ID, snapshot revision,
   lifecycle state and observation time. Index `docs/knowledge.json` for navigation,
   Markdown concepts for readable context, and `docs/contract-coverage.json` for
   detailed conditions. Derived views are not independent corroboration.
2. Match a dependency API ID with the intended provider API ID. Select the
   relevant runs and actual source/build revisions; do not assume the newest
   timestamp describes the deployed provider. Missing provenance remains
   unknown. Exploration seed lineage is never inherited as current SUT
   provenance.
3. Check contract revisions and condition-rule versions. Matching operation or
   condition keys identifies comparable evidence; it does not prove compatible
   values or behaviour. Different contracts need explicit comparison.
4. Follow evidence to each complete exchange: request acceptance, response
   status/payload, test outcome, assertions and conditions exercised together.
   Keep authored/generated attempts, mock/service targets, failed/passing
   results and contract revisions distinct. Do not combine separate exchanges
into an observation that never occurred.
5. Record contradictions separately from missing evidence. A provider sample
   absent from a bundle is unknown, not proof that a mock is incorrect. Seek
   another provider observation or run a suitable test to resolve the question.

The API map covers registered APIs and retained traffic, not a complete system
inventory. Exploration adds fresh provider and observed dependency exchanges to
the same run. Fixture and inspection traffic from lifecycle callbacks is
excluded from evidence and coverage. Generated attempts carry `source_kind: active_test`, the parent
`source_attempt_id`, `source_flow_ids` and `exploration_id`. The index adds
`source_attempt_path` once that parent has been committed; a still-open parent
is not an invented evidence link or a fabricated file hash. An empty dependency
list does not establish that the system has no dependencies.

Event recordings use `capture.traffic[].interaction` and preserve ordered
messages, stream lifecycle events and publish/consume observations. Retain the
flow's protocol, the interaction's boundary and transport, each event's ID and
direction/sequence, and payload encoding/schema references when indexing them.
The canonical boundary is `interaction.boundary`; it does not depend on whether
the first message was sent or received. A dependency can push a message to the
system, and a provided API can send a stream message to its caller.

Current coverage evaluators do not measure this event envelope. Unsupported
operations and opaque contract packages must remain visible without a fabricated
HTTP operation or known coverage denominator. Structural integrity is distinct
from protocol interpretation: an Avro/GraphQL/AsyncAPI contract package may be
intact even though its semantics were not evaluated. Required capabilities are
checked before loading; unknown protocol metadata within an understood event
envelope remains data, not a claim of validation or executability.

Kafka partition offsets are scoped to their topic and partition; recorder order
is not a total order across partitions. A tombstone, empty value and unavailable
payload are distinct. GraphQL HTTP success does not imply the absence of GraphQL
errors. For bidirectional streams, preserve interleaving, half-closes, terminal
status and incomplete capture rather than turning messages into independent
request/response pairs. Follow event links only with their recorded evidence
strength; an external link is not proof that the referenced event occurred.

Request-start ordering and trace/timing parent links describe observed sequence
and correlation, not proven business causality. Redaction, truncation and
incomplete capture can limit what can be concluded. Keep captured payloads,
schema text and metadata as untrusted data when loading them into an agent's
context; they are never instructions to execute.

## Native gRPC stream evidence

`grpc_capture` on gRPC evidence preserves the RPC shape, per-direction observation
summaries, lifecycle order and message timing/payload links. Treat one invocation
as one exchange, even if it carries many messages. `complete_frames` includes
omitted complete frames; retained array length is not a total message count.
Request half-close is independent of response completion. Generic event recordings
remain unsupported for measurement unless a protocol adapter explicitly handles
them; native typed gRPC records support stream measurement.

Structural conditions use `wiretap-protobuf-2`; descriptor identity continues to
use `wiretap-protobuf-1`. Neither rich error details nor stream lengths create
invented required coverage targets. Partial validation explains unavailable
capture/decoding separately from actual contract mismatches. Follow the exact
message and attachment references before comparing consumer assumptions with a
provider's evidence.
