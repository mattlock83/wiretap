# Maintain a bundle with your application

Wiretap can publish a maintained application knowledge bundle at `docs/knowledge`.
Commit that directory with the application, tests and supplied contracts it
documents. A PR can then review the changed code and selected evidence together.
Publication implements the [application knowledge draft](spec/README.md), version
`1.0.0-draft.1`. Immutable 3.1 recordings remain separate inputs under `artifacts`.

## Declare identities and selection

Check in a publication configuration, for example `.wiretap-publication.json`:

```json
{
  "version": 1,
  "bundle_id": "urn:my-team:orders:knowledge",
  "application": {"id": "urn:my-team:orders", "title": "Orders"},
  "inventory_scope": "declared-and-observed",
  "subjects": [
    {"id": "application", "kind": "application", "key": "orders", "title": "Orders"},
    {"id": "api:orders", "kind": "provided-api", "key": "orders", "title": "Orders API", "parent_id": "application", "contract_ids": ["contract:orders-v1"]},
    {"id": "operation:orders:create", "kind": "operation", "key": "create-order", "title": "Create order", "parent_id": "api:orders", "protocol": "http", "operation": "POST /orders", "contract_ids": ["contract:orders-v1"]},
    {"id": "test:orders:create", "kind": "test", "key": "create-order", "title": "Create order", "parent_id": "application"}
  ],
  "contracts": [
    {"schema_id": "orders", "id": "contract:orders-v1", "key": "orders-v1", "subject_ids": ["api:orders", "operation:orders:create"], "origin": "supplied"}
  ],
  "tests": [{"id": "test:orders:create", "name": "creates an order"}],
  "selection": {"mode": "replace-selected-tests", "scope": "complete", "test_ids": ["test:orders:create"]}
}
```

`schema_id` matches the captured schema ID, normally the SUT `id` or dependency key
in `.wiretap.yaml`. Subject and contract IDs belong to one bundle namespace.
Declare an application subject, the provided APIs/dependencies you want to document,
their operations, and the logical tests. Empty API, dependency, test and contract
inventories are supported. The publisher supplies the conventional `page` paths
and defaults subject `status` to `active`.

IDs and keys persist across executions, contract edits and display-title changes.
Choose explicit unique keys; reserved device names, collisions and oversized keys
are errors. Paths never acquire hashes or random suffixes to resolve a collision.
Omitted subjects are retained from the previous publication. Remove a subject
explicitly with `removed_subjects`, using its `id`, `kind`, `key`, `path` and a
`reason`. IDs and paths reserved by tombstones cannot be reassigned.

## Publish, validate and check

After finishing or interrupting a capture run:

```bash
wiretap publish --bundle artifacts/<run-id> --config .wiretap-publication.json
wiretap validate --bundle docs/knowledge
wiretap check --bundle artifacts/<run-id> --config .wiretap-publication.json
```

Use `--output` to select another publication directory. Source and destination
must be separate, non-overlapping directories. `publish` and `check` produce the
same candidate. Check mode leaves the destination untouched, reports changed
paths and reasons, and exits with status 1 if publication would change anything;
it exits 0 when current. Neither command reruns tests.

The result has readable paths:

```text
docs/knowledge/
  bundle.json
  publication.json
  index.md
  system.md
  apis/orders/api.md
  apis/orders/operations/create-order.md
  tests/create-order/test.md
  contracts/orders-v1/manifest.json
  contracts/orders-v1/orders.openapi.yaml
  evidence/tests/create-order/primary/attempt.json
  evidence/tests/create-order/primary/recording.json
```

Frozen OpenAPI files contain normalized JSON, which is also valid YAML, and
retain their original logical filenames. Imports preserve distinguishing relative
directories. The manifest declares the normalization/reference transformation.
Integrity hashes describe stored bytes independently of filenames.

Publishing identical inputs produces no changed bytes. A new execution updates
its evidence and affected hash registry; ordinary test/API/operation pages do not
acquire a new timestamp or run ID. A contract edit updates the package and actual
attempt bindings at the existing API and operation paths.

To publish automatically on successful `/v1/finishRun`, opt in in `.wiretap.yaml`:

```yaml
publication:
  config: .wiretap-publication.json
  output: docs/knowledge
```

Both paths resolve relative to `.wiretap.yaml`; `output` defaults to
`docs/knowledge`. The response includes `publication_path` and `run_archive_path`.
A publication error preserves the archive for an explicit retry using `publish`.
Process interruption preserves an interrupted archive; use the explicit publisher
after inspecting it. Automatic publication does not run on process shutdown.

## Logical tests, retries and partial runs

New integrations can send logical identity directly. In JavaScript:

```js
await runtime.setupTest({
  name: "creates an order",
  knowledge: { testId: "test:orders:create", key: "create-order", attemptOrdinal: 0 }
});
```

In Go:

```go
ordinal := 0 // Assigned by the harness; increment for actual retries.
session := runtime.StartTest(t, wiretap.TestOptions{
    Knowledge: &wiretap.TestKnowledgeIdentity{
        TestID: "test:orders:create", Key: "create-order", AttemptOrdinal: &ordinal,
    },
})
```

