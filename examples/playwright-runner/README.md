# Playwright API suite

This runnable example uses Playwright's `request` fixture with a local Todo API.
No browser installation is needed. The existing global setup starts the API;
Wiretap owns its capture process, per-test sessions and exports.
Use Playwright Test 1.64.1 or later within 1.x and Node.js 22 or later.

From the repository root, install the SDK development dependencies and build the
local binary once:

```sh
npm --prefix sdk/js ci
go build -o dist/wiretap ./cmd/wiretap
WIRETAP_BINARY="$PWD/dist/wiretap" node sdk/js/node_modules/@playwright/test/cli.js test --config examples/playwright-runner/playwright.config.cjs
```

The run creates `artifacts/last-run.json`, a Wiretap artifact, JSON/text API
coverage reports, and a Postman collection inside this example directory.
Both tests pass. The deliberately invalid request remains visible as a request
schema mismatch, separately from the test's passing outcome.

For an existing suite, use the package's `@wiretap/sdk/playwright` import instead
of this example's repository-relative import. See the
[Playwright integration guide](../../sdk/js/docs/playwright.md).

## Browser and parallel capture check

The optional browser check verifies that a real browser navigation reaches the
API with that test's trace context and the existing configured headers. Run it
alongside the API tests using two workers:

```sh
node sdk/js/node_modules/@playwright/test/cli.js install chromium
WIRETAP_BINARY="$PWD/dist/wiretap" WIRETAP_PLAYWRIGHT_BROWSER=chromium WIRETAP_PLAYWRIGHT_PARALLEL=1 node sdk/js/node_modules/@playwright/test/cli.js test --config examples/playwright-runner/playwright.config.cjs
```

Use `WIRETAP_PLAYWRIGHT_BROWSER=chrome` to use an installed Google Chrome instead.
The check requires three passing tests, matching trace headers in both the API
and browser responses, and a completed `artifacts/last-run.json` with no output
errors. The parallel example explicitly enables traced sessions; its API has no
downstream HTTP calls requiring additional propagation.
