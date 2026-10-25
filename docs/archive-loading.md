# Archive loading budgets

Coverage reporting and Postman export load the immutable capture archive through
`internal/bundle.Load`. Loading verifies hashes, schema inventories and gRPC
projections, and restores retained payloads before calculating either output.
Run finalization verifies the same raw archive. A loading-budget or integrity
error prevents a successful finalization result; retained evidence stays on disk.

The loader uses finite defaults:

| Budget | Default |
| --- | --- |
| Any individual file read | 64 MiB |
| Cumulative bytes read | 512 MiB |
| File reads | 100,000 |

The binary accepts positive integer environment overrides:
`WIRETAP_MAX_ARCHIVE_FILE_BYTES`, `WIRETAP_MAX_ARCHIVE_TOTAL_BYTES` and
`WIRETAP_MAX_ARCHIVE_FILES`. Set these on the process that captures, reports or
exports the archive. Zero, empty, negative and malformed values are errors;
there is no environment setting that disables the budgets.

Output generation also inventories raw files to prevent overwriting evidence
through aliases or hard links. That scan has a separate entry budget equal to
`WIRETAP_MAX_ARCHIVE_FILES`: each file, directory or symlink encountered beneath
the raw `tests`, `contracts` and `payloads` roots counts, including unreferenced
entries. The fixed raw roots themselves do not count. Directory entries are read
in bounded batches, and source identities are reused across generated outputs.
An inventory limit error stops output generation and identifies the setting to
raise for a larger trusted archive.

These budgets include `bundle.json`, attempt captures, contract manifests, every
frozen contract and import, HTTP/event/gRPC payload attachments, rich gRPC status
attachments and descriptor reads used to verify gRPC projections. Reading the
same path again consumes the budget again. For example, a payload shared by many
attempts is restored into each attempt and counts each time; the budget is not
the archive's unique size on disk.

File sizes are checked before reading. A bounded reader also catches growth
between the size check and the read. Nonregular files are rejected. A budget
failure returns no partial run or captures and wraps `bundle.ErrLoadLimit`, so
internal callers can use `errors.Is(err, bundle.ErrLoadLimit)` to identify it.
Normal identity, hash and capability checks still apply within the budgets.

Internal integrations that need larger trusted archives can call
`LoadWithLimits(root, limits)` instead of `Load(root)`:

```go
limits := bundle.DefaultLoadLimits()
limits.MaxFileBytes = 128 << 20
limits.MaxTotalBytes = 1 << 30
limits.MaxFileReads = 200_000
run, captures, err := bundle.LoadWithLimits(root, limits)
```

Zero fields select their defaults. Negative fields and byte budgets equal to
`math.MaxInt64` are invalid; there is no unlimited sentinel. Increasing these
budgets does not disable the existing protocol-specific gRPC retention limits.
Explicit `LoadWithLimits` budgets are independent of environment overrides.

The budgets constrain input reads, not the exact process memory footprint.
Decoded JSON, descriptors, restored strings and base64 bodies can require more
memory than their input bytes. Choose smaller budgets when loading archives in
a memory-constrained process. No archive files are modified by loading.
