# Coverage gates and CI reports

Coverage policies are explicit gates over individual APIs and condition
families. They do not produce a global percentage or claim that exercised
requests prove provider compatibility.

## Opt into a coverage policy

Save a policy such as `coverage-policy.json`:

```json
{
  "policy_version": "1.0",
  "require_complete": true,
  "apis": [
    {
      "api_id": "orders",
      "origin": "provided",
      "required_operations": ["GET /orders", "POST /orders"],
      "required_families": ["operation", "response_status"],
      "fail_unknown": true
    }
  ]
}
```

```sh
wiretap report --bundle artifacts/run-... --policy coverage-policy.json --format json --output coverage.json
```

`origin` is `provided` or `dependency`. Select literal operation names from the
coverage report; gRPC operations use their full `/package.Service/Method` path.
Required operations must have eligible coverage evidence. Required families
fail on `not_covered` targets and when the named family is absent. With
`fail_unknown`, unknown denominators and selected `unknown` or `unsupported`
conditions also fail. If no family list is supplied, that unknown check covers
all families of the selected API. Excluded conditions remain excluded.

Rules apply separately to every matching contract revision. A covered request
for an older revision cannot satisfy a gate for another revision. Missing APIs
and required operations fail explicitly. `require_complete` rejects an
incomplete measurement, including unsupported event interactions. Gates are
opt-in; unselected APIs and condition families do not lower another API's result.
The ordinary coverage report is still emitted when a policy fails; diagnostics
go to stderr and the command exits 1. Invalid policies or incompatible report
and measurement versions are input errors.

## Compare two existing coverage reports

```sh
wiretap compare --baseline baseline-coverage.json --current coverage.json --format json --output coverage-diff.json
```

Both inputs must be JSON coverage reports using supported report and measurement
rules. Reads are bounded by `WIRETAP_MAX_ARCHIVE_FILE_BYTES`,
`WIRETAP_MAX_ARCHIVE_TOTAL_BYTES`, and `WIRETAP_MAX_ARCHIVE_FILES` (two reads are
required). Additive envelope fields are allowed within a supported version.

The diff identifies lost or gained coverage for identical target identities,
added and removed targets, and changes to each API's schema inventory. A schema
revision change is an explicit addition/removal; coverage is not compared across
different revisions. These observations are not breaking-change or provider
compatibility proof. Lost coverage on an unchanged target writes the diff and
exits 1; schema changes alone remain descriptive. `--format text` is the default.

Write comparison output outside raw capture archives. The command protects its
two report inputs, their aliases and hard links, and capture directories found
in their ancestry. Externally saved reports do not contain the original archive's
filesystem location, so choose an output path outside that evidence yourself.

## Send assumption checks to CI

Use `--format markdown` for a review artifact or `--format junit` for a CI JUnit
XML importer. They work with explicit pairs, artifact-store discovery and run
sets. They present the existing check verdict and preserve its exit status.
JUnit cases describe consumer/provider assumption checks, not framework tests.
Text, Markdown and JUnit also show how many matched examples received HTTP
declaration/value checks, provider-spec checks, Protobuf declaration/wire checks,
or example-only agreement without schemas. An unknown assurance kind stays
explicit. Aggregate assurance counts include comparisons omitted by presentation
limits; Markdown and JUnit also include each displayed comparison's assurance.

```sh
wiretap check consumer --consumer artifacts/consumer --provider artifacts/provider --format markdown --output check.md
wiretap check consumer --consumer artifacts/consumer --provider artifacts/provider --format junit --output check.xml
```

Diagnostics escape Markdown/HTML and XML, sanitize XML control characters, and
bound individual fields and detail size. The presentation includes at most 256
comparisons and 64 KiB of details per comparison, with an 8 MiB output limit.
Truncation never turns a failed aggregate into a passing JUnit report. Use JSON
for complete diagnostics and machine processing. Expected/actual excerpts name
the first provider candidate only; bounded displayed values do not replace the
complete recordings used by the check.
