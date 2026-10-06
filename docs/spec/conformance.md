# Application knowledge bundle conformance requirements

These acceptance cases accompany [the specification](README.md), version
`1.0.0-draft.1`. They are requirements for future implementations, not a report
that the current Wiretap producer passes them. Schemas and example bundles cover
record shapes; temporal, filesystem and semantic requirements need the cases below.

Each implementation SHOULD publish a matrix naming its supported roles, profiles,
versions and passing cases. Unsupported optional protocol profiles are acceptable
when reported honestly. Unsupported required capabilities prevent full support.

## Core package cases

| Case | Input or action | Required result | Requirements |
| --- | --- | --- | --- |
| C01 | Load the minimal example with no tests or APIs. | Valid core bundle; no observed behaviour or coverage claim. | CONF-02, MAN-01 |
| C02 | Load the tested API example. | Discover one API, operation and test; verify the packaged contract, result and payload without the source workspace. | LAY-04, MAN-05, READ-01 |
| C03 | Copy either bundle to another path and read it offline. | Same identities, hashes and relationships; no network access needed. | LAY-01, PATH-03 |
| C04 | Substitute a different producer name and version, updating affected digests. | Producer-neutral schemas accept it; subject IDs and paths need not change. | CONF-03, ID-06 |
| C05 | Corrupt a payload or contract import without updating its references. | Integrity mismatch identifying the affected file; no valid-evidence claim. | MAN-02, CON-02, READ-02 |
| C06 | Remove a referenced file or point an artifact at an unlisted file. | Unresolved reference; copying only the Markdown is insufficient. | LAY-04, MAN-05 |
| C07 | Add an unlisted `.txt` note or a valid OKF `.md` concept. | It is not authoritative evidence and is preserved by publication; Markdown still receives base OKF validation. | BASE-04, MAN-04, PUB-07 |
| C08 | Duplicate a subject ID, file path, slot pair or contract key. | Identity collision; no first/last-entry-wins interpretation. | ID-02, MAN-02, MAN-05 |
| C09 | Change a slot's test ID to another test while leaving its record unchanged. | Reject the inconsistent binding. | EVD-01, MAN-05 |
| C10 | Change the current contract but retain an old attempt's contract digest. | Reject or retain the required old contract in its own declared slot; never relabel the attempt. | CON-01, EVD-03 |
| C11 | Add an unknown optional extension and round-trip it. | Preserve its structure and bytes where opaque; no fabricated interpretation. | VER-04 |
| C12 | Add an unknown required capability or unsupported draft/major version. | Explicit unsupported result; optional preview clearly states incomplete support. | VER-01, VER-03 |
| C13 | Remove `test-result.v1` from a bundle with evidence. | Reject the missing required declaration. | VER-02 |
| C14 | Add an optional recording with an unknown profile. | Core retains and integrity-checks it; evidence consumer reports unsupported semantics. | EVD-04, VER-03 |
| C15 | Include duplicate JSON/YAML keys, unsafe YAML tags or invalid Unicode. | Malformed-data diagnostic; no lossy parsing into a valid result. | DIFF-02, DIFF-04 |
| C16 | Give a measurement valid JSON but omit its declared profile or exact evidence bindings. | No semantic measurement-conformance claim. | VIEW-04, VIEW-05 |
| C17 | Remove the complete provider or dependency side. | An empty side remains valid with no invented boundary or denominator. | CONF-02, VIEW-05 |
| C18 | Pin a source to an ordinary base commit while setting `dirty: true`. | Dirty provenance remains visible; exact reproducibility cannot be inferred without an input digest. | PUB-01 |

## OKF compatibility cases

These cases distinguish the permissive base format from the additional application
requirements. Passing or failing one level MUST NOT substitute for checking another.

