# Application knowledge bundle specification

**Status:** implementation draft, `1.0.0-draft.1`, 6 October 2026. Profile ID:
`application-knowledge`. This specifies a new publication format. Wiretap's
existing [run bundle format 3.1](../bundle-evidence.md) and
[OKF profile 1.0](../okf-profile.md) remain the current implementation; they do
not yet conform to this draft.

An application knowledge bundle is a portable, self-contained description of an
application's APIs, dependencies, contracts, tests and supporting evidence. Its
normal publication location is a stable directory in the application's source
repository, such as `docs/knowledge/`. A pull request that changes a contract or
adds tests can include the corresponding bundle changes in the same review.

The central requirement is that a change to one application concept produces a
bounded, understandable diff. Concept paths persist across executions and
contract revisions. Evidence retains the identity and provenance of the actual
observations. A consumer can read the bundle without the producing tool, the
original workspace, a running application or a network connection.

This specification defines the package, identity, publication and consumption
contract. It includes a neutral test-result evidence envelope. Protocol capture
formats and coverage algorithms are separately versioned profiles: supporting
the core does not imply an ability to interpret every recording or measurement.

## Normative language and supporting files

The uppercase terms MUST, MUST NOT, REQUIRED, SHOULD, SHOULD NOT and MAY express
requirement levels as defined by [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119)
and [RFC 8174](https://www.rfc-editor.org/rfc/rfc8174). A SHOULD deviation needs a
documented reason; a MUST deviation prevents the corresponding conformance claim.
Requirement IDs in this document are stable references for implementation work.

The following files are part of this draft:

| File | Purpose |
| --- | --- |
| [Shared schema definitions](common.schema.json) | Identifiers, keys, paths and file references used by the record schemas. |
| [Manifest schema](manifest.schema.json) | The root `bundle.json` structure. |
| [Publication schema](publication.schema.json) | Producer, source provenance and evidence selection. |
| [Contract schema](contract.schema.json) | Frozen contract package contents and integrity. |
| [Test result schema](test-result.schema.json) | Execution identity, outcomes, limitations and attachments. |
| [Concept schema](concept.schema.json) | Frontmatter for the profile's primary concept pages after parsing YAML. |
| [Conformance requirements](conformance.md) | Positive, negative and change-diff acceptance cases. |
| [Example bundles](examples/README.md) | A documentation-only bundle and a bundle with a tested API. |
| [Wiretap adoption requirements](wiretap-adoption.md) | Migration from the current producer without claiming the migration is implemented. |

Schemas use JSON Schema Draft 2020-12. Schemas constrain record shapes; the
normative text also constrains references, paths, identity, semantics and
publication over time. Passing a schema alone is insufficient. If this draft's
schemas and prose disagree, the disagreement is a specification defect to fix
before release, rather than an implementation-specific interpretation.

## Relationship to OKF

**BASE-01.** The readable layer MUST conform to
[OKF v0.2 at revision ad30107c](https://github.com/GoogleCloudPlatform/open-knowledge-format/blob/ad30107c/SPEC.md).
This profile adds application-specific structured records and publication rules.
It does not redefine the underlying OKF version.

**BASE-02.** The root `index.md` MUST identify `okf_version: "0.2"` and link to
the application page and available subject groups. Other `index.md` files are
directory listings. Index and log files MUST NOT be used as concept pages.

**BASE-03.** An OKF concept ID remains its bundle-relative Markdown path without
`.md`. The logical subject IDs introduced here identify the application objects
described by those concepts. Both identities MUST be preserved; a subject ID
does not make a path rename invisible to an OKF consumer.

Generic OKF readers can traverse the Markdown. Structured consumers use the
manifest and evidence profiles without parsing narrative prose. Additional
team-authored OKF concepts MAY coexist with the primary pages listed below.

**BASE-04.** Every non-reserved `.md` file anywhere under the bundle root MUST
contain parseable YAML frontmatter with a nonempty `type`, including authored,
unlisted and extension pages. Reserved `index.md` and `log.md` files follow their
OKF structures. The primary-concept schema adds requirements only to primary
pages; it MUST NOT be applied to every other OKF concept. A publisher discovering
an invalid authored page MUST report its path and preserve its bytes, rather than
silently insert frontmatter or delete it. Raw Markdown retained as data MAY use a
non-`.md` filename with its actual media type recorded, preserving the source bytes.

## Conformance classes

**CONF-01.** An implementation MUST state the draft or released profile version,
its roles, and the evidence and measurement profiles it supports. Conformance
roles are independent:

| Role | Required behaviour |
| --- | --- |
| Core producer | Writes the manifest, publication record, readable concepts, declared contracts and complete local reference graph. |
| Repository publisher | Also implements stable updates, ownership, selection and diff requirements across publications. |
| Core consumer | Discovers subjects and files, validates the package, resolves local references and reports unsupported capabilities. |
| Evidence consumer | Also interprets explicitly named evidence profiles and retains their limitations. |
| Measurement producer or consumer | Also implements the named measurement rules and their evidence eligibility requirements. |

**CONF-02.** A core bundle MAY contain no evidence or measurements. Such a bundle
describes declarations; it MUST NOT claim that tests ran or that operations were
observed. Arrays are present and empty when no entries are available. A consumer
or provider inventory MAY be empty without inventing an API, mock or call.

**CONF-03.** Names of producers MUST NOT be fixed by the format. Tool name and
version are provenance. Changing producer does not change bundle or subject
identity. A conforming implementation MUST NOT require a Wiretap binary, service,
account or SDK to create or consume the core package.

**CONF-04.** Consumers MUST distinguish base OKF conformance from this application
profile's conformance and from evidence or measurement support. A missing manifest,
missing profile-required metadata, unsupported application-profile version or
corrupt attachment MUST NOT by itself be reported as invalid OKF. Base OKF permits
missing optional fields or indexes, unknown types and extra frontmatter keys, and
broken cross-links. Its structural rules still apply. When base OKF has not been
checked, report it as unchecked rather than infer validity or invalidity from a
profile result. A plain OKF directory without an application-profile declaration
is outside this profile, not a malformed instance of it. For a bundle declaring
this profile, its additional requirements remain mandatory.

## Directory layout

**LAY-01.** A publication MUST occupy one stable bundle root. Publishers SHOULD
default to `docs/knowledge/` within the application repository and MUST allow an
explicit root. A monorepo MAY contain multiple roots. The root location is not
part of bundle or subject identity. Moving the whole root without changing its
contents MUST preserve all internal references.

```text
docs/knowledge/
  bundle.json
  publication.json
  index.md
  system.md
  apis/<api-key>/
    api.md
    operations/<operation-key>.md
  dependencies/<dependency-key>/
    api.md
    operations/<operation-key>.md
  tests/<test-key>/test.md
  contracts/<contract-key>/
    manifest.json
    openapi.json                      example original or mapped entry filename
    schemas/payment.json             example imported file
  evidence/tests/<test-key>/<slot>/
    attempt.json
    response.json                    example retained attachment
  measurements/<measurement-key>/
    measurement.json
  views/                             optional generated viewers
  extensions/<namespace>/            optional producer or domain extensions
```

**LAY-02.** The four root files above are REQUIRED. Subject directories are
REQUIRED only for subjects present. Evidence, contract, measurement, view and
extension directories MAY be absent when empty. The primary paths are:

| Record | Path |
| --- | --- |
| Application concept | `system.md` |
| Provided API concept | `apis/<key>/api.md` |
| Dependency concept | `dependencies/<key>/api.md` |
| Operation concept | `<parent-boundary-directory>/operations/<key>.md` |
| Test definition concept | `tests/<key>/test.md` |
| Contract package manifest | `contracts/<key>/manifest.json` |
| Selected test attempt | `evidence/tests/<test-key>/<slot>/attempt.json` |
| Measurement | `measurements/<key>/measurement.json` |

**LAY-03.** An independent producer MUST use these primary path conventions.
Consumers MUST discover actual files from `bundle.json`, then validate their
conformance, rather than reconstructing paths from display names. Extra pages
MAY provide further views without replacing the primary pages.

**LAY-04.** The bundle MUST contain every file necessary to validate its claimed
evidence, supplied contract packages and supported measurements. References to
source repositories, external documentation or optional archived runs MAY be
external, but MUST NOT be necessary for core validation or a claimed evidence
result. A package depending on missing mandatory evidence is incomplete.

## Identities and readable names

**ID-01.** `bundle_id` and `application.id` MUST be stable absolute URIs chosen
by the publisher, such as `urn:example:bundles:payments` and
`urn:example:applications:payments`. Bundle identity identifies this maintained
publication series. Application identity identifies the application independently
of repository location, API roles and producing tool. A publisher MUST NOT derive
either identity from a run ID, timestamp, checkout path or current content hash.

**ID-02.** Each subject has an immutable, bundle-local `id`, a stable path `key`,
a mutable human-readable `title`, a `kind` and a lifecycle `status`. IDs are
opaque, case-sensitive strings. All subjects, contracts and measurements share
one ID namespace within the bundle. Cross-bundle references MUST pair an ID with
`bundle_id`; matching local IDs or names alone does not establish equivalence.
An optional `resource` URI identifies a shared external application or API asset.

**ID-03.** Subject kinds are `application`, `provided-api`, `dependency`,
`operation` and `test`. Exactly one application subject is REQUIRED. APIs,
dependencies and tests have that subject as `parent_id`. Operations have an API
or dependency as their parent and record a `protocol` and protocol-specific
`operation` selector. Examples are `POST /payments` for HTTP and
`payments.v1.Payments/CreatePayment` for gRPC. Selectors describe an operation;
they MUST NOT substitute for its stable logical ID. Protocol extensions define
their selector grammar. Undeclared observed routes MUST remain distinguishable
from operations in a supplied contract.

**ID-04.** A key MUST match `[a-z0-9]+(?:-[a-z0-9]+)*`, be at most 64 ASCII
characters, and avoid reserved filesystem names. Keys MUST be unique within the
scope that determines their path: APIs, dependencies and tests each have their
own scope; operations are scoped to their parent; contracts and measurements
each have a bundle-wide key scope. A test represents one declared scenario and
parameter/configuration variant. Different variants MUST have different logical
test IDs and keys, independent of execution order.

**ID-05.** Publishers MUST accept explicit IDs and keys. An initial default key
MAY be derived from a declared name: lowercase ASCII letters, replace runs of
other characters with `-`, and remove leading and trailing `-`. An empty,
reserved, overlong or colliding result MUST require an explicit mapping.
Publishers MUST NOT assign order-dependent `-2` suffixes, random suffixes or
content-hash suffixes to resolve collisions. Existing mappings in the previous
manifest MUST be reused when publishing with another producer.

**ID-06.** Content changes, display-title changes, test execution order,
contract revisions, new sibling subjects and producer upgrades MUST NOT change
an existing key or primary concept path. Intentional identity or path changes
MUST be explicitly requested and represented in `migrations` with `subject_id`,
`from`, `to` and `reason`. A publisher MUST preserve the old concept as a
deprecated forwarding concept for at least that publication and update local
links. The old path MUST NOT be reassigned to a different subject. Migration
records persist in subsequent publications.

**ID-07.** Removing a subject MUST be explicit. An omitted result in a partial
run MUST NOT delete a test or API. Publishers MUST either retain a deprecated
subject and page, or record a tombstone in `removed_subjects` with `id`, `kind`,
`key`, `path` and `reason`. Removed IDs and paths MUST NOT be reused for unrelated
subjects. A semantic replacement gets a new ID and MAY identify `replaces_id`.

**ID-08.** Execution identity is `{run_id, attempt_id}` within a bundle, and
adds the evidence profile's exchange/event ID when addressing a specific
observation. A slot such as `primary` or `retry-1` is a stable publication
location, not an execution identity. A new run MAY replace the bytes at that
slot in a new publication. References to an exact observation MUST include the
attempt file's SHA-256 as well as its path and execution identity.

An evidence slot also declares a stable `group`. Its attempt records the same
`execution_group` and a zero-based `attempt_ordinal` assigned by the harness.
Ordinal zero is the initial attempt. This relationship preserves retry history
even when explicitly named slots are used for multiple selected groups.

Git history preserves older publications. The immutable fact is the bytes
identified by a digest and execution identity, not the continued contents of a
mutable publication path. A reference outside a specific checkout MUST also pin
the repository commit, immutable archive or equivalent publication revision.

## Manifest and file registry

**MAN-01.** `bundle.json` MUST satisfy [manifest.schema.json](manifest.schema.json).
It records the exact profile version, OKF version, identities, inventory scope,
publication path, subjects, contract packages, evidence slots, measurements,
capabilities, migrations, tombstones and file registry. The arrays MUST exist
even when empty. `inventory_scope` describes `declared`, `observed`, or
`declared-and-observed` inputs; none asserts a complete inventory of the
application's internals or possible behaviour.

**MAN-02.** Every file generated as part of the publication, and every file
referenced as mandatory input, MUST appear once in `files`. Each entry has
`path`, `sha256`, `media_type`, `role`, `ownership` and `owner`. The hash is
lowercase hexadecimal SHA-256 of the exact stored bytes, after any declared
redaction or deterministic transformation. `bundle.json` MUST NOT include itself
in this registry; this avoids a self-hash cycle. An external archive or Git
revision can pin the manifest. Hashes establish integrity, not authorship or truth.

**MAN-03.** File roles are `index`, `concept`, `provenance`, `contract`,
`evidence`, `measurement`, `view` and `extension`. `ownership` is `authored` or
`generated`. `owner` is a stable publishing namespace, independent of tool
version. Role, ownership and file extension MUST NOT imply verification.
Contract entries declare whether their content is `supplied` or `inferred`.

**MAN-04.** Unlisted local files MAY exist. Consumers MUST NOT use them as
authoritative evidence or silently add them to an inventory. Publishers MUST
preserve them unless explicitly adopted or removed by the user. Additional
authored concepts MAY be listed as files without becoming primary subjects.
All Markdown concept files, including unlisted authored files, follow BASE-04.
Preservation does not exempt them from base OKF validation or register them as
authoritative evidence.

**MAN-05.** Each primary page, contract manifest, attempt, artifact and
measurement MUST resolve to a registered file with the appropriate role.
References carrying a hash MUST agree with the registry. Parent IDs,
contract `subject_ids`, evidence `test_id`, measurement `subject_ids`, replacement
IDs and evidence references MUST resolve without ambiguity. Referenced subjects
may be deprecated but not absent; `replaces_id` MAY instead resolve to a tombstone.
A tombstone never supplies live evidence. Each primary page's frontmatter subject
ID and title MUST match its manifest subject. The application subject's title and
optional resource MUST agree with the root application declaration.

## File paths and references

**PATH-01.** JSON file paths are bundle-root-relative with `/` separators, no
leading slash, no URI scheme, no empty, `.` or `..` segments, and no query or
fragment. They use ASCII letters, digits, `.`, `_` and `-` within segments.
Paths MUST NOT include credentials, absolute checkout names, timestamps, run IDs
or random values as generated directory components. Genuine source filenames may
contain digits. Generated primary paths obey the stricter key grammar.

**PATH-02.** Paths MUST be unique under ASCII case folding. Segments MUST NOT
end in `.` or match Windows device names, including a device basename followed
by an extension. Producers MUST enforce at most 128 bytes per segment and 240
bytes per bundle-relative path, rejecting or explicitly mapping longer names.
This limit does not include the user-selected outer directory. Symbolic links,
hard links, device files and archive entries that escape the root MUST NOT be
published as bundle files. Consumers MUST enforce containment before reading.

**PATH-03.** Markdown links and OKF path-valued frontmatter follow OKF rules.
Profile producers SHOULD emit document-relative links so ordinary repository
viewers can browse them. A leading `/` in an OKF link means the bundle root,
not the repository or filesystem root. Relative links resolve from the concept's
directory. JSON paths always follow PATH-01. JSON fragment selectors, where
needed, are separate `pointer` fields using JSON Pointer, never appended to a
registered file path.

A subject's optional `source` locator identifies project code: `source_id`
resolves to a publication source and `path` is relative to that source repository,
with optional `symbol`. It is not a bundled file reference and MUST NOT cause a
consumer to open the current machine's checkout. Contract package paths have the
explicit package-relative base defined in CON-02.

**PATH-04.** A published bundle MUST have resolvable local links for its primary
navigation and evidence claims. A consumer MAY display other broken narrative
links as unresolved knowledge, as generic OKF allows. Missing evidence required
for a claim MUST instead invalidate that claim and fail validation of the claimed
conformance level. Merely having a navigable page does not establish integrity.

## Contract packages

**CON-01.** Each contract entry identifies one stable contract slot with `id`,
`key`, `subject_ids`, `origin`, `format` and `manifest`. A slot represents a
declared contract identity, such as `payments-v1`, not a content revision.
Updating its content MUST keep its directory. Two revisions needed simultaneously
MUST occupy distinct explicitly named slots; a publisher MUST NOT overwrite
one revision while retaining evidence that requires it.

**CON-02.** A package manifest MUST satisfy
[contract.schema.json](contract.schema.json). `entrypoint` and each `files[].path`
are relative to the package directory, an explicit exception to bundle-root JSON
paths. Each file lists its SHA-256. The package includes all local or remote
imports needed to interpret the claimed contract. The set of files MUST exactly
match the package's registered source files, excluding its manifest. The root
manifest's hash of `manifest.json` pins the complete package inventory.

**CON-03.** Preserve original relative source filenames when they meet PATH-01
and PATH-02. Otherwise use an explicit stable `source_map` from source document
identifier to packaged path and rewrite internal references consistently. Source
identifiers use credential-free URIs or paths relative to a declared source,
never absolute checkout paths. Remote
imports MUST be frozen locally. Generated inline roots SHOULD use a format name
such as `openapi.json`, `schema.avsc` or `descriptor-set.protoset`. Import
rewriting MUST preserve meaning and MUST NOT depend on an absolute checkout
path. Acquisition context belongs in publication provenance or a capture profile.

**CON-04.** Hash exact stored contract bytes. Hashing the serialized package
manifest identifies its byte revision, including imports. Producers MUST NOT
describe this as semantic contract equivalence: formatting or source layout can
change the digest. A semantic revision algorithm MAY be supplied by a separate
versioned profile. Unchanged contract inputs and mappings MUST produce identical
package bytes across checkout locations.

**CON-05.** Inferred contracts MUST carry `origin: inferred` and MUST NOT be
used as their own coverage denominator or treated as a supplied provider
contract. A `supplied` package may establish declared operations only when the
consumer supports its language. An intact unknown-language package is structurally
valid but semantically unsupported. Credentials and live retrieval are not needed
to validate a frozen package.

## Test results and protocol evidence

**EVD-01.** Each selected evidence slot points to an `attempt.json` satisfying
[test-result.schema.json](test-result.schema.json), with profile
`test-result`, version `1.0.0-draft.1`. Its `test_id` MUST resolve to the manifest's
test subject and match its slot entry. Run and attempt IDs MUST identify the
original execution. The same execution MUST NOT be published with conflicting
outcomes, source identities or factual claims. Replacing a slot with a new
execution MUST replace its execution IDs and digest, while preserving its
readable path.

A changed serialization or transformed representation of the same execution has
a new file digest. It MUST declare `transformation` with the prior
`source_record_sha256`, a public versioned transformation `profile`, and a
`description` of the change. Consumers MUST NOT count multiple representations of
one execution as independent corroboration. The prior digest is provenance; the
prior representation need not be included unless it is needed to support a claim
made by the new representation. A converter MUST preserve the observation
identity and disclose changes in retained information.

**EVD-02.** The envelope records `outcome` (`pass`, `fail`, `skip`, `error`, or
`unknown`), `capture_state` (`complete`, `partial`, or `unavailable`), timestamps,
source IDs, contract bindings, artifacts, and diagnostics. A test outcome is a
test-runner report, not proof of contract validity. `complete` describes the
declared `capture_scope`; it does not mean every application behaviour was
observed. The scope includes a `description` and boundary/operation `subject_ids`.
An empty subject list can explicitly scope a result to the runner outcome alone.
An empty artifact list can represent a test result without a protocol recording.
Times MUST use RFC 3339 UTC form ending in `Z`. A finished time cannot precede
the started time. Open or interrupted attempts have explicit diagnostics and a
partial/unavailable capture state; they MUST NOT be represented as a completed
passing observation.

| Outcome | Meaning |
| --- | --- |
| `pass` | The runner reports that this executed attempt passed its declared checks. |
| `fail` | The runner reports a failed test or assertion. |
| `skip` | The runner deliberately did not execute the scenario. |
| `error` | Setup, infrastructure or runner failure prevented a reliable ordinary test outcome. |
| `unknown` | No trustworthy terminal outcome is available. |

Partial and unavailable captures MUST include diagnostics. A completed passing
runner outcome can coexist with partial capture, and MUST keep both facts.

**EVD-03.** Contract bindings include the logical `contract_id` and exact
`manifest_sha256`. They MUST match the packaged revision used by that execution.
A publisher MUST NOT relabel an old execution as having used a new contract or
new application build. Its `source_ids` resolve to publication source records;
unknown source provenance remains explicitly unknown.

Each attempt's `source_ids` MUST include at least one source with role
`application` and one with role `tests`. Additional source roles are permitted.
Evidence slots are unique by `(test_id, slot)`. Attempt ordinals are unique and
contiguous from zero within `(test_id, run_id, execution_group)`. Slot and record
groups MUST agree. A retry must retain the preceding attempts in that selected
group, including their failures and limitations.

**EVD-04.** Each artifact has a stable local `name`, `kind`, `path`, `sha256`
and `media_type`. Kinds are `recording`, `payload`, `assertions`, and `other`.
Protocol recordings and assertion records also identify a public, versioned
`profile` defining their structure and interpretation. A declared profile need
not be understood for a core consumer to retain the bytes. An evidence consumer
MUST NOT interpret unknown profiles, fabricate HTTP from arbitrary events, or
claim full evidence support when it only understands the outcome envelope.

Payload artifacts additionally record `retention`: a `state` of `complete`,
`truncated` or `unknown`, `redacted`, `retained_bytes`, and optional
`original_bytes`. Retained byte count MUST match the file size. A complete empty
payload has zero bytes. Unavailable payloads have no fabricated file and require
a diagnostic; protocol profiles preserve the more detailed missing/absent states.
An original byte count, when known, MUST NOT be smaller than the retained count.

**EVD-05.** Artifact filenames SHOULD describe their role or stable exchange
key, such as `create-payment-response.json`. Array/event positions MAY identify
ordered messages within one execution; they MUST NOT identify a test across
executions. Shared binary objects MAY use `evidence/objects/<sha256>.bin` for
deduplication. This is the only recommended content-addressed path class; primary
concepts, contract directories and test-result slots MUST use meaningful keys.
Consumers follow registered references and MUST NOT infer object location from a
hash or invent a file extension from a media type.

**EVD-06.** Every evidence profile MUST document field semantics, ID scopes,
ordering, timestamps, required capabilities and reference resolution. Protocol
profiles MUST preserve the following distinctions when applicable:

- Missing, empty, redacted, truncated and unavailable payloads.
- Supplied contracts, observed values, inferred assumptions and asserted results.
- Mock, service and unknown targets, separately for each observation.
- Complete exchanges and transport errors, partial captures or interrupted streams.
- Per-direction stream order, cross-direction observations and inferred causality.
- Test assertion success, contract validation success and provider compatibility.

A body field's presence in a mock response does not show that the application
consumed it. A successful HTTP status does not by itself prove an assertion,
GraphQL success or complete streaming capture. These distinctions MUST survive
conversion between producers. Integrity validation never establishes behavioural
agreement with a provider.

**EVD-07.** Captured bytes MUST NOT be silently changed to reduce Git noise.
Redaction, omission and normalization must be explicit in the responsible
evidence profile, with their effect on completeness and claims. A normalized
readable example MUST be labelled as derived and linked to its source evidence.
It MUST NOT replace exact evidence while claiming the same bytes or completeness. An explicit
transformation follows EVD-01 and preserves which execution it came from.

## Measurements and readable concepts

**VIEW-01.** Every primary concept MUST satisfy
[concept.schema.json](concept.schema.json) after YAML parsing. It includes OKF
`type`, `title`, `description`, `generated.by`, and
`application_knowledge: {subject_id}`. `generated.by` records the actual producer
using OKF's actor convention. `generated.at` is optional; when present it records
the last meaningful content change, not the most recent attempt to render the
page. Authored additional concepts retain their actual authorship.

**VIEW-02.** Primary pages MUST describe their subject and relevant limitations.
They MUST use stable links to supporting records, and MUST NOT copy run IDs,
publication timestamps, global counters or snapshot revisions into every page.
Re-execution details belong in evidence and provenance. A page MAY expose the
specific timing or execution information necessary to explain a failure, freshness
or sequence. Necessary changes are not suppressed merely to reduce diff size.

**VIEW-03.** Subject metadata, supplied contract packages and evidence records
are the structured inputs for generated views. HTML, Markdown summaries, inferred
contracts and measurements do not supply independent observations. Authored
prose MAY supply additional documented knowledge, with its own provenance; it
MUST NOT override recorded test results or silently earn coverage.

**VIEW-04.** Each measurement entry declares `id`, `key`, `subject_ids`, `path`
and a `{id, version}` profile. Its record MUST identify the same subjects, rules
version, exact contract revisions, input evidence paths and hashes, eligibility
rules, results and limitations. Those fields are defined by the named measurement
profile. The core validates its registered file and identity bindings; a consumer
claims semantic support only for measurement profiles it implements.

**VIEW-05.** Measurement profiles MUST distinguish covered, gap, uncertain,
unsupported and excluded outcomes, and an absent or unknown denominator.
No applicable API side or no tests is not automatically zero-percent coverage.
Incomplete or unsupported input MUST remain visible and MUST NOT earn unsupported
coverage. Observations from different attempts MUST NOT be combined into an
exchange that never occurred. Failed attempts and mock assumptions remain
distinguishable from passing complete observations and real-provider evidence.

## Publication and source provenance

**PUB-01.** `publication.json` MUST satisfy
[publication.schema.json](publication.schema.json). It records the producer and
version, source records, `target_source_ids`, selected test IDs, selection scope
and diagnostics. Target source IDs identify the application and test inputs this
publication intends to describe. They MUST resolve to at least one `application`
and one `tests` source. Attempt source IDs describe actual execution inputs and
may differ from the target for retained historical evidence. Consumers MUST NOT
report that older or unknown-source evidence verifies the target build.
Application and test source provenance MUST be separate records with roles
`application` and `tests`, even when they refer to the same revision. Contract,
configuration and other sources MAY also be included. A source has `state: known`
with an immutable revision or build digest, or `state: unknown`. Known local
modifications MUST have `dirty: true`; an ordinary base commit alone MUST NOT be
presented as the exact identity of modified inputs. Pin such inputs with a digest
when claiming exact reproducibility. Source repository URIs MUST omit credentials.

**PUB-02.** Source revisions describe what was actually captured or used. The
documentation commit may follow the application/test commit used by a run;
publishers MUST NOT require the bundle to contain the hash of the Git commit
that contains the bundle itself. This avoids a circular self-reference. Unknown
deployed build identity MUST NOT be replaced with the test checkout's revision.

**PUB-03.** Selection uses `mode: replace-selected-tests`. The publisher MUST
receive a declared test inventory and an explicit set of selected tests.
`scope: complete` means every active test in that declared inventory was selected;
`scope: partial` means a subset. The manifest's active test subjects are that
declared inventory. A bundle containing test declarations but no execution
evidence uses an empty selection. Neither scope claims a complete behavioural
inventory. Selection is a publication input, not an inference from whichever
attempt happened to finish last.

**PUB-04.** For each selected test, publish all attempts in the selected execution
group, including failures, skips, interruptions and retries. Use `primary` for
the initial attempt and `retry-1`, `retry-2`, etc. for harness-declared retries.
For one group named `default`, these are the primary slot names. With multiple
named groups, use `<group>-primary` and `<group>-retry-<ordinal>`, or explicit stable
slot mappings if the composed names exceed the key limit. All records in a group
share its name and source bindings. Retries MUST follow the harness's ordinal,
not completion order. Concurrent or
parameterized variants have distinct test identities. Multiple independent runs
of one test MUST be selected as distinct explicitly named groups/slots or reduced
by an explicit selection policy recorded in `selection.reason`; a publisher MUST
NOT silently select only passing attempts. Slot labels use the key grammar.

**PUB-05.** Unselected tests and their existing evidence MAY remain in a partial
publication. Retained evidence MUST keep its original source bindings and remain
visibly attributable to those sources. A selected test with no completed attempt
requires an explicit unavailable/unknown record and diagnostic; it MUST NOT
silently retain a previous passing result as its new outcome. Old slots belonging
to a selected test may be removed after the new selection is complete, subject
to reference and ownership checks.

**PUB-06.** Publication is complete only after all mandatory references and
hashes validate. A publisher MUST stage changes and commit them as one logical
snapshot, with the manifest as the last commit record. Readers observing a hash
mismatch during concurrent publication MUST retry a bounded number of times or
report an incomplete snapshot; they MUST NOT consume a mixture as valid evidence.
Publishers MUST either use a lock/staged directory replacement or compare the
starting manifest digest before committing. A concurrent update MUST cause a
retry or conflict, not lost work. A failed publication MUST leave the last valid
publication recoverable and MUST NOT commit a partial candidate.

**PUB-07.** Publishers MUST preserve authored files, unlisted files and unknown
extensions. A generated file may be replaced or removed only when its previous
registry entry delegates ownership to that publisher and its current digest
matches the previous entry. An edited generated file requires an explicit
conflict or user-directed adoption. Changing producer requires an explicit
ownership handoff; identical owner strings alone do not authorize overwriting
another tool's output. Cleanup MUST follow the previous registry, never a broad
directory deletion or filename glob. No removed file may have a live mandatory
reference in the candidate publication.

**PUB-08.** Repository publication SHOULD be part of the same PR as the relevant
application, test or contract change. CI SHOULD validate the checked-in package
and compare it with publication from the declared inputs. A validation/rebuild
check MUST NOT rerun tests merely to check deterministic rendering. Optional
per-run archives MAY be retained separately; the committed bundle remains
self-contained for the evidence it claims.

## Determinism and reviewable diffs

**DIFF-01.** With identical source evidence, declared inventory, configuration,
producer version and previous publication (including stable mappings), a
repository publisher MUST produce byte-identical output regardless of working
directory, directory traversal order, map iteration or parallel test completion
order. A clean rebuild uses those declared inputs, not wall-clock time. A new
capture is a new input and may legitimately change evidence and provenance.

**DIFF-02.** Profile-owned JSON MUST be UTF-8 without BOM, two-space indented,
LF-terminated with one final newline. Object keys are sorted by UTF-8 byte order.
Emit Unicode scalars directly except JSON-required escaping; use the short escapes
for backspace, tab, newline, form feed and carriage return and lowercase `\u00xx`
for other control characters. Do not escape `/`. Core counts are non-negative
integers no larger than `9007199254740991`, written without exponent notation.
Duplicate JSON keys, invalid Unicode and non-finite numbers are invalid. Extension
records define their own numeric serialization and MUST be reproducible.

**DIFF-03.** Inventory arrays are sorted by stable identity: `subjects`,
`contracts`, `measurements`, `sources` and `removed_subjects` by `id`; `files` by
`path`; evidence slots by `(test_id, slot)`; migrations by `(subject_id, from, to)`;
source mappings by `(source, path)`; artifacts by `name`; attempt contract
bindings by `contract_id`. Lists of IDs or
capabilities sort lexically and have no duplicates. String comparison is UTF-8
byte order. Arrays whose order is evidence, such as messages, events, assertions
with ordered semantics and retries in a recording, MUST retain their defined
order. A filename's numeric suffix is not a global sorting identity.

**DIFF-04.** Markdown and YAML formatting MUST be deterministic for the same
producer version. YAML uses a safe JSON-compatible subset: string keys, scalars,
arrays and objects; no custom tags, aliases, duplicate keys or implicit timestamp
conversion. Producers SHOULD quote ambiguous strings. A publisher MUST preserve
unchanged authored bytes. Opaque source files and captured payloads retain their
profile-defined original bytes rather than being reformatted under DIFF-02.

**DIFF-05.** Adding a test MUST NOT rename or renumber existing tests, operations,
contracts or evidence slots. A contract content change MUST NOT move the API or
its operation pages. A title-only edit MUST preserve the subject key. New run IDs
or timestamps alone MUST NOT rewrite unrelated concepts. Changes to applicable
coverage, sources, evidence bindings and summaries are legitimate diff changes.

**DIFF-06.** A publisher MUST offer a check mode that validates and computes the
candidate without changing the bundle, and reports changed paths and reasons
(`subject`, `contract`, `evidence`, `provenance`, `measurement`, `navigation`, or
`format`). A byte-identical candidate produces no changes. Upgrading a renderer
MAY deliberately change formatting; the producer version and resulting format
diff MUST be visible rather than disguised as application behaviour changes.

## Capabilities and compatibility

**VER-01.** This draft uses exact version `1.0.0-draft.1`. Draft consumers MUST
require an exact supported draft version. Released versions use major.minor.patch:
major changes permit incompatible interpretation; minor changes only add optional
features or explicitly required capabilities; patch changes clarify existing
semantics without changing valid bytes. A major-version match alone does not
authorize ignoring required capabilities.

**VER-02.** `required_capabilities` lists structural/interpretive features needed
for the bundle's claimed results. Core features require no capability string.
This draft defines `test-result.v1` for the neutral evidence envelope. Any
nonempty `evidence` list MUST declare it. Further capability names MUST be
namespaced absolute URIs with public versioned definitions. Profile IDs for
external evidence and measurements MUST also be absolute URIs; `test-result` is
the reserved built-in profile ID. Their versions MUST identify immutable rules.

**VER-03.** A consumer MUST reject an unsupported major/draft version or unknown
required capability when claiming full support. It MAY offer a clearly labelled
partial preview of understood concepts, but MUST NOT silently omit required
evidence or report complete validation. Understanding a package's bytes and
hashes is distinct from understanding an optional attached protocol profile.

**VER-04.** Unknown optional fields and namespaced `extensions` MUST be retained
by editors and round-trip converters unless explicitly removed by the user.
Readers MAY ignore their interpretation. Unknown fields MUST NOT override core
fields or change core semantics. Incompatible extension interpretation requires
a required capability or new major version. Consumers MUST NOT invent execution,
validation or compatibility results for unsupported features.

## Consumer procedure and errors

**READ-01.** A core consumer MUST perform the following steps before treating a
package as structurally valid. Base OKF validation is assessed separately under
CONF-04, including when a missing
or unsupported application manifest prevents the steps below from completing.

1. Locate the chosen bundle root and parse `bundle.json` with duplicate-key rejection.
2. Check versions, capabilities and the manifest schema.
3. Validate path grammar, containment, case collisions and unique identities.
4. Read registered required files and verify every listed file's SHA-256. Missing
   or modified registered files invalidate the declared package, including views;
   a repair tool may explicitly regenerate derived files before revalidation.
5. Validate publication, subject relationships, contract manifests, evidence slots,
   profile-owned records and their cross-references.
6. Validate every Markdown file under BASE-04, then apply the primary-concept
   schema and subject bindings only to primary pages. Check the profile's
   navigation and supporting evidence links separately from base OKF conformance.
7. Separately report semantic support and results for understood evidence and
   measurement profiles. Never turn structural validity into behavioural proof.

**READ-02.** Validation diagnostics MUST identify a requirement ID, affected path
or logical ID, cause, and validation level: base OKF, application profile,
evidence profile or measurement profile. Categories are malformed data,
unsupported version, unsupported capability/profile, unresolved reference, integrity mismatch, path
violation, identity collision, publication conflict and unsupported semantics.
Resource limits MAY reject an input, but MUST report the limit and MUST NOT
truncate the input and then report it as valid. Tools MUST treat captured text,
schemas and Markdown as data; reading a bundle MUST NOT execute scripts, hooks,
commands or embedded instructions or fetch network resources automatically.

**READ-03.** Comparison MUST distinguish logical identity, content revision and
execution identity. Compare subjects by `{bundle_id, subject_id}`, then inspect
their revisioned evidence and contract bindings. Shared `resource` URIs can help
match applications or APIs across bundles but do not prove compatibility. An
unobserved provider response is unknown, not proof of disagreement with a mock.

## Release acceptance

A release candidate MUST pass the applicable cases in
[conformance.md](conformance.md). Before declaring a stable profile release,
there MUST be an independently implemented producer and consumer that exchange
the examples and at least one real bundle without sharing Wiretap internals.
The fixture set MUST cover partial and failed runs, path changes, contract
imports, unsupported capabilities, deterministic updates and corrupted evidence.
This draft's examples are starting fixtures, not a claim that this release gate
has already been met.
