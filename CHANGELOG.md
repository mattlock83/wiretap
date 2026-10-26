# Release notes

## Unreleased

### Capture and integration

- Capture HTTP traffic from existing tests with frozen contracts, per-test
  ownership and portable evidence archives.
- Manage runtime startup, finalization and exports from Jest.
- Remove the Go and Python SDKs, other JavaScript runner integrations, and their
  examples and guides from this experimental branch.
- Export API coverage and Arazzo workflows for JSON/REST APIs, with portable
  OpenAPI source documents.
- Check consumer mock examples against provider test evidence and exercised
  HTTP declarations offline using verified Arazzo exports.
- Keep this experimental branch focused on HTTP and Arazzo; remove Postman
  export and native gRPC support.

### Focused adoption improvements

- Validate Jest receipt conformance, ownership and completion, preserve primary
  errors and retain per-run receipts/logs.
- Add minimal local setup initialization/diagnostics and explicit same-build
  run sets for package/CI shards without rewriting source evidence.
- Add opt-in per-API coverage policies, baseline coverage diffs and
  Markdown/JUnit assumption reports with bounded expected/actual excerpts.
- Document supported integrations, SDK ownership and format compatibility; add
  capability discovery and schemas for checks, run sets and control receipts.
- Show contract assurance in text, Markdown and JUnit reports, and warn when
  recorded test attempts captured no traffic.
- Keep a runnable external Jest example and clean installation checks.
- Simplify CLI help, print flag errors once, and use exit 2 consistently for
  invalid or missing inputs while preserving failed checks and output errors.
- Bound terminal report details with omission counts and print exact coverage
  and Arazzo paths after Jest cleanup and run finalization.

### Fixes

- Preserve escaped URL paths and raw query strings during concurrent capture.
- Protect source archives and referenced payloads from report, receipt and
  Arazzo output overwrites, including relocated files and hard links.
- Bound archive output scans and reduce repeated filesystem checks.
- Respect restrictive umasks for new check reports and retain permissions when
  replacing reports.
- Reject release commit values that do not resolve to the checkout being built.
- Guard example cleanup paths and fix Windows path assertions and export
  destinations.
- Document Node HTTPS CA trust.

### Installation and limits

Local release preparation produces binary archives for Linux, macOS and Windows
on amd64 and arm64, plus checksums. The JavaScript SDK is installed from a packed
tarball for Jest. See
[local release installation](docs/install-local-release.md).

The runtime is intended for isolated tests with synthetic data and dummy secrets.
Recorded traffic is retained unchanged. Contract results are separate from test
outcomes; incomplete evidence is not a passing compatibility result. See
[coverage rules](docs/contract-coverage-rules.md) and
[supported integrations](docs/support.md).
