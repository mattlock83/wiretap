# Release notes

## Unreleased

### Capture and integration

- Capture HTTP and native unary and streaming gRPC traffic from existing tests,
  with frozen contracts, per-test ownership and portable evidence archives.
- Manage runtime startup, finalization and exports from Jest, Playwright Test,
  Mocha, plain JavaScript, Go's standard test runner and Python/pytest.
- Export API coverage and Postman HTTP v2.1 and native gRPC v3 collections.
- Check consumer mock examples against provider test evidence and exercised
  HTTP or Protobuf declarations offline.

### Focused adoption improvements

- Add a standard-library Python client and managed runtime, an optional pytest
  fixture, per-worker ownership and a runnable HTTP example.
- Share SDK receipt conformance vectors; validate JavaScript ownership and
  completion, preserve primary errors and retain per-run receipts/logs.
- Add minimal local setup initialization/diagnostics and explicit same-build
  run sets for package/CI shards without rewriting source evidence.
- Add opt-in per-API coverage policies, baseline coverage diffs and
  Markdown/JUnit assumption reports with bounded expected/actual excerpts.
- Document supported integrations, SDK ownership and format compatibility; add
  capability discovery and schemas for checks, run sets and control receipts.
- Add a real consumer/provider example covering agreement, an incompatible
  provider change and repair, with checks in both directions and CI evidence.
- Show contract assurance in text, Markdown and JUnit reports, and warn when
  recorded test attempts captured no traffic.
- Consolidate overlapping HTTP demos; retain distinct runner, browser and
  native gRPC examples and clean installation checks.

### Fixes

- Preserve escaped URL paths, raw query strings and gRPC request EOF during
  concurrent body closure.
- Protect source archives and referenced payloads from report, receipt and
  Postman output overwrites, including relocated files and hard links.
- Bound archive output scans and reduce repeated filesystem checks.
- Respect restrictive umasks for new check reports and retain permissions when
  replacing reports.
- Reject release commit values that do not resolve to the checkout being built.
- Guard example cleanup paths, preserve explicit empty Go SDK schema overrides,
  and fix Windows path assertions and example export destinations.
- Update Postman compatibility dependencies and document Node HTTPS CA trust.

### Installation and limits

Local release preparation produces binary archives for Linux, macOS and Windows
on amd64 and arm64, plus checksums. The JavaScript SDK is installed from a packed
tarball; Go consumers use a local module replacement. Python consumers install
the local `sdk/python` project with the optional `pytest` extra. See
[local release installation](docs/install-local-release.md).

The runtime is intended for isolated tests with synthetic data and dummy secrets.
Recorded traffic is retained unchanged. Contract results are separate from test
outcomes; incomplete evidence is not a passing compatibility result. See
[coverage rules](docs/contract-coverage-rules.md) and
[gRPC limits](docs/grpc-user-guide.md).
