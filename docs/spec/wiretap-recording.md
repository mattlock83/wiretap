# Wiretap protocol recording profile

Profile ID: `urn:phrond:wiretap:recording`; version: `1.0.0-draft.1`.
This draft defines a self-contained protocol attachment for the neutral
[test result envelope](test-result.schema.json). Its
[schema](wiretap-recording.schema.json) references the public capture definitions
in [bundle-evidence.schema.json](../bundle-evidence.schema.json). Resolve those
relative schema resources locally; no network retrieval is needed.

The attachment is `kind: recording`, `media_type: application/json`, normally
`evidence/tests/<test-key>/<slot>/recording.json`. It contains:

- `profile`, `run_id`, `attempt_id`: the exact profile and originating execution.
- `source_record_sha256`: the digest of the original immutable archive attempt.
- `capture`: the captured testcase, traffic, schemas, assertions, validation,
  redactions, environment and metadata, with retained payload bytes restored inline.
- `contract_ids`: an original capture schema-ID to publication contract-ID map.
- `boundary`: original `api_id`, `sut_target_kind` and `dependency_kinds`.

The neutral envelope's `transformation` declares conversion from the original
archive attempt. Its source digest must equal the recording's source digest.
The converted recording has its own SHA-256; it is not a second independent
observation or an equivalent serialization hash. Test timestamps retain their
instants, serialized in UTC. Execution IDs, outcomes, trace context, observed
ordering and actual source-role bindings remain attributable to the original run.

`capture.schemas[].source.path` is bundle-root relative and points at the pinned
package entrypoint. Its digest is the digest of the published entrypoint bytes.
Each schema resolves through `contract_ids` to an exact manifest digest in the
envelope's `contracts`. If OpenAPI normalization/import rewriting changes source
bytes, related writer/reader schema links use the transformed entrypoint digest.
The package declares that deterministic transformation. Omitted schemas remain
explicitly omitted. Two different source snapshots cannot share one ambiguous
capture schema ID in this profile.

Inline bodies retain their text or base64 bytes and their recorded encoding,
truncation, original-size, redaction and content-encoding metadata. They do not
become fabricated complete originals. HTTP uses the original request/response
model; native gRPC uses explicitly tagged `kind: grpc` messages. gRPC wire bytes,
descriptor packages, projections, ordered frames/events, observation counters and
status details retain the semantics in the
[3.1 recording guide](../bundle-evidence.md). Projections are checked against the
retained wire bytes and frozen descriptor; they are never independent evidence.

Generic interactions retain their explicit boundary, transport, operation,
capture state and ordered events. Directional sequence and local observation
sequence are distinct. Preserve payload `present`, `empty`, `null`, `redacted`,
`truncated` and `unavailable` states, reader/writer schema links, and qualified
causality links. An external event link continues to pin its original execution
and source-attempt digest; conversion does not silently retarget that reference
to an unrelated current slot. See the event representation in the recording guide.

Assertions and schema validation remain distinct captured facts. A passed runner
outcome does not erase failed validation or incomplete capture. The envelope is
partial for an incomplete/interrupted capture, partial event observation,
truncated/redacted/unavailable retained payload, omitted gRPC frames or an open
gRPC direction, with an explicit diagnostic. Unavailable selections carry no
invented recording attachment. The envelope's capture scope describes configured
boundaries, not exhaustive application behavior.

A consumer must verify registry and artifact hashes before interpreting this
profile, resolve the exact contract package/entrypoint bindings, and validate the
typed recording structure. Native gRPC interpretation also checks wire/projection
and status-detail consistency offline. No recorded URL, text, instruction or
schema reference authorizes executing commands or retrieving live resources.
Unknown optional fields are preserved; other protocol/profile versions remain
explicitly unsupported. Structural validity alone proves neither business
correctness nor compatibility with an unobserved provider.

[The HTTP exchange example](examples/wiretap-http/index.md) is produced by the
implemented publisher. Reusable generated regression fixtures in
`internal/knowledge/publication_test.go` cover HTTP text/binary/truncation,
native gRPC wire/projection verification, ordered events/tombstones, failed and
interrupted retries, actual source retention, and corrupted evidence. These
fixtures establish this implementation's supported recording behavior; they do
not replace the independent implementation exchange required for stable release.
