# CI Example

This is a minimal GitHub Actions workflow that runs Go tests, JS SDK tests, and uploads artifacts.

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
          go-version: "1.22"

      - name: Setup Node
        uses: actions/setup-node@v4
        with:
          node-version: "18"

      - name: Go tests
        run: go test ./...

      - name: JS SDK tests
        working-directory: sdk/js
        run: npm test

      - name: Upload artifacts
        uses: actions/upload-artifact@v4
        with:
          name: test-artifacts
          path: artifacts/*.yaml
```

Notes:
- The core service writes artifacts to `./artifacts` by default. Override with `PHROND_ARTIFACT_DIR` in CI.
- If HTTPS MITM is used in CI, add the proxy CA to the trust store (see `docs/https-trust.md`).
