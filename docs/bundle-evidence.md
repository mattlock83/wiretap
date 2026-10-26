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
  tests/<attempt-id>.json       test metadata, HTTP exchanges, validation and outcomes
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
| `protocols` | `http` when known; an empty array means unknown. |
| `contracts` | Contract IDs, types and frozen source references, or an explicit `omitted` source. |
| `observed_in` | IDs of committed test attempts containing traffic for this boundary. |

For example, extract the dependency inventory:

```bash
jq '.dependencies[] | {id, target_kind, protocols, configured, observed_in, contracts}' bundle.json
```

A configured dependency appears even when no test called it. Follow its
`observed_in` IDs through the manifest's `tests` references to find exact
HTTP exchanges. In each test file, `capture.traffic` entries identify their
boundary through `channel` and `asset_id`. Outbound dependency exchanges use
`channel: sut_to_dependency`; calls to the SUT use `channel: test_to_sut`.
Each exchange records its request method, URL and response when available.
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
boundary. Jest capture uses that boundary and records only traffic that passes through its capture listener.

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
provenance fields.
The inventory is replaced atomically.
A committed attempt refers to one immutable JSON file. Only listed attempts
are read. Staging files and orphaned attempt files are ignored by ordinary
readers. A run left `open` after a hard kill remains unfinalized.

Each attempt records the original capture as `capture`, with test metadata,
flows, validation, diagnostics and tracing/correlation evidence. Older captures
may also contain redaction metadata, which remains a read-only evidence limit.
Schema-validation findings are recorded in `capture.schema_validation`.
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

Writers produce `3.1`. Readers accept supported `3.x` HTTP/OpenAPI records
subject to their structure and integrity. Older captures retain only the
guarantees they recorded; reading them does not infer newer observation fields.
Both the run and each attempt must include `required_capabilities: []`.
Unsupported capabilities, event records and non-OpenAPI contracts are rejected
explicitly. Unknown optional metadata may be ignored. Unsupported major
versions and corrupt or missing required evidence fail explicitly.

A stable exchange identity is `{run_id, attempt_id, flow_id}`. Test names, trace
IDs, filesystem paths, ephemeral ports and timestamps are labels or evidence,
not exchange identity.

For outbound flows, `assumption` says `evidence_basis: observed_exchange`,
`consumed_fields: unknown`, and `provider_check.status: not_checked`. Capturing a
field in a mock response does not mean the consumer required it.
The offline [assumption checker](assumption-checks.md) writes a separate result;
it does not mutate the captured assumption or its original `not_checked` state.

An HTTP API captured without a spec has a schema reference with
`source.type: omitted` and no source bytes. This is the default for HTTP
dependencies; the main SUT API requires an explicit `schema_required: false`
opt-out. Validation
lists its ID in `schema_validation.skipped`; mixed attempts have status `partial`
unless an enforced schema fails. Coverage remains unknown for the omitted API,
while concrete observed paths and exchanges remain browsable.

## External analysis and replay

The bundle is a build artifact. External tools can read
`tests/<attempt-id>.json`, frozen contracts and ordinary payload files. HTTP
request timing and trace correlation remain available in the raw records.
Wiretap exports Arazzo workflows for supported JSON/REST exchanges; it does not
execute the workflows. Captured
responses are observations, not exact-match assertions or permission to execute code.

Authentication, fixture setup, substitutions and cleanup belong to project-owned
code outside the bundle. There are no embedded fixture hooks or scripts. Replay
results must remain distinguishable from the captured run.

## Derived contract coverage

`wiretap report` calculates JSON, text and HTML coverage reports from
this evidence and the supplied frozen contracts. Reports never become evidence
inputs. The catalogue under `contract_coverage` identifies the run snapshot, measurement
rule version, content-based contract revision, and API/operation/condition keys.
Each observation links back to an attempt path and flow ID, with phase, instance
location, test name, outcome, message validation, capture completeness and
transport failure details. See [contract coverage rules](contract-coverage-rules.md)
for status meanings and unsupported measurement scopes.
