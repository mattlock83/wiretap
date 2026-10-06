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
  `bundle.json`, `index.md`, `tests/`, `contracts/`, `payloads/`, and `docs/` remain together.
- Body capture defaults to 4 MiB per request or response. Set
  `WIRETAP_MAX_CAPTURE_BYTES` to another non-negative byte count when CI
  artifact size requires a different bound; `0` means unlimited.
- Upload after failures as well as successes: failed attempts are useful evidence.
  This example uses `if: always()` for the upload step.

- If HTTPS MITM is used in CI, add the proxy CA to the trust store (see [HTTPS trust](https-trust.md)).

For [maintained repository bundles](repository-bundles.md), check in the identity
and selection configuration together with `docs/knowledge`. After producing a
closed run, run `wiretap check --bundle <run> --config .wiretap-publication.json`.
It fails CI when the tracked publication differs from the candidate. Run the same
inputs through `wiretap publish` locally and include the resulting diff in the PR.
Keep the whole immutable source run as a CI artifact, including failed attempts.
