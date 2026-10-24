# CI example

This is a minimal GitHub Actions workflow that runs Go tests, JS SDK tests,
and the capture example in this repository, then uploads the bundles.
For another project, substitute its existing harness and test command while
keeping complete bundle directories as CI artifacts.

```yaml
name: ci

on:
  push:
    branches: [ main ]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Setup Node
        uses: actions/setup-node@v4
        with:
          node-version: "20"

      - name: Go tests
        run: go test ./...

      - name: JS SDK tests
        working-directory: sdk/js
        run: npm ci && npm test

      - name: Capture the example
        run: ./examples/run-basic-rest-e2e.sh

      - name: Upload artifacts
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: test-artifacts
          path: artifacts/
```

Notes:

- The service writes a bundle to `./artifacts/<run-id>/` by default. Override
  the output root with `WIRETAP_OUTPUT_DIR` in CI. Upload the whole bundle so
  `bundle.json`, `tests/`, `contracts/` and `payloads/` remain together.
- Body capture defaults to 4 MiB per request or response. Set
  `WIRETAP_MAX_CAPTURE_BYTES` to another non-negative byte count when CI
  artifact size requires a different bound; `0` means unlimited.
- Upload after failures as well as successes: failed attempts are useful evidence.
  This example uses `if: always()` for the upload step.

- If HTTPS MITM is used in CI, add the proxy CA to the trust store (see [HTTPS trust](https-trust.md)).

Generate a JSON or YAML coverage report from a completed artifact with
`wiretap report --bundle <run> --output coverage.json`. Keep the complete raw run
as a CI artifact, including failed attempts, so another process can verify and
export it offline.

For a suite that should always export both formats, configure:

```yaml
outputs:
  coverage: [json]
  postman: true
```

The suite owner finalizes once after all workers finish. Save the returned
`bundle_path` and `outputs` paths directly; `exports/result.json` records successes
and export errors inside that run. Upload this archive after test failures too.
An original test failure must retain its exit code even if finalization also fails.
