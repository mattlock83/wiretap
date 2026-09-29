# Capture bundles

Each Wiretap runtime writes one portable run directory under `./artifacts` by
default. Set `WIRETAP_OUTPUT_DIR` to choose another parent directory.

The `bundle_path` returned by `/v1/startRun` and `/v1/setupTest` identifies
the exact run directory. Open `viewer/index.html` for the offline HTML dashboard,
or `index.md` for the OKF pages. Both browse the API, dependency observations,
test attempts, operation coverage, and contract conditions. The runtime updates these views as
attempts finish. `/v1/finishRun` finalizes the run after all attempts
have been torn down. A copied bundle remains readable without the original
workspace, source schemas, or a live Wiretap process.

```text
artifacts/<run-id>/
  index.md              OKF entry page
  viewer/index.html     offline interactive HTML dashboard
  run.md                lifecycle and pending attempts
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

The JSON evidence, frozen schemas, and payloads are authoritative. The HTML
dashboard and OKF Markdown pages are generated views. A test attempt page links to every captured
exchange; dependency pages group observations by declared operation and keep
each attempt/flow reference. Editing a Markdown summary cannot change coverage
or exports. If views are missing or stale, rebuild them:

```bash
go run ./cmd/wiretap report --bundle ./artifacts/<run-id>
```

The command prints the absolute `index.md` path and also regenerates
`viewer/index.html`. All required data comes from the bundle. Body previews
are limited to 4 KiB in both views; attachments keep
the retained bytes. The capture limit still applies, so a truncated attachment
is not a complete original body.

The HTML dashboard uses embedded styling and a small filter script; it needs no
server, network connection, or external libraries. Its links stay within the
bundle. Diagnostic JSON/YAML are optional
`--format json` or `--format yaml`
exports. Those files are not generated during normal capture.

## Contract conditions

The **Conditions** tab and `contract-coverage.md` list the keys derived from each
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

The format contracts are [JSON evidence](bundle-evidence.md) and the
[Wiretap OKF profile](okf-profile.md). Standard trace/serial guidance remains in
the [adoption notes](otel-adoption.md).
