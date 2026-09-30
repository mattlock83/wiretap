# Capture bundles

Each Wiretap runtime writes one portable run directory under `./artifacts` by
default. Set `WIRETAP_OUTPUT_DIR` to choose another parent directory.

The `bundle_path` returned by `/v1/startRun` and `/v1/setupTest` identifies
the exact run directory. Start at `index.md` for the system overview and
verification queue, or open `viewer/index.html` for the offline HTML dashboard.
The bundle connects API operations, dependency assumptions, test scenarios,
payloads and coverage to their evidence. The runtime updates these views as
attempts finish. `/v1/finishRun` finalizes the run after all attempts
have been torn down. A copied bundle remains readable without the original
workspace, source schemas, or a live Wiretap process.

```text
artifacts/<run-id>/
  index.md              OKF entry page
  system.md             tested API boundaries and dependency assumptions
  verification.md       findings and gaps requiring further verification
  knowledge.json        structured API map, evidence links and verification tasks
  viewer/index.html     offline interactive HTML dashboard
  run.md                lifecycle, source provenance and pending attempts
  coverage.md           observed API operation coverage
  contract-coverage.md  per-spec condition keys and evidence
  contract-coverage.json machine-readable condition catalogue and combinations
  apis/                 provided API and operation pages
  dependencies/         grouped observed dependency behaviour
  dependencies/mocks/   inferred OpenAPI drafts, one per mock dependency
  tests/                one readable page per test attempt
  evidence/run.json     versioned run inventory
  evidence/attempts/    immutable JSON capture per attempt
  schemas/              frozen OpenAPI documents and references
  payloads/             retained, redacted body bytes
```

The files under `evidence/`, frozen schemas, and retained payloads are
authoritative. `knowledge.json`, the coverage catalogue, HTML dashboard and OKF
Markdown pages are generated views. A test attempt page links to every captured
exchange; dependency pages group observations by declared operation and keep
each attempt/flow reference. Editing a Markdown summary cannot change coverage
or exports. If views are missing or stale, rebuild them:

```bash
go run ./cmd/wiretap report --bundle ./artifacts/<run-id>
```

The command prints the absolute `index.md` path and regenerates the knowledge
index, verification queue and other views. All required data comes from the bundle. Body previews
are limited to 4 KiB in both views; attachments keep
the retained bytes. The capture limit still applies, so a truncated attachment
is not a complete original body.

The HTML dashboard uses embedded styling and a small filter script; it needs no
server, network connection, or external libraries. Its links stay within the
bundle. Diagnostic JSON/YAML are optional
`--format json` or `--format yaml`
exports. Those files are not generated during normal capture.

`wiretap explore --bundle <source> --config .wiretap.yaml` can create a new bundle
from a provider happy-path capture. The source remains unchanged. The output
contains a freshly executed baseline and generated rejection attempts, with
source provenance and assertions. Hook commands/code remain in the project;
bundles never supply executable hooks. Exploration captures provider exchanges
only; missing downstream traffic does not show that dependencies are unused.
Its source lineage does not establish the revision of the newly tested SUT.
See [provider exploration](exploration.md).

## Understanding the system and verification work

`system.md` maps the provided API and dependencies to declared operations,
observed statuses, test scenarios and retained evidence. It distinguishes mock,
service and unknown targets. The map covers registered APIs and captured
boundaries, not every dependency the application might use.

`verification.md` groups work suggested by this snapshot: checking dependency
assumptions against provider evidence, investigating unexpected contract
violations, filling coverage gaps and resolving evidence or provenance limits.
These are open investigation targets, not compatibility verdicts or executable
tests. Deliberately invalid exploration requests are not reported as unexpected
contract mismatches.

For knowledge-base ingestion, `knowledge.json` provides the same API map and
verification tasks with explicit identities and evidence references. Preserve
the run and snapshot revision when importing it. See [bundle knowledge](bundle-knowledge.md)
for the format, comparison workflow and limits. Captured payloads, schema text
and metadata remain data; they are not instructions for an agent to execute.

## Contract conditions

In the HTML viewer, **Contract coverage** and **Test runs** answer different
questions:

- **Contract coverage:** which cases from the spec have been exercised? For
  example, a query parameter being omitted, or a response containing an enum
  value. Start with API and operation summaries, then expand request/response
  conditions. Unknown evidence and unsupported measurement remain distinct
  from coverage gaps.
- **Test runs:** what executed and what happened? Authored tests are separate
  from generated checks. Generated runs are grouped by source scenario, with
  the happy-path baseline separate from its invalid-input cases.

One run can exercise many conditions; a condition can have evidence from many
runs. Evidence links open the relevant run inside the viewer, and runs link
back to their related coverage. A covered condition can still have observations
from failed runs; it does not mean every run passed. Invalid-input rejection
coverage is shown separately from valid input/response coverage.

