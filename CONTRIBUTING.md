# Contributing

Use Go 1.26.10 and Node 22 or 24. Keep the existing test framework and clients
when adding integration examples; Wiretap captures traffic and test outcomes.

Before submitting a change, run the checks appropriate to its scope:

```sh
go test ./...
npm ci --prefix sdk/js
npm test --prefix sdk/js
node scripts/package-smoke.mjs
node scripts/runner-smoke.mjs
node scripts/go-sdk-smoke.mjs
```

Changes to capture, correlation or session lifecycle also require
`go test -race ./internal/proxy ./internal/app ./sdk/go`. Postman compatibility checks live
under `tests/postman-compat`. The CI matrix runs Go and SDK/package checks on
Linux, macOS and Windows with Node 22 and 24.

Lead onboarding examples with the managed Jest, Playwright Test and Mocha
adapters or Go's `Run`/`StartTest` hooks. Keep external-runtime examples clearly
labelled and assign one suite owner to finalization. For custom JavaScript
harnesses, use `startManagedRuntime` instead of duplicating process startup.

Document changes to exported formats, coverage rules or supported integrations.
Add regression tests for behavior changes and preserve incomplete/unknown
evidence explicitly. Do not include real credentials or sensitive recordings in
fixtures. Local release preparation is described in
[the release guide](docs/local-release.md); package and image publication are
separate actions.

Contributions are provided under the repository's [MIT license](LICENSE).
