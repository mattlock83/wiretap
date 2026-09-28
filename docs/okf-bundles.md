# Capture bundles

Each Phrond runtime writes one portable run directory under `./artifacts` by
default. Set `PHROND_OUTPUT_DIR` to choose another parent directory. The older
`PHROND_ARTIFACT_DIR` name remains an alias for this migration; set only one,
or give both the same value.

The `bundle_path` returned by `/v1/startServer` and `/v1/setupTest` identifies
the exact run directory. Open `viewer/index.html` for the offline HTML dashboard,
or `index.md` for the OKF pages. Both browse the API, dependency observations,
test attempts, and operation coverage. The runtime updates these views as
attempts finish. `/v1/killServer` finalizes the run after all attempts
have been torn down. A copied bundle remains readable without the original
workspace, source schemas, or a live Phrond process.

```text
artifacts/<run-id>/
  index.md              OKF entry page
  viewer/index.html     offline interactive HTML dashboard
  run.md                lifecycle and pending attempts
  coverage.md           observed API operation coverage
  apis/                 provided API and operation pages
  dependencies/         grouped observed dependency behaviour
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
go run ./cmd/phrond-coverage --bundle ./artifacts/<run-id>
```

The command prints the absolute `index.md` path and also regenerates
`viewer/index.html`. All required data comes from the bundle. Body previews
are limited to 4 KiB in both views; attachments keep
the retained bytes. The capture limit still applies, so a truncated attachment
is not a complete original body.

The HTML dashboard uses embedded styling and a small filter script; it needs no
server, network connection, or external libraries. Its links stay within the
bundle. Generated OpenAPI, Workflow YAML, JSON diagnostic, and the older
standalone HTML report remain explicit `--format` exports. Those export files
are not generated during normal capture.

The bundle can be browsed with a Markdown file browser or a repository viewer:
start at `index.md` and follow the relative links. Keep the complete directory
when copying or uploading it; the pages link to JSON evidence, frozen schemas,
and retained body files. The HTML dashboard uses relative links and inline
styling and script so it can be opened from a copied bundle without a server.
Phrond's grouped pages and test pages also work as ordinary Markdown. The
serial basic REST fixture contains one test attempt and three exchanges;
the generated-view test also checks a synthetic 100-attempt, 1,000-exchange
bundle for bounded previews and a complete grouped evidence list.

An outbound exchange says what the SUT received from a dependency during that
test. If the upstream is declared `mock`, the exchange records behaviour the
test's mock supplied. It does not show which response fields the SUT consumed,
or prove a real provider gives the same response. Phrond records these unknowns
explicitly. Compare provider and consumer repositories only after selecting
the intended runs and source revisions; the bundle alone does not perform a
provider agreement check.

Provided `api_id` and dependency `asset_id` can identify the same API across
repositories. Configure them in the test harness, without changing the SUT.
The optional `upstream_kind` is `mock`, `service`, or `unknown` (default).
Set `sut_target_kind` and `test_source`/`sut_source` when known. Omitted source
details remain unknown; a test checkout revision is not automatically the
revision of an externally running SUT.

For a historical YAML directory, conversion is explicit:

```bash
go run ./cmd/phrond-coverage --artifacts ./old-artifacts \
  --format okf --output ./converted-bundle
```

This reads old captures without changing them. If an old schema is missing or
its recorded hash cannot be verified, the imported bundle marks it unavailable
and does not invent an API coverage denominator. Capture-time completeness and
source provenance that were never recorded cannot be recovered by conversion.

The format contracts are [JSON evidence](bundle-evidence.md) and the
[Phrond OKF profile](okf-profile.md). Standard trace/serial guidance remains in
the [adoption notes](otel-adoption.md).
