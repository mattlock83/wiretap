# Dependency security maintenance

The runtime and Go SDK share the root [Go module](../go.mod). JavaScript SDK,
Node gRPC example and Postman compatibility dependencies have separate package
manifests and committed lockfiles. Review each dependency graph when changing
its manifest; passing checks for one graph do not cover the others.

## Runtime and Go SDK

The module requires Go 1.26 and selects Go 1.26.10 in its toolchain directive.
Its direct dependencies include kin-openapi 0.149.0, grpc-go 1.83.2, x/net 0.60.0
and OpenTelemetry Go 1.45.0. The exact direct and indirect versions are recorded
in `go.mod` and `go.sum`.

Run these checks from the repository root with `govulncheck` installed:

```sh
govulncheck ./...
go test ./...
go vet ./...
```

Use a patched Go toolchain for vulnerability scans: dependency updates cannot
repair standard-library vulnerabilities in the executable's build toolchain.
Reachability information helps assess an advisory, but does not replace updating
an affected dependency.

## JavaScript and compatibility fixtures

Install and audit each committed dependency graph:

```sh
npm ci --prefix sdk/js
npm audit --prefix sdk/js --audit-level=low
npm test --prefix sdk/js

npm ci --prefix examples/grpc-boundaries/node
npm audit --prefix examples/grpc-boundaries/node --audit-level=low

npm ci --prefix tests/postman-compat
npm audit --prefix tests/postman-compat --audit-level=low
npm test --prefix tests/postman-compat
```

Run the [Node gRPC example](../examples/grpc-boundaries/node/README.md) through
its harness to exercise its runtime dependencies. The JavaScript SDK and example
use their existing OpenTelemetry libraries; no collector is required.

The [Postman compatibility harness](../tests/postman-compat/README.md) uses
exact-pinned Ajv 8.20.0 to validate exports against official HTTP v2.1 and gRPC v3
schemas and assert captured content. Its
[workflow](../.github/workflows/postman-compat.yml) runs installation, audit and
compatibility checks on relevant changes.

## Python

The Python SDK's runtime client uses the standard library. pytest and pytest-xdist
are optional integration/test dependencies declared in
[pyproject.toml](../sdk/python/pyproject.toml). Check the versions installed in the
application's environment as part of that application's dependency maintenance.

A scan describes the checked dependency graph and advisory database at the time
it runs. It is not a permanent vulnerability-free guarantee. Report suspected
Wiretap vulnerabilities through the private channel in [SECURITY.md](../SECURITY.md).
