# Capture artifacts and exports

Wiretap writes one portable directory per run under `./artifacts` by default.
Set `output_dir` in `.wiretap.yaml` or `WIRETAP_OUTPUT_DIR` to choose another
parent directory. SDK/API responses return the exact `bundle_path`.

Each test teardown commits an immutable JSON recording. The Jest integration finalizes the run and its exports after test
cleanup, then stops its Wiretap process. With a project-owned runtime, one suite owner calls
`/v1/finishRun` after every worker finishes. Keep the complete directory when sharing it or
uploading it to CI so its contracts and payload references remain valid. Managed
runs also write `last-run.json` with exact archive/export paths and a bounded
`wiretap.log` inside each run directory. This isolates receipts for concurrent
Jest runs. Use the archive-local receipt when selecting a particular run.
Preserve the complete run directory.

After successful finalization, Jest prints the archive and exact
coverage and Arazzo paths from the completion receipt to stderr.
Failed tests keep their original
outcome even when capture and export finish successfully.

```text
artifacts/<run-id>/
  bundle.json             run identity, API/dependency inventory and test references
  contracts/              frozen contracts, imports and integrity manifests
  tests/                  immutable JSON recording per test attempt
  payloads/               retained body bytes
```

Read `bundle.json` for `provided_apis`, `dependencies`, `tests`, pending attempts,
run state and source provenance. Follow each test's `path` to its recording.
Configured but uncalled boundaries remain in the inventory. Each recording
contains the test outcome, traffic, validation results, diagnostics and references
to retained payloads. See the [format reference](bundle-evidence.md) for integrity
rules and HTTP fields.

Application identity is separate from API identity. The format can represent
provider-only, consumer-only and mixed systems, while the current runtime setup
remains centred on a SUT. Empty inventories describe configured and observed
scope; they do not prove that no other APIs or dependencies exist.

## Verify consumer/provider examples

Use `wiretap check` with an explicit pair selected by `--consumer` and
`--provider`, or [same-build run sets](run-sets.md) selected by `--consumer-set`
and `--provider-set`. Every run
needs its complete archive, Arazzo workflows and their OpenAPI source
companions on both sides. The checker verifies those projections against their
raw evidence, then requires each selected mock request and paired
response to have compatible evidence from a passing provider service test.
The provider's frozen OpenAPI 3.0 contract can validate both concrete exchanges
without a consumer dependency spec; that match is marked
`openapi_3_0_provider_contract`. With both contracts present, exercised rules
must also agree and each exchange validates under its own schema. With both
HTTP schemas absent or omitted,
the report marks a verified match as example-only. Uncovered scenarios fail the
check. See [assumption checks](assumption-checks.md) for command examples,
dependency selection, JSON results and limits.
Provider-contract validation does not rewrite the consumer archive's original
unknown contract coverage. A matching passing provider test is still required.

## Generate coverage

`report` reads and verifies captured evidence, frozen contracts and retained
payloads. It works offline without the original workspace or servers and
defaults to JSON coverage. Start with the compact text summary or HTML
dashboard, and use JSON for machine processing:

```bash
wiretap report --bundle ./artifacts/<run-id> --format text
wiretap report --bundle ./artifacts/<run-id> --format html --output coverage.html
wiretap report --bundle ./artifacts/<run-id> --output coverage.json
```

Omit `--output` to write to stdout. The report includes operation coverage,
validation diagnostics and detailed condition results under `contract_coverage`.
These reports are derived from the raw artifact and never become evidence inputs.

Text limits contract, operation and condition detail lists and names the omitted
counts. JSON retain the complete report; truncation never changes totals.

Save output files outside the capture archive or under its `exports/` directory.
Reports and Arazzo exports reject destinations that replace raw recordings,
contracts, retained payloads or the manifest, including symlink and hardlink
aliases. The archive's `exports/` directory must be a real directory; a source
file stored there through an archive alias remains protected. These checks also
apply to automatic exports and `--format all` regeneration.

