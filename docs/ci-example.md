# Keep test evidence in CI

Run the same test command locally and in CI. The runner adapter, Go `Run`, or
custom parent harness owns startup, readiness, finalization and shutdown.
Workers sharing one runtime own only their attempts; managed Go packages each
own an independent runtime and archive.
Use synthetic data and dummy secrets because captured traffic is retained
unchanged.

## Standard Go tests

This job runs the [Go example](../examples/go-suite/README.md) from a Wiretap
checkout. Its `TestMain` calls `wiretap.Run(m)` and its shared test helper calls
`wiretap.StartTest(t)`. In your repository, install the local SDK/binary and keep
the same `go test -count=1 ./...` command.

```yaml
name: Go API tests
on: [push, pull_request]
jobs:
  api-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.10'
      - run: go build -o dist/wiretap ./cmd/wiretap
      - name: Run tests and generate exports
        env:
          WIRETAP_BINARY: ${{ github.workspace }}/dist/wiretap
          WIRETAP_OUTPUT_DIR: ${{ github.workspace }}/artifacts
        run: go -C examples/go-suite test -count=1 ./...
      - name: Preserve evidence, including failed tests
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: go-api-test-evidence
          path: artifacts/
          if-no-files-found: error
```

Each package's run directory contains `last-run.json`, `wiretap.log` and the full
archive. `-count=1` prevents a cached test result from skipping capture. Independent
Go packages may share an output parent because every receipt belongs to its own
archive.

## JavaScript runners and a custom Node harness

For Jest, Playwright Test or Mocha, use the corresponding
[runner adapter](../sdk/js/README.md), set `WIRETAP_BINARY`, run your ordinary
test command, and upload its configured output directory unconditionally.
Independent suites retain their own `last-run.json` and bounded `wiretap.log`
inside each archive. The JavaScript root-level latest receipt is advisory; use
the exact run receipt when suites share an output root.

The workflow below runs the [plain Node example](../examples/node-suite/README.md)
from a Wiretap checkout. In your project, install your locally built binary and
packed SDK, then run your copy of the same harness.

```yaml
name: API tests
on: [push, pull_request]
jobs:
  api-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.10'
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - name: Build Wiretap and install its SDK
        run: |
          mkdir -p dist
          go build -o dist/wiretap ./cmd/wiretap
          npm ci --prefix sdk/js
          cd examples/node-suite
          npm install --no-save ../../sdk/js
      - name: Run API tests and generate exports
        env:
          WIRETAP_OUTPUT_DIR: ${{ github.workspace }}/artifacts
        run: node examples/node-suite/run-tests.mjs --binary dist/wiretap
      - name: Preserve evidence, including failed tests
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: api-test-evidence
          path: artifacts/
          if-no-files-found: error
```

The managed owner writes `<bundle_path>/last-run.json` with exact `bundle_path`
and `outputs` paths. The Node example also writes an advisory
`artifacts/last-run.json` for its single-suite harness. Each `artifacts/<run-id>/` contains raw recordings, contracts, payloads,
coverage, Postman collections and `exports/result.json`. Keep the whole directory
so another tool can verify and export it offline. Do not select the newest
folder by timestamp when the finalization response already identifies the run.

The harness's managed owner attempts finalization after the test process exits. An
original nonzero test exit code is retained if cleanup also fails; cleanup or
export errors fail a suite that otherwise passed. Upload runs unconditionally,
so failure evidence remains available.

A hard panic, process kill or runner timeout can leave an interrupted archive
without completed exports. Unconditional upload preserves the evidence that
was written; it cannot recover work lost when the process was terminated.

Set a unique output root per concurrent CI job. Body retention defaults to
4 MiB per HTTP request/response. `WIRETAP_MAX_CAPTURE_BYTES` chooses another
non-negative byte count (`0` is unlimited); truncated evidence stays explicit.
For HTTPS interception, configure the [proxy CA](https-trust.md).

The repository's [package smoke](../scripts/package-smoke.mjs) verifies this
harness from a fresh external installation with passing tests, deliberate
negative requests, and a failing framework cleanup hook. No package publishing
is needed to run it.

## Gate consumer/provider assumptions offline

Upload each complete run, including `exports/postman/`, under its stable
application ID in your shared artifact store. For the check job, restore the
selected peer snapshots into a local application-folder root. Keep the local
suite's exact completed run as the anchor. Artifact upload/download remains
part of your existing CI or Artifactory tooling.

Use a known run ID and tested revision for each application/API. Consumer
provider mappings can pin `sut_source.version`, but two runs matching that pin
are still ambiguous. Restore one intended peer snapshot per selected
application/API; Wiretap does not choose a newest folder or merge evidence
from historical runs. The check needs no running server and executes no
Postman scripts.

The example assumes the local consumer is in `evidence/orders-run/`, mapped
providers have been restored beneath `api-evidence/<application-id>/`, and
`wiretap` is on `PATH`. Keep results outside all input archives and outside
`api-evidence/`. Use a fresh result directory per job so an old report cannot
be mistaken for a new result.