| Case | Input or action | Required result | Requirements |
| --- | --- | --- | --- |
| O01 | Read a plain OKF directory with one concept containing only `type`, no index and no application manifest. | Valid base OKF; application profile is not present. Do not apply the primary-concept schema. | BASE-01, CONF-04 |
| O02 | Add a non-reserved `.md` file without frontmatter or with an empty `type`, inside an authored, unlisted or extension directory. | Fail base OKF validation at that path; preserve authored bytes and do not publish the candidate as conforming. | BASE-04, MAN-04, PUB-07 |
| O03 | Add an authored non-primary concept with a nonempty unknown `type` and unknown optional fields. | Accept base OKF without requiring primary subject metadata; preserve unknown fields on round-trip. | BASE-04, CONF-04, VER-04 |
| O04 | Corrupt a registered payload while leaving the Markdown intact. | Application integrity fails; base OKF may independently pass. Diagnostics name the failed level. | CONF-04, MAN-02, READ-02 |
| O05 | Remove an index or introduce a broken link in otherwise valid OKF. | Base OKF remains valid. Report a separate application-profile error only where this profile requires the index or link to resolve. | CONF-04, BASE-02, PATH-04 |
| O06 | Add a nested `index.md` with concept frontmatter, or a `log.md` that violates the reserved log structure. | Report the reserved-file violation even though the file is not a primary concept. | BASE-02, BASE-04 |
| O07 | Use an unsupported application-profile version with readable OKF pages. | Report unsupported application profile separately; assess base OKF or explicitly label it unchecked. | CONF-04, VER-03, READ-02 |

## Path and identity cases

| Case | Input or action | Required result | Requirements |
| --- | --- | --- | --- |
| P01 | Add a test whose generated slug collides with an existing test. | Require explicit stable mapping; existing path stays unchanged. | ID-04, ID-05 |
| P02 | Change only a display title or add an earlier-sorting sibling. | Existing keys and paths stay unchanged. | ID-06, DIFF-05 |
| P03 | Explicitly rename a concept path. | Record migration, preserve a deprecated forwarding concept and fix local links. | ID-06 |
| P04 | Explicitly remove a subject. | Retain a deprecated concept or tombstone; no reuse of its ID/path for a different subject. | ID-07 |
| P05 | Publish `../x`, absolute paths, a drive path, backslashes, query strings, fragments or case-folded duplicates as JSON file paths. | Reject before opening the target. | PATH-01, PATH-02 |
| P06 | Include a symlink, hard link, device file or escaping archive entry. | Reject as a published bundle file. | PATH-02 |
| P07 | Use `con.json`, an overlong component or a trailing dot. | Require a stable safe mapping or reject. | PATH-02 |
| P08 | Freeze identical contract inputs from two different absolute checkout locations. | Identical packaged paths, bytes and digests. | CON-03, CON-04 |
| P09 | Freeze imports with colliding basenames from different directories. | Preserve distinguishing relative paths or use explicit mappings; no hash-only document names. | CON-03 |
| P10 | Read a document-relative link and an OKF bundle-root link. | Correctly resolve each base; neither escapes to the repository/filesystem root. | PATH-03 |
| P11 | Publish two revisions of a contract required by separate retained attempts. | Distinct stable contract slots, exact attempt bindings and no overwrite. | CON-01, EVD-03 |
| P12 | Reuse an ID/path reserved by a tombstone or migration for an unrelated subject. | Reject the publication. | ID-06, ID-07 |

## Repository diff cases

Compare tracked candidate bytes, not file modification times. Use identical
declared inputs and the same previous publication unless the case changes them.
The file registry and publication provenance may change when their inputs change;
that is distinct from rewriting unrelated concept pages.

