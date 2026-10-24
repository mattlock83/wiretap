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
Use a separate output root for independent concurrent JavaScript suites because
their `last-run.json` and `wiretap.log` describe the latest run in that root.

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

The harness writes `artifacts/last-run.json` with exact `bundle_path` and `outputs`
paths. Each `artifacts/<run-id>/` contains raw recordings, contracts, payloads,
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

After capturing both suites, restore the exact complete consumer and provider
archives into the check job, including their `exports/postman/` directories.
Select known run IDs or artifact versions from the tested revisions. Do not
substitute the newest folder or an unrelated green provider build. The check
needs no running application, mock server or provider, and executes no Postman
scripts.

The example below assumes `evidence/orders-run/` and
`evidence/inventory-run/` have already been restored and `wiretap` is on `PATH`.
The output directory is separate from both inputs. Use a fresh output directory
per job so an old report cannot be mistaken for a new result.

```yaml
- name: Check captured assumptions
  shell: bash
  run: |
    mkdir -p assumption-results
    set +e
    wiretap check consumer \
      --consumer evidence/orders-run \
      --provider evidence/inventory-run \
      --dependency inventory \
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
      evidence/inventory-run/
    if-no-files-found: error
```

The explicit status capture keeps the check as the gate even while saving its
diagnostics. Exit `0` means every selected mock example has supporting provider
evidence; `1` means an incompatible, uncovered or unverified result; `2` means
invalid input or flags. Input errors are written to stderr and may prevent a
JSON report from being written. Preserve diagnostics even when no report exists.

For a provider repository's gate, change `check consumer` to `check provider`.
Keep `--consumer` pointing to the consumer's mock archive and `--provider` to
the provider's service archive. Both modes enforce the same relationship; the
mode changes the viewpoint, not the direction of the assumptions. Run a separate
check for each consumer or dependency to protect.

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

Retrieve provider snapshots with your existing CI artifact tooling before this
step. Keep stable application/API IDs and pin the intended tested revision,
run and manifest identity; avoid display-name matching or a moving `latest`
artifact. The CLI reads downloaded local archives and does not fetch from a
remote store. See [sharing provider snapshots](assumption-checks.md#share-provider-snapshots)
for a suggested layout. Provider-contract validation of a schema-free consumer
capture does not retroactively give that archive contract coverage, and a spec
without a matching passing provider test still leaves the scenario uncovered.

Read [issues the check finds](assumption-checks.md#issues-the-check-will-find),
[what it cannot establish](assumption-checks.md#what-the-check-will-not-establish)
and [interpreting results](assumption-checks.md#interpreting-results) before
setting the gate's release policy. The check complements your suite's assertions
and its expected-call checks; it cannot infer those from captured traffic.
