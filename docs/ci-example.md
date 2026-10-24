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
