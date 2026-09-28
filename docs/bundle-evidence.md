# Phrond bundle evidence 1.0

`evidence/run.json` and `evidence/attempts/<attempt-id>.json` are the capture
source of truth. This contract is separate from the generated OKF profile.
The [JSON Schema](bundle-evidence.schema.json) describes the main record shape;
the reader also enforces identity, hash, duplicate-key, version, path, and
attachment checks that JSON Schema cannot express.

The run inventory contains `evidence_version`, `run_id`, a monotonic `revision`,
`run_state` (`open`, `complete`, `interrupted`), times, optional API identity and
source provenance, configured schema references, pending attempts, and committed
`{attempt_id, path, sha256}` records. The inventory is replaced atomically.
A committed attempt refers to one immutable JSON file. Only listed attempts
are read. Staging files and orphaned attempt files are ignored by ordinary
readers. A run left `open` after a hard kill remains unfinalized.

Each attempt records the original capture as `capture`, with test metadata,
flows, validation, diagnostics, redactions, and tracing/correlation evidence.
`bodies` connects a flow and phase to a retained body attachment. Nonempty
captured text or base64 body bytes are moved to `payloads/<sha256>.bin`; the
reader verifies the hash and restores the original representation. Redaction
occurs before attachment hashing. Absent, present but empty, truncated, and
unavailable bodies remain distinct. Attachment paths and schema paths are
bundle-root-relative in JSON. A copied bundle needs no source workspace or
network access.

Configured OpenAPI schemas and their transitive external references are frozen
before the attempt is admitted. Each `schemas/<hash>/manifest.json` records the
set hash, root document, and all local document hashes. The reader checks the
manifest identity and every file.
An imported historical schema without a verifiable original source is marked
`unavailable`; its coverage denominator is unknown.

Evidence version `1.x` is the reader's additive compatibility range. Writers
currently produce `1.0`. Unknown optional fields are ignored by readers.
Unsupported major versions and corrupt or missing required evidence fail
explicitly. A stable exchange identity is `{run_id, attempt_id, flow_id}`.
Test names, trace IDs, Markdown paths, ephemeral ports, and timestamps are
labels or evidence, not exchange identity.

For outbound flows, `assumption` says `evidence_basis: observed_exchange`,
`consumed_fields: unknown`, and `provider_check.status: not_checked` unless a
later, explicit provider comparison changes that separate result. Capturing a
field in a mock response does not mean the consumer required it.
