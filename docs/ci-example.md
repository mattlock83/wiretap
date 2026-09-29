# CI Example

This is a minimal GitHub Actions workflow that runs Go tests, JS SDK tests,
and the capture example, then uploads its OKF bundle.

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

      - name: Capture example
        run: ./examples/run-basic-rest-e2e.sh

      - name: Upload artifacts
        uses: actions/upload-artifact@v4
        with:
          name: test-artifacts
          path: artifacts/
```

Notes:
- The service writes a bundle to `./artifacts/<run-id>/` by default. Override
  the output root with `WIRETAP_OUTPUT_DIR` in CI. Upload the whole bundle so
  `index.md`, `viewer/index.html`, `evidence/`, `schemas/`, and `payloads/` remain together.
- Body capture defaults to 4 MiB per request or response. Set
  `WIRETAP_MAX_CAPTURE_BYTES` to another non-negative byte count when CI
  artifact size requires a different bound; `0` means unlimited.
- If HTTPS MITM is used in CI, add the proxy CA to the trust store (see `docs/https-trust.md`).
