# Wiretap bundle evidence 1.0

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

An API explicitly configured with `schema_required: false` and no supplied spec
has a schema reference with `source.type: omitted` and no source bytes. This is
a declared exception. Validation
lists its ID in `schema_validation.skipped`; mixed attempts have status `partial`
unless an enforced schema fails. Coverage remains unknown for the omitted API,
while concrete observed paths and exchanges remain browsable.

## Derived contract coverage

Generated provider attempts also contain optional `capture.exploration` evidence:
generator version, source run/attempt/hash, baseline or rejection mode, targeted
constraint, and whether the transmitted request was verified to violate that
constraint. The baseline contains the generated target catalogue and generation
limits/diagnostics. Ordinary `assertions` record expected rejection status,
response schema conformance and any explicit property results. An intentionally
invalid request has schema status `expected_rejection`; unrelated failures still
fail the attempt. Normal capture enforcement is unchanged.

Hook commands/code, runtime bindings, fixture state and credentials are not
execution metadata in the bundle. Captured message values follow redaction rules.
The source bundle stays immutable; exploration records freshly executed evidence
in a separate run and never grants current coverage from old source observations.

`contract-coverage.json`, `contract-coverage.md`, and the HTML Contract coverage tab are
rebuildable views of this evidence and the supplied frozen contracts. They never
become evidence inputs. The catalogue identifies the run snapshot, measurement
rule version, content-based contract revision, and API/operation/condition keys.
Each observation links back to an attempt path and flow ID, with phase, instance
location, test name, outcome, message validation, capture completeness and
transport failure details. See [contract coverage rules](contract-coverage-rules.md)
for status meanings and unsupported measurement scopes.

Exploration's generated `rejection_coverage` catalogue is another derived view,
separate from valid-condition targets. It counts only complete passing attempts
with explicit request-verification, rejection-status and response-schema checks.
Failed or unexecuted targets remain visible. No hook execution is required to
rebuild or export it. See [exploration](exploration.md).