```yaml
- name: Check captured assumptions
  shell: bash
  run: |
    mkdir -p assumption-results
    set +e
    wiretap check consumer \
      --consumer evidence/orders-run \
      --artifacts api-evidence \
      --format json \
      --output assumption-results/check.json \
      2> assumption-results/check.stderr
    check_status=$?
    set -e
    cat assumption-results/check.stderr >&2 || true
    exit "$check_status"
- name: Preserve the check and its inputs
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: assumption-check-evidence
    path: |
      assumption-results/
      evidence/orders-run/
      api-evidence/
    if-no-files-found: error
```

The explicit status capture keeps the check as the gate even while saving its
diagnostics. Exit `0` means every selected mock example has supporting provider
evidence; `1` means an incompatible, uncovered or unverified result; `2` means
invalid input or flags. Input errors are written to stderr and may prevent a
JSON report from being written. Preserve diagnostics even when no report exists.

For a provider repository's gate, restore the intended consumer snapshots
beneath application folders and use:

```sh
wiretap check provider \
  --provider evidence/inventory-run \
  --artifacts api-evidence \
  --format json \
  --output assumption-results/check.json
```

Consumer mode checks every local mock dependency through its provider mapping.
Provider mode finds consumer mappings to the local application/API. Both modes
enforce the same relationship, returning one aggregate report with pair details
in `comparisons`. No matches, missing mappings, missing peers or ambiguous
snapshots fail with exit `2`; they do not produce a passing empty report.
Provider mode includes mappings pinned to a different provider version and
reports them as unverified, rather than silently ignoring those consumers.

Add `--dependency inventory` to scope consumer discovery to that local mock ID.
Provider discovery always checks all mapped consumer dependencies. To compare
one selected relationship, use `--consumer` and `--provider` together, omit
`--artifacts`, and add `--dependency` when needed. Preserve that pair's two
archives with the report.

Before making a check mandatory, align synthetic request fixtures between the
suites. Different IDs or body values are uncovered scenarios. Decide which
volatile headers are irrelevant and add explicit `--ignore-header` flags if
needed; those exclusions affect requests and responses and are reported in
`ignored_headers`. Payload fields have no ignore rule. An extra optional response
field can therefore fail this strict example comparison without proving a
runtime break.

Keep the frozen OpenAPI 3.0 contracts with HTTP evidence when gating on contract
agreement. When both HTTP contracts are supplied, rules on exercised operations
and selected statuses/media are compared even when both recorded values are
valid: an added enum value or a
relaxed limit can fail as declared-rule drift. Inspect those changes rather
than assuming every mismatch is behaviorally breaking. A passing match with
both HTTP schemas absent is marked `contract_check: "unavailable"`; the text
report counts these example-only matches. Consumer HTTP dependency schemas are
optional: when only the provider's schema is present, both actual exchanges
validate under it and the match is marked `openapi_3_0_provider_contract`.
Independent consumer/provider rule drift is checked only when both schemas are
present. A provider schema missing while the consumer supplies one, or an
unsupported required contract, leaves the comparison unverified.
Native gRPC also requires equal declarations in the selected method's reachable
message and enum graphs, plus successful observed wire cross-decoding. This can
catch a newly allowed enum value even when no example returns it. Custom
validation options are compared as opaque declarations, so retain tests that
assert their actual validation behavior.

Retain the exact snapshot identities and manifest digests used by the check.
Keep stable application/API IDs and select the intended tested revision and
run; display names and moving `latest` labels do not establish that identity.
The CLI reads downloaded, extracted local archives. See
[sharing provider snapshots](assumption-checks.md#share-provider-snapshots) for
the required application-folder layout and discovery rules. Provider-contract
validation of a schema-free consumer
capture does not retroactively give that archive contract coverage, and a spec
without a matching passing provider test still leaves the scenario uncovered.

Read [issues the check finds](assumption-checks.md#issues-the-check-will-find),
[what it cannot establish](assumption-checks.md#what-the-check-will-not-establish)
and [interpreting results](assumption-checks.md#interpreting-results) before
setting the gate's release policy. The check complements your suite's assertions
and its expected-call checks; it cannot infer those from captured traffic.

## Python and split CI jobs

Install the local Python SDK with its pytest extra, set `WIRETAP_BINARY`, and run
`python -m pytest` with the `wiretap` fixture. The [pytest example](../examples/pytest-suite/README.md)
includes a real local application and its configuration. Upload complete archives
on failed jobs as well as successful jobs. Each xdist worker owns an independent
runtime; [run sets](run-sets.md) group completed members from the same build.

Use [coverage policies and baseline comparison](report-policies.md) for explicit
measurement gates. `wiretap check ... --format junit --output assumptions.xml`
produces a CI test report; `--format markdown` produces a review summary. Both
preserve the underlying check verdict. JSON retains the complete diagnostics.
