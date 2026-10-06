# Wiretap landing page

Wiretap leads the header, page title and product content. Phrond appears only as
the footer attribution. Repository URLs retain the existing GitHub organisation.

This is plain HTML, CSS and a small script. There is no build step, package
installation, external font, analytics or service dependency. All assets use
relative URLs, so the page works at a GitHub Pages project path or a custom domain.

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
Wiretap report or measured customer results. Keep them consistent with the product
docs. Do not turn mock evidence into a claim of proven provider agreement.

Without JavaScript the first example, all product copy, quickstart and FAQs still
work. With JavaScript, the example supports Left/Right arrows, Home and End. The
copy button selects the command text if clipboard access is unavailable.

## Publish with GitHub Pages

For free hosting, make the repository public when you are ready. The included
workflow runs **only on manual dispatch**, so pushing page changes does not publish
them automatically.

1. Commit and push `site/` and `.github/workflows/pages.yml` to `main`.
2. In the repository's **Settings → Pages**, select **GitHub Actions** as the source.
3. In **Actions → Deploy Wiretap landing page**, choose **Run workflow** on `main`.
4. Open the URL reported by the deployment. Without a custom domain or an inherited
   organisation domain, the project URL is `https://phrond.github.io/wiretap/`.

Repeat step 3 to publish updates. The workflow uploads only `site/`; it does not
publish captured bundles, test payloads or other repository content.

See GitHub's [custom workflow guide](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

## Connect your domain

First verify domain ownership using GitHub's
[domain verification guide](https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/verifying-your-custom-domain-for-github-pages).
In **Settings → Pages → Custom domain**, set the hostname you want to use, then
configure its DNS and enable **Enforce HTTPS** once GitHub has issued the certificate.
Follow GitHub's [domain setup instructions](https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/managing-a-custom-domain-for-your-github-pages-site)
for the correct records for an apex domain or subdomain.

No domain is hardcoded in this page. A custom Actions deployment uses the Pages
domain setting; a `CNAME` file is not needed. Once the final public URL is chosen,
add its canonical URL and `og:url` to `index.html`.

The page can use the main domain now and move under `/wiretap/` if you introduce
other products later.
