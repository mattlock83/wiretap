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
- Keep explicit pair and run-set checks with text/JSON results; remove artifact
  discovery, coverage policy gates, baseline diffs and Markdown/JUnit output.
- Document supported integrations, SDK ownership and format compatibility; add
  capability discovery and schemas for checks, run sets and control receipts.
- Show contract assurance in text and JSON reports, and warn when
  recorded test attempts captured no traffic.
- Keep a runnable external Jest example and clean installation checks.
- Simplify CLI help, print flag errors once, and use exit 2 consistently for
  invalid or missing inputs while preserving failed checks and output errors.
- Bound terminal report details with omission counts and print exact coverage
  and Arazzo paths after Jest cleanup and run finalization.

- Restore self-contained HTML reporting from Wiretap JSON, OpenAPI specs and
  Arazzo, with test-linked coverage and captured HTTP exchanges.
- Consolidate exports under `report` and comparisons under `check`; remove YAML
  coverage, live report requests, unused event records and opaque contract packages.
- Keep the Jest SDK focused on `withWiretap` and `captureJest`, with one options
  path and automatic attempt ownership.
- Reuse one verified archive snapshot for coverage, HTML and Arazzo generation.
- Share frozen OpenAPI parsing across reports and compatibility checks, use one
  operation index for spec and per-test coverage, and consolidate staged output
  writing. Reuse verified snapshots across shard comparisons.
- Keep schema-validation findings only in `schema_validation` and remove
  condition-combination fingerprints while retaining individual condition evidence.
- Let Go apply capture defaults once. Use keyed dependency objects and `schemas`
  arrays in the Jest SDK, with explicit empty overrides preserved.
- Remove unused boundary registration and consolidate setup, lifecycle and local
  release documentation.
- Give managed Jest one startup owner and retain standalone capture support.
- Always generate JSON/HTML coverage and eligible Arazzo at finalization; remove
  output-selection settings. Text reports remain available through the CLI.
- Keep completion receipts inside their exact archive; remove the shared
  output-parent `last-run.json`.
- Share strict JSON decoding, prepare frozen contracts once per registration,
  and defer run-set coverage generation until a report is requested.
- Flatten the internal HTTP message model while preserving archive JSON, and
  share runtime configuration resolution between startup and diagnostics.

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
[local release installation](docs/local-release.md#install-a-prepared-release).

The runtime is intended for isolated tests with synthetic data and dummy secrets.
Recorded traffic is retained unchanged. Contract results are separate from test
outcomes; incomplete evidence is not a passing compatibility result. See
[coverage rules](docs/contract-coverage-rules.md) and
[supported integrations](docs/support.md).
