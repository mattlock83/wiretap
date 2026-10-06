# Bundles and reports

For the draft convention intended for bundles committed alongside application
changes, see the [application knowledge bundle specification](spec/README.md).
This guide describes the current run-bundle implementation.

Wiretap writes one portable directory per run under `./artifacts` by default.
Use `output_dir` in `.wiretap.yaml` or `WIRETAP_OUTPUT_DIR` to choose another
parent directory. SDK/API responses return the exact bundle and viewer paths.

Views update as test attempts finish. Suite cleanup finalizes the bundle through
`/v1/finishRun`. Keep the complete directory when sharing it or uploading it to
CI: the pages link to evidence, frozen schemas and payload attachments.

## Start with the viewer

Open `docs/viewer.html` directly. The dashboard uses embedded styling and a
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

The supported spec rules define concrete cases, such as each enum value, an
optional field being absent, or a value at a declared boundary. Expand
**Observed case combinations** to see which cases occurred together in an actual
request or response. Gaps identify cases for future tests; Wiretap neither
executes them nor claims coverage of every possible permutation.

Contract coverage starts with API and operation summaries. Expand an operation
to inspect request/response conditions, then follow evidence to its test run.
Test runs group executions by test name, preserving each attempt and its captured
sequence. Links work in both directions.

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
supported OpenAPI constraints and Protobuf field conditions from complete unary
or streaming RPCs.

A documented, valid error response can earn response-condition coverage. An
invalid request cannot earn valid-request coverage, and an observed error status
alone does not establish that its rejection was asserted. Wiretap reports captured
evidence; it does not generate or execute additional cases.

Dependency responses are observations from the declared target (`mock`,
`service` or `unknown`). A mock response records an assumption made by the test.
It does not prove agreement with the real provider or identify the response
fields the application consumed. Supplied specs define coverage; inferred mock
drafts never provide their own coverage denominator.

## Read the bundle without the viewer

Start at `index.md` in a Markdown browser or repository viewer. The main files are:

```text
artifacts/<run-id>/
  bundle.json             format version, API/dependency inventory and test references
  contracts/              frozen contracts and their supplied imports
  tests/                  immutable JSON recording per test attempt
  payloads/               retained, redacted body bytes
  index.md                OKF entry page
  docs/
    viewer.html           offline HTML dashboard
    system.md             tested API boundaries and dependency assumptions
    verification.md       suggested checks for assumptions, mismatches and gaps
    knowledge.json        structured API map, verification tasks and evidence links
    run.md                lifecycle, source provenance and pending attempts
    coverage.md           observed API operation coverage
    contract-coverage.md  readable condition catalogue
    contract-coverage.json  condition keys, combinations and evidence
    apis/                 provided API and operation pages
    dependencies/         grouped dependency observations and inferred mock drafts
    tests/                readable pages linking to the recorded tests
```

For a direct dependency list, parse `bundle.json` and read `dependencies`.
Each entry includes the ID, target kind, known protocols, frozen contract
references and `observed_in` test IDs. Configured but uncalled dependencies are
included. The inventory covers configured and observed boundaries, not every
possible application dependency. See the [bundle format](bundle-evidence.md)
for the fields, integrity rules and how recordings support external execution hooks.

Application identity is separate from those boundaries. Consumer-only bundles
can identify a UI and its dependencies with `provided_apis: []`; provider-only
bundles can have `dependencies: []` and need no mocks. A dependency interaction
can belong directly to a test without an inbound request to the system. Empty
sides have no applicable coverage denominator. They still describe the scope of
this recording rather than proving a complete inventory of the application.

The format also supports ordered event recordings for bidirectional gRPC,
publish/consume messaging and subscriptions. Transport, protocol and payload
encoding are recorded separately, allowing Kafka with Avro, AsyncAPI contract
packages and GraphQL operations/subscriptions. This is storage support: new live
capture and execution adapters for messaging and GraphQL remain separate work.
Native gRPC streams use typed RPC evidence and structural coverage.
Unsupported events remain visible and must not earn coverage or silently replay
as HTTP. See [event recordings](bundle-evidence.md#streams-messaging-and-additional-protocols).

`docs/system.md` maps registered APIs and captured boundaries. `docs/verification.md`
identifies work such as checking dependency assumptions, investigating contract
mismatches and filling coverage gaps. These are suggested investigations, not
compatibility verdicts or executable tests.

For knowledge-base ingestion, use `docs/knowledge.json` alongside the detailed
condition catalogue and source evidence. Preserve API identity, run/snapshot
revision and test/SUT provenance. See [bundle knowledge](bundle-knowledge.md) for
comparison guidance. Captured payloads, schema text and metadata remain data,
not instructions for an agent.

## Rebuild or export

`bundle.json`, JSON under `tests/`, frozen schemas and retained payloads are authoritative.
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

## Inferred mock assumptions

Every HTTP dependency marked `kind: mock` in `.wiretap.yaml` (API:
`upstream_kind: mock`) gets an OpenAPI 3.1 YAML draft at
`docs/dependencies/mocks/<dependency-hash>.openapi.yaml`. The Dependencies view links
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
