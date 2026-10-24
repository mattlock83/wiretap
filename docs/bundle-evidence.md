# Wiretap bundle format 3.1

This is the portable raw run artifact format. It stores captured tests, frozen
contracts and retained payloads for offline inspection and exports.

A bundle is a portable recording of tests at API boundaries. The same recorded
tests support coverage and external analysis. Test hooks, fixtures and environment
configuration stay in the project.

```text
wiretap-bundle/
  bundle.json                  identity, API inventory and committed test references
  contracts/<hash>/            frozen contracts, imports and integrity manifests
  tests/<attempt-id>.json       test metadata, interactions, assertions and outcomes
  payloads/<sha256>.bin         retained message bodies and binary representations
```

`contracts/` and `payloads/` appear when there are corresponding files. The
manifest always includes `provided_apis`, `dependencies` and `tests` arrays, even
when empty. Coverage reports are calculated from these verified inputs.

## Discover APIs and dependencies

Read `bundle.json` directly to discover `provided_apis` and `dependencies`.
No live application or original project configuration is needed.
Each boundary includes:

| Field | Meaning |
| --- | --- |
| `id` | The configured API/dependency ID, local to this system. An unidentified boundary uses `unknown`. |
| `target_kind` | `mock`, `service` or `unknown`; this describes the test target. |
| `configured` | Whether the boundary was declared through configuration or registered contracts. |
| `protocols` | Known protocols, such as `http` or `grpc`; an empty array means unknown. |
| `contracts` | Contract IDs, types and frozen source references, or an explicit `omitted` source. |
| `observed_in` | IDs of committed test attempts containing traffic for this boundary. |

For example, extract the dependency inventory:

```bash
jq '.dependencies[] | {id, target_kind, protocols, configured, observed_in, contracts}' bundle.json
```

A configured dependency appears even when no test called it. Follow its
`observed_in` IDs through the manifest's `tests` references to find exact
interactions. In each test file, `capture.traffic` entries identify their boundary
through `interaction.boundary` for event recordings, or `channel` and `asset_id`
for request/response recordings. A dependency event has boundary role
`dependency`; a dependency request/response pair has `channel: sut_to_dependency`.
HTTP exchanges identify methods/URLs; gRPC exchanges identify procedures.
Contracts list the declared operations, including unobserved ones.

`inventory_scope: configured_and_observed` is deliberately limited: an empty
dependency list means none were configured or observed in this run. It does not
prove the application has no other dependencies. An observed mock response is a
consumer assumption, not proof of the real provider's behavior. Local dependency
IDs also do not establish a global provider identity across repositories; that
mapping must be supplied explicitly through `provider_mappings`. Recorded source/build metadata helps assess
which implementation was tested.

The reader verifies that the inventory agrees with the registered contracts,
configured target kinds and committed recordings. Coverage reports are derived
from these verified inputs.

## Consumer-only, provider-only and mixed systems

`application` optionally records the system's `name`, `id` and `description`.
Application identity is independent of API identity: a UI can have an application
name and dependencies without providing an API. The two inventory arrays are
independent:

| System | `provided_apis` | `dependencies` |
| --- | --- | --- |
| UI or other consumer-only system | Empty | Configured or observed dependencies |
| Provider with no dependencies | Configured or observed APIs | Empty |
| Mixed system | Configured or observed APIs | Configured or observed dependencies |

No placeholder provider API, inbound request or mock is required by the bundle
format. Dependency traffic belongs directly to the named test; an inbound parent
flow is optional. In particular, a UI test can record the requests the browser
makes to its dependencies without inventing a request to the UI itself.

An absent side has no applicable coverage denominator. An empty inventory is
still bounded by `inventory_scope`; it does not prove the absence of unconfigured
or uncaptured dependencies. Missing contract data for a boundary that does exist
means unknown coverage, not an absent side or 100% coverage.

These are bundle-format guarantees. Runtime configuration still requires a SUT
boundary. The Playwright adapter routes its request/browser fixtures through that
boundary; it does not add a dependency-only application configuration. Browser
traffic appears only when it actually passes through a capture listener.

## Authoritative records

