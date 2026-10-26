# Contributing

For vulnerability reports, follow the [security contact instructions](SECURITY.md).

Use Go 1.26.10 and Node 22 or 24. Keep existing Jest assertions and HTTP clients
when adding integration examples; Wiretap captures
traffic and test outcomes.

Before submitting a change, run the checks appropriate to its scope:

```sh
go test ./...
npm ci --prefix sdk/js
npm test --prefix sdk/js
node scripts/runner-smoke.mjs
```

Changes to capture, correlation or session lifecycle also require
`go test -race ./internal/proxy ./internal/app`. Arazzo export checks live
under `tests/arazzo-compat` and run in the SDK smoke checks. The CI matrix runs
Go and SDK/package checks on Linux, macOS and Windows with Node 22 and 24. See the
[support matrix](docs/support.md) for supported Jest versions.

Lead onboarding with the managed Jest configuration wrapper and the
[external Jest example](examples/existing-jest/README.md). The runtime remains
written in Go; Jest is the supported test integration on this branch.

Document changes to exported formats, coverage rules or supported integrations.
Documentation and website copy describe implemented behavior and runnable
examples. Keep proposed features and deferred work only in [the roadmap](docs/roadmap.md).
Add regression tests for behavior changes and preserve incomplete/unknown
evidence explicitly. Do not include real credentials or sensitive recordings in
fixtures. Local release preparation is described in
[the release guide](docs/local-release.md); package and image publication are
separate actions.

Contributions are provided under the repository's [MIT license](LICENSE).