The text summary keeps operation counts and condition families separate. For
example, this illustrative excerpt makes an untested operation visible without
reading a full JSON report:

```text
Attempts: 2 (2 passed, 0 failed, 0 skipped)
Captured interactions: 2 (2 HTTP, 0 other)

Provided APIs
  orders: 1/2 operations exercised
    Untested: POST /orders

Contract conditions (covered / gaps / unknown / unsupported / excluded)
  orders (provided, service)
    enum: 1 / 1 / 0 / 0 / 0
```

The summary warns when recorded tests captured no network traffic and points to
client routing and trace propagation. This is a diagnostic, not a new test
failure. If attempt details are missing, the count is explicitly unavailable.

The JSON report retains all details behind those counts. A condition gap means
no qualifying observation was found; an unknown means the evidence could not
support a decision. The summary does not collapse them into one percentage.

Offline loading uses finite [archive loading budgets](archive-loading.md),
including limits on individual files, cumulative bytes and file reads.
[Run sets](run-sets.md) combine coverage and consumer/provider checks across
selected shards from the same build.

Operation coverage shows which declared operations were observed. Contract
coverage measures finite, supported cases from supplied specs, such as an enum
value, optional-field absence or a declared numeric boundary. One test attempt
can exercise many conditions, and multiple attempts can contribute observations
to one condition. Conditions observed together remain linked by message; this is
not exhaustive coverage of every possible combination.

| State | Meaning |
| --- | --- |
| Covered | A complete passing attempt supplied valid evidence for the condition. |
| Gap | No qualifying observation covers the condition. |
| Unknown | Evidence or evaluation is inconclusive. |
| Unsupported | The measurement rules cannot evaluate this condition. |
| Excluded | The target is outside applicable scope or has been shown infeasible. |

A documented, valid error response can earn response-condition coverage.
Framework test outcomes remain independent of contract-validation findings, so
a deliberate invalid request and asserted `400` can be a passing test.
Invalid requests do not earn valid-request coverage. An observed error status
alone does not establish that the test asserted rejection. Incomplete, truncated
or undecodable messages cannot earn successful payload coverage. The same limit
applies to legacy-redacted evidence.
See [contract coverage rules](contract-coverage-rules.md) for exact semantics.

A dependency response records what the declared `mock`, `service` or `unknown`
target returned. A mock response is a consumer assumption; it does not prove
agreement with the real provider or identify which fields the application used.
Wiretap captures and reports existing tests; it does not generate or execute
additional cases.

Capture retains up to 4 MiB per HTTP body by default. Retained payloads can still
be truncated; older archives may also contain redaction metadata. Current
capture retains synthetic data and dummy secrets unchanged.
Exporting a report cannot restore omitted bytes. See
[troubleshooting](troubleshooting.md) for capture limits and integrity errors.

## Offline HTML dashboard

Automatic coverage outputs include `exports/coverage.html`. Open it directly in
a browser. The self-contained dashboard needs no report server, external scripts
or network requests. It shows operation and contract-condition coverage,
measurement gaps, test outcomes, captured HTTP traffic and Arazzo workflows.
Coverage evidence links conditions back to the test attempts and flows that
exercised them. Tests and API contract validation retain their separate results.

Rebuild it from a complete archive:

```sh
wiretap report --bundle ./artifacts/<run-id> --format html --output coverage.html
```

Or render the exported Wiretap coverage JSON, optionally supplying OpenAPI specs
and an Arazzo document:

```sh
wiretap report --coverage-json ./coverage.json \
  --spec ./orders.openapi.yaml --spec ./inventory.openapi.yaml \
  --arazzo ./arazzo/arazzo.json --format html --output report.html
```

`--spec` is repeatable. Specs and Arazzo are optional supplementary sources;
coverage JSON supplies the measured counts, conditions and test evidence.
Standalone rendering does not recalculate coverage or certify consumer/provider
compatibility. Supplied files are embedded safely without fetching remote
references or executing workflows. Keep the complete JSON report when sharing
these inputs; the text summary deliberately omits detail.