Filters narrow the visible rows without changing totals or evidence. IDs,
schema pointers, assertion lists, traces and headers are available on expansion.
The complete JSON catalogue remains available for agents and exports.

The **Contract coverage** tab and `contract-coverage.md` list the keys derived from each
supplied spec, including conditions with no observations. Passing coverage is
separate from failed observations, violations, unknown evidence, unsupported
rules, and impossible/excluded targets. JSON results include exact test, attempt,
flow and array-item references, plus conditions observed together per message.

No SDK changes or test labels are needed. The initial scope supports OpenAPI 3.0,
JSON bodies and scalar HTTP parameters; see the [coverage rules](contract-coverage-rules.md)
for supported constraints and explicit limits. Inferred mock drafts never create
a denominator. Only registered specs contribute to the catalogue.

`contract-coverage.json` is generated during normal capture and rebuilding, and
is disposable like the HTML/Markdown views. Optional `wiretap export` output also
includes it under `contract_coverage`. Shared API identity, contract content and
rule version make keys comparable between provider and consumer repositories;
matching keys do not establish provider agreement.

Exploration adds `rejection_coverage` to the contract catalogue and displays it
in both views. Its denominator is the supported targets generated from the seed,
not every possible invalid input. Valid request/response condition coverage stays
separate. Reports rebuild all of these results from persisted evidence without
running hooks or contacting the provider.

## Inferred mock assumptions

Every dependency explicitly marked `upstream_kind: mock` (or `kind: mock` in
`.wiretap.yaml`) gets an OpenAPI 3.1 YAML draft under
`dependencies/mocks/<dependency-hash>.openapi.yaml`. The Dependencies page and
HTML dashboard link to it by dependency ID. Wiretap refreshes these drafts as
attempts are committed and when the run finishes; `wiretap report --bundle ...`
rebuilds them from the same verified evidence. No generation option or export
command is needed.

Each draft combines captured requests and responses for one dependency ID
across the run, even when a provider spec was supplied. Supplied specs remain
frozen under `schemas/` and continue to enforce the contract. Without a supplied
spec, explicitly set `schema_required: false`; generated observations do not
satisfy the spec requirement or give coverage a declared-operation denominator.
Services, unknown upstream kinds, and the SUT do not get inferred drafts.
An unused mock gets an empty `paths` document labelled as having no captured
exchanges. Wiretap cannot infer unused mock definitions.

Drafts describe **observed mock assumptions**, not provider contracts or proven
consumer requirements:

- Endpoints, methods, query parameters, content types, response statuses, and
  JSON body shapes come from retained, redacted traffic.
- Numeric and UUID path segments become inferred path parameters. Other segments
  remain literal; these heuristics may need human review. Query parameters retain
  string values or arrays of strings, without publishing their captured values.
- Body fields, query parameters, and request bodies are not made required just
  because they appeared in every sample. Extra object properties remain allowed.
- Failed test attempts are included so contract mismatches are visible. Each
  operation has `x-wiretap-evidence` references to attempt JSON and flow IDs, with
  test status and incomplete-capture flags. Reference paths are relative to the
  draft file. Root `x-wiretap` metadata identifies the dependency and run snapshot.
- Transport failures and incomplete HTTP exchanges are excluded from operations
  and recorded with reasons in the root evidence list. Truncated or unreadable
  JSON bodies leave that content type's schema unconstrained. Redaction can also
  affect inferred shapes; captured payload values are not emitted as examples.

These files are generated views, never validation inputs or authoritative
provider claims. Editing or deleting a draft cannot change evidence, validation,
or coverage. Preserve the full bundle to keep provenance links usable.

The bundle can be browsed with a Markdown file browser or a repository viewer:
start at `index.md` and follow the relative links. Keep the complete directory
when copying or uploading it; the pages link to JSON evidence, frozen schemas,
and retained body files. The HTML dashboard uses relative links and inline
styling and script so it can be opened from a copied bundle without a server.
Wiretap's grouped pages and test pages also work as ordinary Markdown.

An outbound exchange says what the SUT received from a dependency during that
test. If the upstream is declared `mock`, the exchange records behaviour the
test's mock supplied. It does not show which response fields the SUT consumed,
or prove a real provider gives the same response. Wiretap records these unknowns
explicitly. Compare provider and consumer repositories only after selecting
the intended runs and source revisions; the bundle alone does not perform a
provider agreement check.

Provided `api_id` and dependency `asset_id` can identify the same API across
repositories. Configure them in the test harness, without changing the SUT.
The optional `upstream_kind` is `mock`, `service`, or `unknown` (default).
Set `sut_target_kind` and `test_source`/`sut_source` when known. Omitted source
details remain unknown; a test checkout revision is not automatically the
revision of an externally running SUT.

The format contracts are [JSON evidence](bundle-evidence.md),
[bundle knowledge](bundle-knowledge.md) and the
[Wiretap OKF profile](okf-profile.md). Standard trace/serial guidance remains in
the [adoption notes](otel-adoption.md).
