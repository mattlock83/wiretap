# Combine packages or CI shards from one build

Keep every package or worker's archive intact. A run set names the completed
archives belonging to one tested build and pins their `bundle.json` hashes:

```sh
wiretap run-set --bundle artifacts/shard-a --bundle artifacts/shard-b \
  --output provider-runs.json
wiretap report --run-set provider-runs.json --output combined-coverage.json
```

Set `application.id`, `sut.id`, and a nonempty `sut_source.revision` or
`sut_source.build_id` in the project configuration before capturing multiple
members. Members must agree on application/API identity, build and test source
metadata, boundary kinds, provider mappings, and frozen contract revisions.
An incomplete run, duplicate run, changed manifest or mixed contract/build is
rejected. A single-member set needs application/API identity but no build field.

The [manifest schema](run-set.schema.json) contains relative paths, so copying
the manifest and its referenced directory tree together preserves the links.
Create the manifest after downloads or moves if the layout has changed. The
command does not copy, rewrite or select the newest archives. Limits are 64
members per set and 256 archive pairs per assumption check.

The JSON coverage output has `report_version: "1.0"`, producer metadata,
`scope: "same_build_run_set"`, the original per-run reports in `members`, and
`conditions` grouped by API, schema, origin and stable target key. A condition
is covered when a member supplies eligible evidence. Otherwise unknown states
remain visible. Every evidence reference retains its original run/attempt/flow;
the denominator is counted once per condition. Inspect member reports for
validation violations, missing denominators and other measurement limits.
This envelope is distinct from a single-archive coverage report.

## Check consumer and provider shards

```sh
wiretap run-set --bundle consumer/package-a --bundle consumer/package-b \
  --output consumer-runs.json
wiretap check --consumer-set consumer-runs.json \
  --provider-set provider-runs.json --dependency inventory --format json \
  --output assumptions.json
```

The two sets represent different applications/builds. Each set must be internally
consistent. A consumer mock exchange is supported when a complete passing
provider test in any selected provider member captured the matching request and
paired response and passes the contract checks. Wiretap never pairs a request
from one exchange with a response from another. All selected projections are
checked against their raw archives, even when another shard supplies a match.
Both sides use Arazzo workflows and source companions from their member archives.

Every member with eligible workflows must retain `exports/arazzo/arazzo.json`
and its source companions. An empty shard with no selected traffic and no
eligible workflows may omit the default document; an existing document is stale
when the archive has no generated workflows. Complete archives remain required
for response evidence, passing-test provenance and frozen contracts. Missing,
stale or altered documents or companions fail input verification; selected
unsupported traffic cannot be silently certified. Each member is also subject
to the [strict whole-run Arazzo export limits](assumption-checks.md#arazzo-check-inputs).
Verification does not execute Arazzo workflow expressions.

Use `--consumer-set` and `--provider-set` together. Every member's projection
remains under its own `exports/arazzo/` directory; a run set does not synthesize
a combined replay or change test identities. Run sets do not accept per-member
Arazzo path overrides. Keep original archives and exports alongside the manifest.

Aggregate coverage is available as JSON. HTML dashboards currently describe one
archive; inspect each member's dashboard for test-level requests and responses.
