# Jest example

The [existing Jest suite](existing-jest/README.md) adds Wiretap to a pinned
Express API with Jest 29, Supertest, an in-memory mock datastore and 120 existing
tests. The integration keeps the test files and assertions unchanged, captures
HTTP traffic, and writes coverage reports and Arazzo workflows.

From the repository root, with Git, Go 1.26+ and Node.js 22+:

```bash
node examples/existing-jest/run.mjs
```

The script builds Wiretap, installs the local Jest SDK, runs the original suite
and then runs it with capture enabled. It checks that all test outcomes stay the
same and validates the exported Arazzo workflows against their schema and raw
capture evidence. The demo intentionally retains its existing test failures;
the wrapper succeeds when outcome preservation and artifact checks pass.

Keep the complete printed artifact directory, including `bundle.json`, `tests/`,
contracts, payloads and exports. Its receipt gives exact output paths. See the
[Jest integration guide](../sdk/js/docs/jest.md) for your own suite and the
[assumption checker](../docs/assumption-checks.md) for comparing captured HTTP
mock assumptions with a provider's passing test evidence.