Archive rendering derives workflows from the verified Wiretap JSON and frozen
specs, so it does not require a pre-existing Arazzo export. If eligible traffic
cannot be represented as Arazzo, the dashboard retains HTTP coverage and test
evidence and reports the workflow limitation. Non-JSON traffic remains visible
even when it has no Arazzo workflow. Body availability, truncation and other
measurement limits remain explicit; rendering cannot recover omitted bytes.

The earlier dashboard was removed in commit
[`b793fa5`](https://github.com/mattlock83/wiretap/commit/b793fa525c31275bd4e05c0bea20383e9664e021),
“Remove OKF generation and publication.” This implementation restores reporting
directly over Wiretap JSON, specs and Arazzo.

## Arazzo workflows

Export captured JSON/REST requests as an Arazzo document and portable OpenAPI
source files:

```bash
wiretap report --bundle ./artifacts/<run-id> --format arazzo \
  --output-dir ./arazzo
```

The directory contains `arazzo.json` and its source companions. Keep them
together so relative source URLs resolve. `--output ./arazzo/orders.json` chooses
the document's filename and writes companions beside it. Use either `--output`
or `--output-dir`. Stdout is available only when no source companions are needed.
`wiretap report --format arazzo` accepts the same options.

Each recorded attempt becomes a workflow for each eligible boundary: the SUT
and dependencies marked `kind: mock`. Steps retain captured request order,
request values and response status expectations. Retries remain separate
workflows. These are observed test sequences; exporting them does not run them
or infer data dependencies between steps.

Workflow input defaults retain captured literals, including strings that resemble
Arazzo expressions. Steps also retain the complete request headers in
`x-wiretap-request-headers` and the captured URL in `x-wiretap-url`. Standard step
parameters cover query values and supported declared path, header and cookie
parameters. Configure authentication, content negotiation and server addresses
in the consuming runner; frozen sources retain their declared server settings.
Repeated or structured parameters, undeclared query parameters in frozen
operations, and incomplete or altered JSON captures fail export with a diagnostic.
Arazzo generation is strict across the whole run and has no omit-incomplete
mode. An incomplete or unrepresentable eligible JSON capture on any boundary
can prevent export and the regeneration used by assumption checks, even when
that boundary is unselected or another passing provider attempt is usable.

Frozen OpenAPI contracts supply operation references when available. For a
schema-free JSON API, the exporter creates an OpenAPI source describing observed
operations and labels it as observed evidence. It does not infer contract
constraints from examples. Nonempty request and response bodies require a JSON
content type and valid JSON. Empty-body HTTP operations require a JSON content
type or a matching frozen OpenAPI operation.
Non-JSON HTTP traffic is excluded. A run with no eligible workflows
does not produce an automatic Arazzo output.

Suite finalization automatically generates `exports/arazzo/arazzo.json` when
eligible traffic is present. `exports/result.json` returns its exact path in `arazzo` and source
paths in `arazzo_files`. `--format all` includes Arazzo when eligible JSON/REST
traffic is present. Export errors are reported independently from coverage errors.

Arazzo workflows are the projection inputs to JSON/REST assumption checks:

```sh
wiretap check --consumer ./consumer-run --provider ./provider-run \
  --dependency inventory
```

Both archives default to `exports/arazzo/arazzo.json` with companions beside it.
Keep complete archives: the checker uses their actual responses, passing-test
provenance and frozen contracts, and verifies the Arazzo document and companions
against a regenerated projection. It does not execute workflow expressions.
Missing, stale or altered inputs fail verification. Unsupported traffic in a
selected boundary cannot be silently excluded from a passing check. See
[Arazzo check inputs](assumption-checks.md#arazzo-check-inputs)
for explicit paths and run-set usage.

Automatic exports always include JSON and HTML coverage plus eligible Arazzo
workflows. There are no output-selection settings. Generate text explicitly with
`wiretap report --bundle <run> --format text`.

Coverage and Arazzo errors remain separate in `exports/result.json`; successful
coverage paths remain available when Arazzo export fails.
