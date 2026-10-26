# Keep Jest capture evidence in CI

Run your existing Jest command with the Wiretap integration and upload the
complete output directory even when tests fail. Jest remains the source of test
outcomes; schema findings and compatibility checks are
separate gates.

## Run a Jest project

This example assumes the repository contains `.wiretap.yaml`, an OpenAPI contract,
a Jest configuration wrapped with `withWiretap`, and a prepared local Wiretap
binary and SDK package. Install the SDK tarball alongside the project's Jest
version and set `WIRETAP_BINARY` to the executable's absolute path.

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-node@v4
    with:
      node-version: '22'
      cache: npm
  - run: npm ci
  - name: Run existing Jest tests with capture
    env:
      WIRETAP_BINARY: /absolute/path/to/wiretap
    run: npm test -- --runInBand
  - uses: actions/upload-artifact@v4
    if: always()
    with:
      name: api-evidence
      path: artifacts/wiretap/
```

Adapt binary/package preparation and the output path to your build. The SDK does
not download a runtime. For reproducible local installation, see
[local release preparation](local-release.md). Keep one suite owner so exports
finish after test cleanup.

The runnable [external Jest example](../examples/existing-jest/README.md) checks
integration against an unchanged existing suite. Its harness preserves expected
baseline failures and verifies the captured archive and eligible Arazzo exports.
The repository's [CI workflow](../.github/workflows/tests.yml) runs it separately
from the clean-package Jest checks.

## Retain complete archives

Use an explicit outputs configuration:

```yaml
outputs:
  coverage: [json, text, html]
  arazzo: true
```

Keep `bundle.json`, `tests/`, `contracts/`, `payloads/`, `exports/`, the archive's
`last-run.json` and its bounded `wiretap.log` together. Copying only coverage or
Arazzo files loses evidence needed for compatibility checks. Use the canonical
receipt inside each run to identify its exact output paths; the parent directory's
latest receipt is advisory when multiple Jest runs share the output root.

Incomplete captures can fail strict Arazzo export even when coverage succeeds.
Do not turn a failed capture or export into a passing CI job. Retain the evidence
and inspect its diagnostics. See [artifact limits](artifacts.md).

## Gate consumer/provider assumptions offline

Restore complete consumer and provider snapshots from trusted builds, then run:

```sh
wiretap check --consumer ./orders-run --provider ./inventory-run \
  --dependency inventory --format json --output check-results/inventory.json
```

Create `check-results/` first and keep it outside the input archives and Arazzo
export trees. The checker uses each archive's `exports/arazzo/arazzo.json` and
OpenAPI companions by default. Both sides must represent eligible JSON/REST
traffic; selected unsupported traffic cannot silently disappear from a pass.

Upload/download snapshots with your existing artifact tooling. Extract ZIP/TAR
archives before checking and retain their directory structure. Pin the intended
application builds and provider versions, then supply their paths explicitly.
For separate suites or jobs, use [same-build run sets](run-sets.md).

Every selected captured mock example needs a complete passing provider witness
for the same concrete request and paired response. With only a provider OpenAPI
3.0 contract, both concrete exchanges validate under that contract. With both
contracts, exercised validation rules must also agree. With neither, a verified
match is explicitly example-only. Missing scenarios, mismatches and unverified
evidence fail the check. Arazzo inputs are verified offline; expressions and
workflow success criteria are not executed.

| Exit status | Meaning |
| --- | --- |
| `0` | All selected assumptions have verified supporting evidence. |
| `1` | Uncovered, mismatched or unverified evidence, or an operational/output failure. |
| `2` | Inputs or flags are invalid. |

Use the exit status alongside JSON or text reports. Input validation
leaves an existing output file unchanged, so an old report is not evidence of a
successful current check. Keep the provider and consumer suites' own exit statuses
as separate gates.

See [assumption checks](assumption-checks.md) for comparison limits,
[run sets](run-sets.md) for explicit shards of the same build.
