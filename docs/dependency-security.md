# Dependency security maintenance

The runtime uses the root [Go module](../go.mod). The Jest SDK has its own
[package manifest](../sdk/js/package.json) and committed lockfile. Review and
check each dependency graph when changing its manifest.

## Runtime

`go.mod` and `go.sum` record the selected toolchain and exact direct and indirect
dependency versions. Run these checks from the repository root with
`govulncheck` installed:

```sh
govulncheck ./...
go test ./...
go vet ./...
```

Use a patched Go toolchain for vulnerability scans: dependency updates cannot
repair standard-library vulnerabilities in the executable's build toolchain.
Reachability information helps assess an advisory, but does not replace updating
an affected dependency.

## Jest SDK

Install and audit the committed dependency graph:

```sh
npm ci --prefix sdk/js
npm audit --prefix sdk/js --audit-level=low
npm test --prefix sdk/js
node scripts/runner-smoke.mjs
```

The smoke script packs the SDK and installs it in a clean external Jest project.
It verifies startup, test outcomes, cleanup, capture archives, coverage and
Arazzo exports. It also checks TypeScript declarations. The
[existing Jest example](../examples/existing-jest/README.md) exercises a larger,
pinned external application without changing its original test assertions.
Its validation helper checks Arazzo against the pinned official schema and
compares eligible workflow requests and success criteria with captured evidence.

The smoke projects install exact dependency versions specified by their
harnesses. When SDK dependencies change, regenerate its lockfile from the
package manifest and verify a clean install. OpenTelemetry instrumentation runs
locally; no collector is required.

A scan describes the checked dependency graph and advisory database at the time
it runs. Report suspected Wiretap vulnerabilities through the private channel in
[SECURITY.md](../SECURITY.md).
