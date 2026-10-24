# Keep test evidence in CI

Run the same harness locally and in CI. It owns startup, readiness, all test
workers, one finalization and shutdown. Each worker owns only its attempts.
Use synthetic data and dummy secrets because captured traffic is retained
unchanged, with no built-in redaction.

This workflow runs the [complete two-file Node example](../examples/node-suite/README.md)
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

The harness always attempts finalization after the test process exits. An
original nonzero test exit code is retained if cleanup also fails; cleanup or
export errors fail a suite that otherwise passed. Upload runs unconditionally,
so failure evidence remains available.

Set a unique output root per concurrent CI job. Body retention defaults to
4 MiB per HTTP request/response. `WIRETAP_MAX_CAPTURE_BYTES` chooses another
non-negative byte count (`0` is unlimited); truncated evidence stays explicit.
For HTTPS interception, configure the [proxy CA](https-trust.md).

The repository's [package smoke](../scripts/package-smoke.mjs) verifies this
harness from a fresh external installation with passing tests, deliberate
negative requests, and a failing framework cleanup hook. No package publishing
is needed to run it.
