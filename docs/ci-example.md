# Keep Jest capture evidence in CI

Run your existing Jest command with the Wiretap integration and upload the
complete output directory even when tests fail. Jest remains the source of test
outcomes; schema findings, coverage policies and compatibility checks are
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
wiretap check consumer --consumer ./orders-run --provider ./inventory-run \
  --dependency inventory --format junit --output check-results/inventory.xml
```

Create `check-results/` first and keep it outside the input archives and Arazzo
export trees. The checker uses each archive's `exports/arazzo/arazzo.json` and
OpenAPI companions by default. Both sides must represent eligible JSON/REST
traffic; selected unsupported traffic cannot silently disappear from a pass.

For a shared local artifact store, use declared provider mappings:

```sh
wiretap check consumer --consumer ./orders-run --artifacts ./api-evidence
wiretap check provider --provider ./inventory-run --artifacts ./api-evidence
```

Upload/download snapshots with your existing artifact tooling. Extract ZIP/TAR
archives before checking and retain their directory structure. The CLI discovers
local restored runs; it does not upload or fetch evidence. Pin the intended
application builds and provider versions instead of choosing a moving latest run.
See [shared snapshot selection](assumption-checks.md#share-provider-snapshots).

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
| `1` | The comparison completed with uncovered, mismatched or unverified evidence. |
| `2` | Inputs or flags are invalid, or the command could not complete. |

Use the exit status alongside JSON, Markdown or JUnit reports. Input validation
leaves an existing output file unchanged, so an old report is not evidence of a
successful current check. Keep the provider and consumer suites' own exit statuses
as separate gates.

See [assumption checks](assumption-checks.md) for comparison limits,
[report policies](report-policies.md) for coverage gates and CI report formats,
and [run sets](run-sets.md) for explicit shards of the same build.
