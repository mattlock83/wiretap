# Phrond OKF profile 1.0

Phrond's generated pages follow [OKF v0.2](https://github.com/GoogleCloudPlatform/open-knowledge-format/blob/ad30107c/SPEC.md), reviewed at revision `ad30107c`.
This profile describes the readable projection. It is versioned separately
from [JSON capture evidence](bundle-evidence.md). The
[frontmatter JSON Schema](okf-profile.schema.json) applies after parsing YAML.

The root `index.md` uses only `okf_version: "0.2"` in its optional frontmatter.
Other `index.md` files are directory listings without frontmatter. Every other
Phrond-generated `.md` file is a concept with `type`, `title`, `description`,
`generated: {by: "phrond/1.0", at: ...}`, and
`phrond: {profile_version: "1.0", kind, run_id, snapshot_revision}`.
`kind` is `run`, `coverage`, `api`, `operation`, `assumptions`, or `attempt`.
Phrond does not set OKF `verified` merely because a test passed.
The bundle's `viewer/index.html` is a separate disposable HTML view over the
same JSON evidence; it is not an OKF concept or an evidence source.

Pages may carry `phrond.evidence`: a bundle-root-relative JSON path for a run
or attempt, or a list of `{attempt_id, flow_id, path}` pointers for an operation
group. Supporting files also appear in OKF `sources` when applicable. Ordinary
Markdown links lead to readable test pages and raw JSON or body attachments.
The run inventory resolves attempt paths; page filenames are navigation, not
capture identity. Grouped dependency pages never merge distinct observations.

`snapshot_revision` identifies the run snapshot used for that page. An
unchanged attempt page can have an older revision than `evidence/run.json` and
still be valid for its immutable attempt. A full rebuild rewrites all pages
against one inventory snapshot. Evidence readers and exports do not parse
frontmatter or Markdown, and missing pages do not block them.

Phrond owns the documented generated paths (`index.md`, `run.md`,
`coverage.md`, `viewer/index.html`, and its `apis/`, `dependencies/`, and `tests/` indexes/pages).
Regeneration replaces those paths only. Unrecognized team-authored concepts
and extensions are preserved.