The HTTP field is `application_knowledge`, with `test_id`, optional `key`,
`execution_group` and `attempt_ordinal`. It is retained under testcase
`meta.application_knowledge` in the archive. A logical ID must resolve to a declared
test subject; an explicitly supplied key must match its stable mapping. Variants
use distinct logical IDs. A `tests[].name` mapping supports existing integrations
without these fields; ambiguous names require explicit logical identity.

All attempts for each selected logical test in the input run are retained,
including failures, skips and unfinished registered attempts. Multiple attempts
require harness ordinals or `tests[].attempt_ordinals`, an explicit mapping from
actual attempt IDs to zero-based ordinals. Completion order never assigns retry
identity. The default group has `primary`, `retry-1`, etc. Named groups use
`<group>-primary` and `<group>-retry-<ordinal>`. Groups can be set through the SDK
or `tests[].group`. Ordinals within each group must be contiguous from zero.

For a partial update, set `selection.scope` to `partial`, list exactly the selected
test IDs, and describe the policy in `selection.reason`. Unselected test evidence
retains its original bytes and source bindings. A selected test without a completed
capture receives an explicit unknown/unavailable result, preserving a registered
pending attempt's ID when available. Without any observed attempt, its unavailable
selection record explicitly declares that no execution was observed; it is not a
passing test or a fabricated protocol exchange.

Captured `test_source` and `sut_source` facts become distinct source-role records.
Missing facts stay `unknown`. Configure optional `sources` and `target_source_ids`
to declare the intended target revision separately from the actual inputs to
retained observations. Publication never infers exact reproducibility from an
ordinary dirty base commit or relabels old evidence with the current target.

## Contract revisions and existing archives

New OpenAPI archives retain logical source names for publication. Older archives
that only contain `doc-<hash>.json` names need an explicit readable mapping:

```json
"files": {
  "doc-<root-name>.json": "openapi.json",
  "doc-<import-name>.json": "schemas/order.json"
}
```

These names come from the input contract package manifest. Unsafe or colliding
source names also require explicit mappings. The publisher rewrites OpenAPI
imports consistently. AsyncAPI packages must retain their original relative names
and have a frozen local reference closure; unresolved remote imports are rejected.
Other explicit packages retain their source bytes. Markdown source data requires
an explicit non-`.md` mapping so it is not mistaken for an OKF concept.

If retained unselected evidence pins an older contract, give a changed contract a
distinct explicit revision ID/key, such as `contract:orders-v2` / `orders-v2`.
Never overwrite the old slot while its evidence still requires it. Optional
`source_sha256` disambiguates mappings when a captured schema ID has multiple byte
revisions. Contract slot keys cannot change while retaining the same contract ID.

Intentional subject key/path changes use `migrations`, with `subject_id`, `from`,
`to`, and `reason`. List every affected primary page, including operation pages
whose parent moves. The publisher retains deprecated forwarding concepts and
persists migration records. Moving a test requires selecting that test so its
evidence moves with it. A later explicit retirement preserves forwarding links
to a deprecated retirement page where needed.

## Ownership, recovery and supported roles

The file registry distinguishes generated ownership from authored content.
Wiretap preserves unlisted files, authored bytes and unknown optional fields.
An edited generated file or another producer's ownership blocks an update.
Resolve that conflict explicitly before regenerating. Every Markdown concept,
including unlisted team notes, needs OKF frontmatter with a nonempty `type`.
Invalid authored content is reported and preserved.

Publication takes a sibling lock, validates a complete candidate, checks for
concurrent edits, and stages the manifest last before swapping directories. After
a crash, inspect `<output>.wiretap-recovery` and the sibling staging directory.
The recovery directory contains the previous snapshot if interruption occurred
during the swap. Restore the complete prior directory before clearing a stale
`<output>.wiretap-lock`; do not combine partial snapshots. A reader can retry
during the brief directory-swap interval.

`validate` reports base OKF, application, neutral evidence and measurement
assessments separately. Plain OKF can be valid with no application manifest.
Unknown required capabilities/versions are unsupported; optional unknown profiles
remain integrity checked and are listed as unsupported semantics. Filesystem
aliases and unsafe paths are rejected. Current limits are 10,000 files, 128 MiB per
file, 512 MiB in total, 256 JSON nesting levels, 64 YAML nesting levels, 100,000
YAML nodes per document, and
bounded regular-expression processing.

The [Wiretap recording profile](spec/wiretap-recording.md) preserves HTTP, native
gRPC and ordered event evidence, assertions, validation, payload limits and actual
boundary kinds. `report --format okf` validates an existing repository publication;
`export --format json|yaml` exports its verified manifest, provenance, envelopes
and supported recordings. Existing run archives retain their viewer/rebuild and
coverage export behavior and support [Postman collection export](okf-bundles.md#postman-collections)
from captured HTTP tests. Postman export requires a run archive rather than a
repository publication.

Repository publication does not currently generate a measurement profile or
aggregate coverage across retained observations. Measurements from other tools
are reported as unsupported semantics and require explicit regeneration before
Wiretap updates that publication. The independent producer/consumer exchange
required for a stable specification release remains a separate release gate.