| Case | Change | Required diff |
| --- | --- | --- |
| D01 | Rebuild the same inputs twice. | Empty second diff. |
| D02 | Change working directory, map order, directory enumeration and parallel completion order. | Empty diff from the reference output. |
| D03 | Add `test:refund-payment`. | New test page/evidence plus affected inventory, navigation and measurements; existing test and operation paths unchanged. |
| D04 | Change a field constraint in `payments-v1`. | Contract files and affected descriptions/measurements change at existing paths; the API tree is not renamed. |
| D05 | Repeat a test with fresh execution IDs/timestamps and identical visible behaviour. | Evidence/provenance and affected hash entries change; unrelated API/test definitions do not. |
| D06 | Change a dependency's observed response shape. | Relevant evidence, derived assumptions and applicable measurements change; unrelated dependencies remain byte-identical. |
| D07 | Rename the test's display title. | Its page/title and relevant navigation change; test ID/key and evidence paths persist. |
| D08 | Add a failed initial attempt followed by a passing retry. | Both attempts and the retry relationship are visible; no success-only replacement. |
| D09 | Publish a partial test selection against a new application revision. | Selected evidence changes; retained observations retain their older source bindings and are not reported as current-build evidence. |
| D10 | Selected test produces no finished attempt. | Explicit unknown/unavailable or interrupted evidence; previous pass is not silently reused. |
| D11 | Change only an optional renderer version/format. | Formatting changes are identified separately; execution identities and evidence are unchanged. |
| D12 | Remove a generated file that is no longer selected. | Delete only owned, unmodified, unreferenced output; preserve authored/unlisted material. |

D01–D12 exercise ID-06, PUB-03–PUB-08 and DIFF-01–DIFF-06. A publisher's check mode
MUST report the same candidate paths as a subsequent publish using the same inputs.
It MUST leave the working tree untouched. Re-running tests is a separate operation.

## Publication and evidence cases

| Case | Input or action | Required result | Requirements |
| --- | --- | --- | --- |
| E01 | Read the same slot from two Git revisions with different execution IDs. | Treat them as different observations; an exact old reference pins the old digest and publication. | ID-08 |
| E02 | Interrupt publication before the manifest is committed. | Previous valid publication is recoverable; incomplete candidate is not claimed as valid. | PUB-06 |
| E03 | Two publishers start from the same base and publish different changes. | Detect conflict or retry using the new base; no silently lost update. | PUB-06 |
| E04 | Edit a generated page by hand before regeneration. | Detect digest conflict; preserve the user's bytes until explicitly resolved. | PUB-07 |
| E05 | Switch producer without ownership handoff. | Discover and preserve output; do not silently overwrite it. | PUB-07 |
| E06 | An assertion passes but capture is partial. | Preserve both facts; do not upgrade it to complete evidence. | EVD-02, VIEW-05 |
| E07 | Redact or truncate a payload. | Record the condition and its measurement limitations; do not present the bytes as complete originals. | EVD-06, EVD-07 |
| E08 | Compare a mock response with an unobserved provider response. | Report unknown agreement; do not claim compatibility or incompatibility from absence. | EVD-06, READ-03 |
| E09 | Include an inferred contract beside a supplied one. | Distinguish origins and exclude self-derived denominators. | CON-05 |
| E10 | Read captured text containing execution instructions. | Treat it as data, with no automatic command execution or fetching. | READ-02 |
| E11 | A protocol recording contains streams or messaging. | Preserve ordering, boundary role and capture limitations per its profile; do not turn events into invented independent HTTP calls. | EVD-04, EVD-06 |
| E12 | Exceed a consumer's resource limit. | Explicit limit diagnostic; no silent truncation followed by a validity claim. | READ-02 |

## Independent implementation release gate

Before a stable release, a producer and consumer implemented independently of
Wiretap internals MUST exchange a minimal bundle and a tested bundle. The consumer
must discover subjects, resolve the contract import graph, verify attempt
bindings, and report the same result and limitations. At least one supported
protocol profile must be exercised if protocol evidence interoperability is
claimed. Sharing schemas and fixtures is expected; sharing one serializer/parser
alone does not demonstrate independent interpretation.

The release record MUST distinguish base OKF conformance, core conformance,
repository publishing, evidence interpretation and measurement support. A report must identify skipped
optional profiles rather than imply that all protocols are supported.
