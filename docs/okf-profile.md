# Wiretap OKF profile 1.0

Wiretap's generated pages follow [OKF v0.2](https://github.com/GoogleCloudPlatform/open-knowledge-format/blob/ad30107c/SPEC.md), reviewed at revision `ad30107c`.
This profile describes the readable projection. It is versioned separately
from [JSON capture evidence](bundle-evidence.md). The
[frontmatter JSON Schema](okf-profile.schema.json) applies after parsing YAML.

The root `index.md` uses only `okf_version: "0.2"` in its optional frontmatter.
Other `index.md` files are directory listings without frontmatter. Every other
Wiretap-generated `.md` file is a concept with `type`, `title`, `description`,
`generated: {by: "wiretap/1.0", at: ...}`, and
`wiretap: {profile_version: "1.0", kind, run_id, snapshot_revision}`.
`kind` is `system`, `verification`, `run`, `coverage`, `api`, `operation`,
`assumptions`, or `attempt`. Concept titles identify their API, operation or
test where applicable, so pages retain context when indexed separately.
Wiretap does not set OKF `verified` merely because a test passed.
The bundle's `docs/viewer.html` is a separate disposable HTML view over the
same JSON evidence; it is not an OKF concept or an evidence source.
`docs/contract-coverage.md` uses kind `coverage`. `docs/contract-coverage.json` is a
disposable measurement with separately versioned condition rules, not captured
evidence or an OKF concept.
`docs/system.md` uses kind `system`; `docs/verification.md` uses kind `verification`.
They describe observed boundaries and suggested follow-up work. Their structured
counterpart, `docs/knowledge.json`, is a disposable, separately versioned
[knowledge projection](bundle-knowledge.md), not captured evidence or an OKF
concept. Its tasks do not supply executable code or establish provider agreement.
Likewise, `docs/dependencies/mocks/<dependency-hash>.openapi.yaml` files are disposable
inferred mock assumptions, not OKF concepts or supplied contracts. They link to
source evidence using file-relative `x-wiretap-evidence` paths.

Pages may carry `wiretap.evidence`: a bundle-root-relative JSON path for a run
or attempt, or a list of `{attempt_id, flow_id, path}` pointers for an operation
group. Supporting files also appear in OKF `sources` when applicable. Ordinary
Markdown links lead to readable test pages and raw JSON or body attachments.
The run inventory resolves attempt paths; page filenames are navigation, not
capture identity. Grouped dependency pages never merge distinct observations.

`snapshot_revision` identifies the run snapshot used for that page. An
unchanged attempt page can have an older revision than `bundle.json` and
still be valid for its immutable attempt. A full rebuild rewrites all pages
against one inventory snapshot. Evidence readers and exports do not parse
frontmatter or Markdown, and missing pages do not block them.

Wiretap owns the documented generated paths (`index.md`, `docs/system.md`,
`docs/verification.md`, `docs/knowledge.json`, `docs/run.md`, `docs/coverage.md`,
`docs/contract-coverage.md`, `docs/contract-coverage.json`, `docs/viewer.html`,
`docs/dependencies/mocks/*.openapi.yaml`, and its `docs/apis/`, `docs/dependencies/`,
and `docs/tests/` indexes/pages). The root `tests/` contains authoritative JSON
recordings; it is never an output destination for report regeneration.
Regeneration replaces those paths only. Unrecognized team-authored concepts
and extensions are preserved.