`bundle.json` and `tests/<attempt-id>.json` are the capture
source of truth.
The [JSON Schema](bundle-evidence.schema.json) describes the main record shape;
the reader also enforces identity, hash, duplicate-key, version, path, and
attachment checks that JSON Schema cannot express.

The run inventory contains `format_version`, `run_id`, a monotonic `revision`,
`run_state` (`open`, `complete`, `interrupted`), times, optional application/API
identity and source provenance, optional capture `producer` and
`provider_mappings`, frozen schema references under `contracts`, pending attempts
under `pending_tests`, and committed `{attempt_id, path, sha256}` records under
`tests`. Test identities distinguish stable logical tests from individual
attempt IDs. See the [machine interface](machine-interface.md) for identity and
provenance fields. gRPC acquisition states are in `contract_resolutions`.
The inventory is replaced atomically.
A committed attempt refers to one immutable JSON file. Only listed attempts
are read. Staging files and orphaned attempt files are ignored by ordinary
readers. A run left `open` after a hard kill remains unfinalized.

Each attempt records the original capture as `capture`, with test metadata,
flows, validation, diagnostics and tracing/correlation evidence. Older captures
may also contain redaction metadata, which remains a read-only evidence limit.
`bodies` connects a flow and phase to a retained body attachment. Nonempty
captured text or base64 body bytes are moved to `payloads/<sha256>.bin`; the
reader verifies the hash and restores the retained representation. Body objects
expose optional `redacted` and `omitted` flags for compatibility with older
archives. A legacy changed representation is redacted; an omitted representation
has no retained bytes. New captures do not redact traffic. These flags are
independent of capture `truncated` state. Absent, present but empty, truncated, and
unavailable bodies remain distinct. Attachment paths and schema paths are
bundle-root-relative in JSON. A copied bundle needs no source workspace or
network access.

Configured OpenAPI schemas and their transitive external references are frozen
at configured run startup, or before admission for per-test overrides. This
keeps declared operations visible even if the suite has no tests. Each
`contracts/<hash>/manifest.json` records the set hash, root document, and all local
document hashes. The reader checks the
manifest identity and every file.

For gRPC, the run inventory freezes each selected boundary's binary
`FileDescriptorSet` under `contracts/<hash>/descriptor-set.protoset`, including
its imported descriptors. The schema reference names the selected services and
contract provenance. Each RPC flow has `protocol: grpc` and typed request and
response messages with procedure, metadata, ordered Protobuf messages, trailers,
HTTP/2 status and a separate gRPC application status. Decoded JSON and raw
Protobuf message bytes are separate, hash-checked attachments with a message
index. An application error can have HTTP 200 and a nonzero `grpc_status`.
Legacy redacted payloads do not retain raw wire bytes.
Run-level `contract_resolutions` retain each declared gRPC boundary while acquisition is
`pending`, `resolved`, or `failed`. Unresolved boundaries have an unknown
coverage denominator and explicit acquisition diagnostics. A complete
run requires resolved contracts; each resolved entry must match its frozen
descriptor and selected services.
An empty Protobuf message still has a zero-byte wire attachment; it is distinct
from a missing or incomplete message. Readers verify the projection against the
frozen descriptor and wire message, and reject inconsistent protocol tags,
schema references, ordered message indices, and duplicate attachment references.

Rich RPC errors have a separate response `status_details` object. It records
where the `google.rpc.Status` envelope arrived, its code/message, decoding state
and ordered `Any` details. The terminal `grpc_status` stays authoritative;
contradictory envelope codes are labelled `inconsistent`. A missing object means
no retained status-detail metadata was observed; an empty envelope, malformed
metadata and truncated evidence have distinct representations. Legacy records
can also identify redacted evidence. Missing metadata in an incomplete capture is not proof that the server sent none.

Custom detail types use the frozen boundary descriptors. Standard `google.rpc`
error types absent from that boundary use a separately hashed auxiliary
`FileDescriptorSet` attachment. The auxiliary set does not change the boundary
contract bytes or semantic revision. Conflicting helper definitions are refused;
unknown types remain opaque. No type URLs are fetched and no fresh reflection
is needed to decode or rebuild the bundle.

