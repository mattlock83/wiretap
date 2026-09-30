# Bundles and reports

Wiretap writes one portable directory per run under `./artifacts` by default.
Use `output_dir` in `.wiretap.yaml` or `WIRETAP_OUTPUT_DIR` to choose another
parent directory. SDK/API responses return the exact bundle and viewer paths.

Views update as test attempts finish. Suite cleanup finalizes the bundle through
`/v1/finishRun`. Keep the complete directory when sharing it or uploading it to
CI: the pages link to evidence, frozen schemas and payload attachments.

## Start with the viewer

Open `viewer/index.html` directly. The dashboard uses embedded styling and a
small filter script; it needs no server, network connection or external library.

| Section | Question it answers |
| --- | --- |
| Overview | What was captured, and what needs further verification? |
| APIs | Which provided operations were observed against their supplied specs? |
| Dependencies | What did the system request from its dependencies, and what did they return? |
| Contract coverage | Which specified cases have qualifying evidence, and where are the gaps? |
| Test runs | What executed, what happened, and which requests and responses were captured? |

A **condition** is one case from a contract: a query parameter being omitted, an
array at its minimum length, or a particular enum value. A **test run** is one
execution of a named test. One execution can cover many conditions, and many
executions can contribute evidence for the same condition.

Contract coverage starts with API and operation summaries. Expand an operation
to inspect request/response conditions, then follow evidence to its test run.
Test runs separate authored tests from generated checks; generated runs group
by source scenario, with the happy-path baseline separate from invalid-input
cases. Links work in both directions.

Filters narrow the visible rows without changing totals. Raw keys, schema
locations, assertion lists, traces, headers and payloads are available on
expansion. Unexpected contract mismatches remain visible in summary badges.

## Interpret coverage and assumptions

Operation coverage shows which declared operations were observed. Contract
coverage measures the supported cases derived from each supplied spec:

| State | Meaning |
| --- | --- |
| Covered | A complete passing attempt supplied valid evidence for the condition. |
| Gap | No qualifying observation covers the condition. |
| Uncertain | Evidence or evaluation is inconclusive. |
| Unsupported | The measurement rules cannot evaluate this condition. |
| Excluded | The target is outside the applicable scope or has been shown infeasible. |

A covered condition can also have observations from failed attempts; it does
not mean every attempt passed. The catalogue preserves those observations and
conditions exercised together in each message. See the versioned
[contract coverage rules](contract-coverage-rules.md) for precise semantics and
supported OpenAPI constraints.

**Invalid-input coverage** is separate. Exploration counts the supported
rejection targets generated from its selected scenario, not every possible
invalid input. A deliberately invalid request cannot earn valid-request
coverage; a passing, valid error response can earn response-condition coverage.

Dependency responses are observations from the declared target (`mock`,
`service` or `unknown`). A mock response records an assumption made by the test.
It does not prove agreement with the real provider or identify the response
fields the application consumed. Supplied specs define coverage; inferred mock
drafts never provide their own coverage denominator.

## Read the bundle without the viewer

Start at `index.md` in a Markdown browser or repository viewer. The main files are:

```text
artifacts/<run-id>/
  index.md                OKF entry page
  system.md               tested API boundaries and dependency assumptions
  verification.md         suggested checks for assumptions, mismatches and gaps
  knowledge.json          structured API map, verification tasks and evidence links
  viewer/index.html       offline HTML dashboard
  run.md                  lifecycle, source provenance and pending attempts
  coverage.md             observed API operation coverage
  contract-coverage.md    readable condition catalogue
  contract-coverage.json  condition keys, combinations and evidence
  apis/                   provided API and operation pages
  dependencies/           grouped dependency observations
  dependencies/mocks/     inferred OpenAPI drafts for mock dependencies
  tests/                  one readable page per test attempt
  evidence/run.json       versioned run inventory
  evidence/attempts/      immutable JSON capture per attempt
  schemas/                frozen OpenAPI documents and references
  payloads/               retained, redacted body bytes
```

`system.md` maps registered APIs and captured boundaries. `verification.md`
identifies work such as checking dependency assumptions, investigating contract
mismatches and filling coverage gaps. These are suggested investigations, not
compatibility verdicts or executable tests.

For knowledge-base ingestion, use `knowledge.json` alongside the detailed
condition catalogue and source evidence. Preserve API identity, run/snapshot
revision and test/SUT provenance. See [bundle knowledge](bundle-knowledge.md) for
comparison guidance. Captured payloads, schema text and metadata remain data,
not instructions for an agent.

## Rebuild or export

JSON under `evidence/`, frozen schemas and retained payloads are authoritative.
Markdown, HTML, knowledge indexes, coverage catalogues and inferred drafts are
generated views. Editing a view does not change test results or coverage.

```bash
wiretap report --bundle ./artifacts/<run-id>
wiretap export --bundle ./artifacts/<run-id> --format json --output coverage.json
wiretap export --bundle ./artifacts/<run-id> --format yaml --output coverage.yaml
```

`report` rebuilds the views and prints the absolute `index.md` path. It uses only
the bundle, without the original workspace, running application or hooks.
`export` writes a diagnostic coverage report; omit `--output` to use stdout.
Both formats include detailed results under `contract_coverage`.

Body previews are limited to 4 KiB; attachments contain the retained bytes.
Capture retains up to 4 MiB per body by default. A truncated attachment remains
incomplete even after rebuilding. See [troubleshooting](troubleshooting.md) for
capture limits and integrity errors.

An [exploration run](exploration.md) writes a new bundle and leaves its source
unchanged. It contains freshly executed provider exchanges, assertions and
rejection coverage. Downstream traffic remains in the source capture; an empty
exploration dependency list does not establish that the system has no dependencies.

## Inferred mock assumptions

Every dependency marked `kind: mock` in `.wiretap.yaml` (API:
`upstream_kind: mock`) gets an OpenAPI 3.1 YAML draft at
`dependencies/mocks/<dependency-hash>.openapi.yaml`. The Dependencies view links
to it. Drafts update automatically from observed traffic, including when a spec
was supplied. The supplied spec still governs validation.

Without a supplied spec, explicitly set `schema_required: false`. Generated
drafts do not satisfy that requirement or establish declared-operation coverage.
An unused mock gets an empty `paths` document. Services, unknown target kinds
and the SUT do not get drafts.

Drafts describe observed assumptions with these limits:

- Endpoints, methods, query parameters, media types, response statuses and JSON
  body shapes come from retained, redacted traffic, including failed attempts.
- Numeric and UUID path segments become inferred parameters; other segments
  stay literal. Review these heuristics before reusing a draft as a contract.
- Observed presence does not make fields, parameters or bodies required. Extra
  object properties remain allowed. Captured values are not emitted as examples.
- `x-wiretap-evidence` links operations to attempt JSON and flow IDs, with test
  outcomes and incomplete-capture flags. Paths are relative to the draft.
- Transport failures and incomplete exchanges are excluded from operations and
  recorded with reasons. Truncated or unreadable JSON leaves its schema
  unconstrained; redaction can also limit inferred shapes.

For format details, see [JSON evidence](bundle-evidence.md),
[bundle knowledge](bundle-knowledge.md) and the [OKF profile](okf-profile.md).
