# Wiretap adoption requirements

This is the implementation mapping for the
[application knowledge bundle draft](README.md). It does not change the meaning
of existing [3.1 run bundles](../bundle-evidence.md). Introduce an explicit
repository-publication operation rather than silently labelling existing run
output as conforming to the new profile.

Adoption includes the draft's explicit distinction between base OKF validation and
application-profile validation, and regression checks for hash-driven changes.
The work is complete only when an implemented publisher passes the applicable
[conformance cases](conformance.md), including updates to an existing publication.

## Required implementation changes

| Work | Existing behaviour | Required target |
| --- | --- | --- |
| Publication identity | A bundle belongs to a timestamped run. | A stable application publication has its own ID; executions retain their own IDs in evidence. |
| Test identity | Test attempt IDs determine JSON and Markdown filenames. | Add explicit logical scenario/variant IDs and stable keys. Separate test-definition pages from selected attempts. |
| API and operation paths | API directories include schema-set hashes; operations use hash filenames. | Resolve paths from stable boundary and operation mappings. Contract edits keep page paths. |
| Contract storage | Readers require a contract directory name to equal its set hash. | Register meaningful package paths and validate independent file/manifest hashes. Preserve or explicitly map source filenames and imports. |
| Payload references | Some renderers derive payload paths directly from bytes. | Follow registered artifact references; readable attachments and optional shared objects are both valid. |
| Manifest | `bundle.json` is a run inventory. | Add the new publication manifest and source records as a distinct format, with selected evidence slots and an owned-file registry. |
| OKF frontmatter | `wiretap/1.0`, run ID and snapshot revision are attached to generated concepts. | Use producer-neutral subject bindings and move volatile execution metadata into evidence/provenance. |
| Markdown validation | Validation focuses on known generated views. | Check every non-reserved `.md` file for base OKF frontmatter, including authored, unlisted and extension pages; validate reserved indexes/logs too. Apply the stronger concept schema only to primary pages. |
| Conformance reporting | Run evidence and generated-view checks are tied to the current Wiretap format. | Report base OKF, application-profile, evidence-profile and measurement-profile results separately. A plain valid OKF directory need not satisfy the application profile. |
| Deterministic publication | Hashes can incorporate source locations, and run metadata changes between captures. | Remove checkout locations and changing contract hashes from logical path identity; retain actual-byte integrity hashes and contain legitimate execution changes. Verify identical inputs produce identical output. |
| Regeneration | Rendering updates known output paths. | Stage, validate, check ownership/conflicts and commit a whole publication; preserve authored files and unknown extensions. |
| Knowledge and coverage | Outputs are scoped to a run. | Define separately versioned profiles with exact publication evidence/contract bindings and partial-source semantics. |
| CLI workflow | `report` rebuilds a run bundle. | Provide publication and check operations with an explicit inventory, evidence selection, prior publication and path mappings. Existing commands retain documented semantics until deliberately migrated. |

## Delivery sequence

1. Implement separate base OKF and application-profile validation. Scan the entire
   bundle tree for Markdown, including unlisted files, and validate reserved index
   and log conventions. Add the core schemas, filesystem/reference validation and
   reading of both example bundles. Return the validation level and requirement
   ID in diagnostics; distinguish unsupported and unchecked results from invalid
   data. Pass O01–O07 before relying on the new validator for publication.
2. Add stable subject and key mappings to project configuration and SDK/test
   adapters. Require explicit resolution for ambiguous test names and variants.
3. Implement stable contract packaging and hashes independent of the absolute
   checkout location. Preserve readable import filenames or explicit mappings.
   Remove hash-derived primary paths and directory-name/hash equality assumptions
   from the new reader. Keep exact capture identities, source bindings and real
   integrity verification during conversion. Pass P08–P11 and D04.
4. Specify a public versioned Wiretap protocol-recording profile and measurement
   profiles. Reuse current HTTP, gRPC, event and coverage semantics where valid,
   while replacing storage-path assumptions. Publish schemas and conformance
   fixtures for these profiles before claiming semantic interoperability.
5. Implement selection, staging, ownership conflicts, safe cleanup and check mode.
6. Produce Markdown and optional HTML from the new structured inputs. Ensure
   lookup/rendering follows manifest paths and supports unknown optional profiles.
   Reuse subject/path mappings, keep run metadata in evidence/provenance, and retain
   unchanged page bytes and their original meaningful-change timestamps. Pass
   D01–D07 using actual publisher output.
7. Add CI validation and the complete change-diff acceptance suite, including
   selection, retries, ownership and cleanup. Make check mode produce the same
   candidate as publication while leaving files untouched. Document which commands
   emit run archives and which update the maintained repository bundle.