Status attachments use `kind: status-envelope`, `status-descriptors`,
`status-detail-wire` or `status-detail-json`; the latter two use a zero-based
`status_detail_index`. These indices are independent of application message
indices. The binary metadata envelope moves into its attachment instead of
also being retained in headers/trailers. Readers verify detail bytes against
that envelope, descriptor hashes and ProtoJSON projections. Legacy redacted
projections explicitly cannot make that original-byte verification claim.

Raw envelope bytes, retained detail bytes, readable projections and captured
response metadata share a 64 KiB budget. At most 256 detail records and a
Protobuf recursion depth of 100 are supported. Auxiliary descriptors have the
normal descriptor limits; they are shared schema data, not user payloads.
Exhausted limits produce explicit truncated/unavailable detail states and never
limit the bytes forwarded to the client. Error details are observations, not
additional response messages or coverage conditions: a service declaration does
not enumerate its allowed rich errors.

Older redacted error records may contain only a modified projection, without
original detail or envelope bytes. Those limitations remain explicit and do not
support original-byte verification or replay. Current capture retains synthetic
test data without redaction.

Writers produce `3.1`. Readers accept supported `3.x` records subject to their
structure and required capabilities. Older captures retain only the guarantees
they recorded; reading them does not infer newer observation fields. `required_capabilities` declares structural features a reader
must understand. Readers reject unknown required capabilities and recordings
that omit the capabilities their structure requires; they must not silently
discard an event interaction and interpret the remaining traffic as complete.
Unknown optional metadata may be ignored. Minor versions may add optional
metadata; incompatible interpretation requires a major version. Unsupported
major versions and corrupt or missing required evidence fail explicitly.
A stable exchange identity is `{run_id, attempt_id, flow_id}`; an event adds
`event_id`. Test names, trace IDs, filesystem paths, ephemeral ports and timestamps
are labels or evidence, not exchange identity.

Both the run and each attempt explicitly list `required_capabilities`, including
an empty array for ordinary request/response recordings. Event interactions
require `event-interactions.v1`; frozen Avro/AsyncAPI/GraphQL packages require
`contract-packages.v1`. These capabilities describe storage structure, not the
availability of a live capture, coverage or execution adapter.

For outbound flows, `assumption` says `evidence_basis: observed_exchange`,
`consumed_fields: unknown`, and `provider_check.status: not_checked` unless a
later, explicit provider comparison changes that separate result. Capturing a
field in a mock response does not mean the consumer required it.

An HTTP API explicitly configured with `schema_required: false` and no supplied spec
has a schema reference with `source.type: omitted` and no source bytes. This is
a declared exception. Validation
lists its ID in `schema_validation.skipped`; mixed attempts have status `partial`
unless an enforced schema fails. Coverage remains unknown for the omitted API,
while concrete observed paths and exchanges remain browsable.

## Streams, messaging and additional protocols

A flow has exactly one representation: the existing HTTP/gRPC `request` and
`response` pair, or an `interaction` containing ordered events. The event form
does not require a response and does not flatten a stream into two lists.
`protocol` on the flow identifies the API or messaging protocol; transport,
contract language and payload encoding are separate. For example, a Kafka
interaction can contain Avro messages described by an AsyncAPI contract, while
a GraphQL subscription can use a WebSocket transport.

| Interaction field | Meaning |
| --- | --- |
| `kind` | `request_response`, `publish`, `consume` or `stream`. |
| `boundary` | `{role: provided\|dependency, id}` identifies the system boundary independently of message direction. |
| `transport` | Transport or transport binding, such as `http2`, `websocket` or `kafka`. |
| `operation` | Protocol-specific procedure, operation or channel identity. |
| `capture_state` | `complete` or `incomplete`; complete capture is not a successful test or contract check. |
| `stream_mode` | For streams: `client`, `server`, `bidirectional` or `subscription`. |
| `events` | Ordered observations belonging to this interaction. |

