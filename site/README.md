# Wiretap landing page

Wiretap leads the header, page title and product content. Phrond appears only as
the footer attribution. Repository URLs point to `mattlock83/wiretap`.

This is plain HTML, CSS and a small script. There is no build step, package
installation, external font, analytics or service dependency. Assets use relative
URLs so the page can be served from a root or project path.

## Preview locally

From the repository root:

```bash
python3 -m http.server 4173 --bind 127.0.0.1 --directory site
```

Open <http://127.0.0.1:4173/>. Stop the server with Ctrl+C.

## Edit the page

- `index.html`: copy, links, examples and accessible page structure.
- `styles.css`: colours, typography and responsive layout.
- `site.js`: the example's keyboard-accessible tabs and the quickstart copy button.
- `wiretap-icon.svg`: the reusable Wiretap mark; the page reuses its `#mark` symbol.
- `favicon.svg`: the same mark on a dark background for browser tabs.
- `phrond-icon.svg`: the footer's fern-shaped connected graph.

The W follows the traffic path. Its three upper nodes represent the tests,
application and mocks; its two lime lower nodes represent Wiretap's capture
points between them. Keep that geometry consistent between the mark and favicon.

The Phrond footer mark combines a fern silhouette with a diagonal stem, angular
branches and graph nodes, reflecting its roots in visualising dependencies.

The order flow and coverage cases are **illustrative examples**, not a live
Wiretap capture interface or measured customer results. The page describes the
implemented integrations and outputs in the [support matrix](../docs/support.md).
Product plans belong in [the roadmap](../docs/roadmap.md).

Without JavaScript the first example, all product copy, quickstart and FAQs still
work. With JavaScript, the example supports Left/Right arrows, Home and End. The
copy button selects the command text if clipboard access is unavailable.

## Publish with GitHub Pages

The [included workflow](../.github/workflows/pages.yml) runs **only on manual
dispatch** on `main`. Pushing page changes does not publish them automatically.
The repository's GitHub Pages configuration must use GitHub Actions as its source.

1. Commit and push `site/` and `.github/workflows/pages.yml` to `main`.
2. In the repository's **Settings → Pages**, select **GitHub Actions** as the source.
3. In **Actions → Deploy Wiretap landing page**, choose **Run workflow** on `main`.
4. Open the URL reported by the deployment.

Repeat step 3 to publish updates. The workflow uploads only `site/`; it does not
publish captured bundles, test payloads or other repository content.

See GitHub's [custom workflow guide](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).