8. Run the independent producer/consumer exchange before declaring a stable
   public format release.

## Validation acceptance gates

Implement and report two distinct assessments before interpreting evidence:

- **Base OKF:** inspect all Markdown under the bundle root; require frontmatter and
  nonempty `type` on each concept, and the appropriate reserved-file structures.
  Accept unknown concept types, optional fields and ordinary unresolved links as
  OKF allows. Additional authored concepts do not require application subject IDs.
- **Application profile:** verify the declared version, required capabilities,
  manifest, primary-page schema, stable mappings, hashes, mandatory references and
  publication rules. Missing or corrupt required evidence fails this assessment
  without implying that the Markdown violates OKF.

An invalid authored `.md` file must produce a diagnostic and remain intact. It
must not be silently excluded from the base OKF check or rewritten to make the
check pass. Raw Markdown stored as an attachment can retain its bytes under a
non-`.md` filename, with the actual media type and updated references recorded.
That packaging choice must be explicit and respect authored-file ownership.

O01–O07 cover the distinction. C01–C18 and P01–P12 cover the additional package
rules. Protocol and measurement support must have their own declared fixtures
and results; successful hash checks establish integrity only.

## Hash and diff acceptance gates

Tests must exercise the implemented publisher against a prior publication and
compare paths and bytes. Validating the static examples alone does not establish
these guarantees. Each fixture must identify the expected changed-file set and
assert that unrelated files remain byte-identical.

| Change being tested | Required result | Cases |
| --- | --- | --- |
| Rebuild identical declared inputs or vary traversal/completion order. | No changed bytes or paths. Check mode reports no diff and performs no writes. | D01, D02 |
| Generate from the same inputs at different absolute checkout locations. | Identical frozen contract paths, import references, bytes and digests. | P08, P09, D02 |
| Add a test or change its display title. | Existing logical IDs and primary paths persist; only relevant subjects, evidence, provenance, navigation and applicable measurements change. | D03, D07 |
| Change contract content. | The contract digest and affected bindings/measurements change, while the API and operation directories stay in place. | D04, P11 |
| Capture the same visible behaviour with new run IDs and timestamps. | Evidence, provenance and their referenced hashes may change; unrelated concept pages and directories remain unchanged. | D05 |
| Change a dependency's observed behaviour. | Its evidence and applicable assumptions/measurements change at stable paths; unrelated dependencies stay byte-identical. | D06 |
| Corrupt an attachment after publication. | Integrity validation still fails even though hashes no longer determine primary filenames. | C05 |

Do not meet these gates by dropping failed attempts, rewriting captured bytes,
ignoring meaningful changes or retaining obsolete provenance. A fresh execution
is a new input and does not promise a completely empty diff. The guarantee is
stable identity and bounded propagation of legitimate changes. Raw-byte digests
also change when source bytes are reformatted; semantic equivalence requires its
own versioned comparison profile.

## Migration rules

- Preserve existing 3.1 bundles as inputs to an explicit converter. Their current
  manifest, contract-directory and attachment rules remain valid for that format.
- A conversion MUST write a new destination, assign or receive application and
  logical subject identities, and declare its output profile. It MUST NOT mutate
  a source recording in place or imply that existing readers understand the new
  manifest.
- Existing test names and operation selectors MAY suggest initial keys. They are
  not sufficient to disambiguate retries, parameterized tests or renamed scenarios.
  Ambiguities require configuration, never order-dependent guesses.
- Copy or transform evidence only under a documented profile. Preserve execution
  IDs, timestamps, actual source revisions, failed attempts, capture limitations
  and exact contract bindings. Record any transformation and recompute all affected
  references. Hashes from different serializations MUST NOT be treated as equal.
- A legacy run can be retained as an optional extension/archive for compatibility.
  That archive does not satisfy the new format's primary path or diff requirements
  by itself. Claims made by the new publication need its declared self-contained
  evidence graph.
- Keep the new spec and current-format guides distinctly labelled until the new
  publisher and consumer pass their declared conformance roles. Updating a document
  or schema alone does not establish runtime support.

## Scope of this draft

The core package, publication rules, contract package manifest and neutral test
result envelope are specified here. The tested example demonstrates a test-runner
outcome and a retained payload, not a complete HTTP exchange or coverage verdict.
Wiretap's existing recording/coverage documents are source material for the
separate protocol and measurement profiles; they are not implicitly incorporated
as new neutral schemas. Producers may already exchange the core and test-result
envelope while declaring unsupported optional protocol records honestly.