Each event has an `id`, a contiguous zero-based `sequence`, a `direction` (`sent`
or `received`, relative to the system being tested), a contiguous zero-based
`direction_sequence` within that direction, and a `kind`. Both sequences include
all event kinds. Event kinds
include `headers`, `message`, `half_close`, `trailers`, `complete`, `cancel`,
`error` and `ack`. Optional `observed_at` describes observation time. `metadata`
preserves protocol-specific values. `links` records qualified relationships to
other flows/events through `flow_id` and optional `event_id`. `relation` is
`caused_by`, `reply_to`, `delivery_of` or `acknowledges`; `evidence` is `trace`,
`protocol`, `hook`, `timing` or `unknown`. A relation describes the recorded claim;
timing or an unknown source does not establish causality. Local targets must
resolve within the attempt. External links additionally carry all three of
`run_id`, `attempt_id` and `attempt_sha256`. They are preserved without fetching
the other bundle and do not verify the referenced evidence merely by naming it.

Sequences describe the recorder's observation order, with a separate order in
each direction. They do not establish global ordering across Kafka partitions,
independent connections or systems. Retries and duplicate deliveries remain
distinct events even if they reference the same payload hash.

The generic event form can describe a bidirectional RPC by preserving client
and server message interleaving, initial metadata, a client half-close and final trailers/status.
For example, a provided API may observe `received message`, `sent message`,
`received message`, `received half_close`, `sent message`, then `sent trailers`.
The half-close ends only one sending direction. Cancellation, transport errors
and missing terminal status remain distinguishable from a completed successful
RPC. A capture stopped while the stream is open is incomplete. A terminal gRPC
status is distinct from the HTTP/2 status. Message type and schema references
record the known Protobuf decoding context. The generic reader validates event
structure and references; it does not validate a gRPC state machine, infer a
missing terminal status or treat `capture_state: complete` as protocol success.

Events carry named `payloads` so a Kafka key, value and binary header or an
encoded message and decoded projection can coexist. Each payload records:

- `name`, `encoding` and optional `media_type`/`message_type`.
- `availability`: `present`, `empty`, `null`, `truncated` or `unavailable`. The
  compatibility value `redacted` is retained for legacy evidence only. A Kafka
  tombstone (`null`) differs from a zero-byte value.
- Optional `writer_schema` and `reader_schema` references containing contract ID
  and the frozen root file's `sha256`. They must resolve unambiguously to a schema
  on the same boundary in this attempt. Its package path/manifest pins imports;
  the root digest alone does not identify the complete import closure across
  bundles. A registry's numeric schema ID alone is not portable.
- Optional `derived_from` and `transformation` for a decoded representation.
  `derived_from` names another payload in the same event; projection chains and
  self-references are rejected. A projection is derived evidence; its presence
  does not prove it was correctly decoded by an unsupported protocol adapter.
- A body whose retained bytes use the existing content-addressed `.bin` store.
  `bodies` references use `phase: event`, `event_id` and `payload_name`.

Payload `encoding` describes the message format, such as `avro`, `protobuf` or
`json`. The body's `encoding` describes its storage representation; binary bodies
use base64 before attachment extraction. An `empty` payload still has a zero-byte
attachment. `null`, `redacted` and `unavailable` payloads have no retained body.
For compatibility with older records, a redacted representation cannot retain
source bytes or an available decoded projection that would reveal the original
value. These checks validate legacy evidence; they are not a capture feature.

Frozen GraphQL SDL, Avro schemas and AsyncAPI documents can be stored as opaque
contract packages with their supplied imports. Package manifests record the
contract type/profile, root and file hashes; bytes and relative paths are
preserved. The producer supplies the complete reference closure. Wiretap can
verify package integrity without claiming it resolved every language-specific
import or validated the schema's semantics.

This envelope represents these protocols; it does not enable their live capture,
semantic validation, replay or coverage adapters. Live capture and contract
measurement currently cover HTTP/OpenAPI and native gRPC/Protobuf, including
all four RPC shapes. Execution adapters are outside the local capture tool.
Generic event recordings remain visible as unsupported for measurement/execution
until the relevant adapter is implemented. Native gRPC uses its typed RPC record
and the observation fields below; it does not duplicate payloads into generic events.
GraphQL `errors` can coexist with `data` and HTTP 200; Kafka acknowledgement does
not prove downstream processing; a complete stream does not prove every message
was asserted. Readers must preserve those distinctions.

## Native gRPC observations

Format 3.1 adds `grpc-observation.v1` and `grpc-status-details.v1` required
capabilities. Older readers must reject these features rather than discard their
meaning. The layout and HTTP evidence stay unchanged. Old captures without
observation counters cannot establish streaming completeness.

Native RPCs retain typed `request` and `response` records. Each direction has an
`observation` with `complete_frames`, `retained_frames`, `omitted_frames`,
`observed_bytes`, `counts_complete`, `eof` and `end`. Complete observed frames equal
retained plus omitted frames. `end` distinguishes `open`, `eof`, `error` and
`closed`; closing after an early response is not proof of EOF. Count completeness
refers to the observed interval, not hypothetical later traffic. Framing errors
make subsequent counts unknown. A partial frame has its index, header/payload
byte counts and start observation, without a fabricated completion time.

Retention stops at the first omitted frame. Retained indices are a contiguous
wire prefix; `omitted: {first_index, count}` summarizes the remaining complete
frames in bounded space. The observer counts zero-byte frames and oversized
frames after retention stops. Partial frames are separate from complete counts.
Each direction retains at most 1,024 complete messages and its byte budget.
Decode work and readable projections have a finite aggregate budget too.

Each retained message has an `observation` containing `wire_bytes`,
`started_offset_ns`, `completed_offset_ns`, `start_sequence` and
`complete_sequence`. Offsets use the invocation's monotonic start; `started_at`
provides its UTC anchor. `grpc_events` references lifecycle observations through
`sequence`, `offset_ns`, `direction` (`request`, `response`, `rpc`) and `kind`.
Initial metadata, half-close, end, trailers, cancellation and errors share this
observation order. Message bodies remain stored once, on their direction.
Missing fields are unavailable, never inferred from array lengths or report time.

`decode_state` distinguishes `decoded`, `invalid` and `unavailable`, with a
`decode_error` reason for the latter two. Unsupported compression, capture limits,
recursion limits or an unavailable JSON projection are not fabricated wire
violations. `schema_validation.unavailable` explains partial checks; malformed
Protobuf still fails validation. Retention, completeness and decoding limits
prevent successful stream coverage even when the test itself passes.

Loaders verify counter relationships, omission ranges, wire lengths, original
indices, monotonic timing and sequence uniqueness, as well as the existing hashes,
Protobuf bytes and JSON projections. A drain snapshot is immutable: subsequent
traffic cannot update the committed attempt or migrate into the next test.

## External analysis and replay

The bundle is a build artifact. External tools can read
`tests/<attempt-id>.json`, frozen contracts and ordinary payload files. Sequence, stream events and correlation remain available without a second
scenario representation. Wiretap exports Postman projections for supported HTTP
and native gRPC exchanges; it does not execute those collections. Captured
responses are observations, not exact-match assertions or permission to execute code.

Authentication, fixture setup, substitutions and cleanup belong to project-owned
code outside the bundle. There are no embedded fixture hooks or scripts. Replay
results must remain distinguishable from the captured run.

## Derived contract coverage

`wiretap report` and `wiretap export` calculate JSON/YAML coverage reports from
this evidence and the supplied frozen contracts. Reports never become evidence
inputs. The catalogue under `contract_coverage` identifies the run snapshot, measurement
rule version, content-based contract revision, and API/operation/condition keys.
Each observation links back to an attempt path and flow ID, with phase, instance
location, test name, outcome, message validation, capture completeness and
transport failure details. See [contract coverage rules](contract-coverage-rules.md)
for status meanings and unsupported measurement scopes.

### gRPC contract readiness

`bundle.json` includes `contract_resolutions` for declared gRPC boundaries before
schema discovery: API ID, role, source, service selection and
`contract_resolution` (`pending`, `failed`, `resolved`). Failed acquisition keeps
the run open and the denominator unknown. A resolved entry must match a frozen
Protobuf schema; a complete run cannot contain pending or failed contracts.
Diagnostics omit reflection credentials and filesystem paths. Successful
acquisition provenance is stored on the boundary's schema reference.

Pending test records retain setup metadata and the attempt identity so interrupted
work remains distinguishable from committed capture.
