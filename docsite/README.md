# docsite

The Skylands documentation site, published at
[docs.skylands.ai](https://docs.skylands.ai). It's
[Starlight](https://starlight.astro.build) on Astro, styled like the citadel's
web client: the same island mark, fonts, and colors.

- `src/content/docs/` holds the pages, in Markdown. Each page's path is its
  address, such as `getting-started/installation.md` at
  `/getting-started/installation/`.
- `astro.config.mjs` sets the sidebar, the logo, and the site's address.
- `src/styles/theme.css` maps the web client's colors and fonts onto
  Starlight's.
- `src/assets/mark.svg` is the island mark from the web client's header, and
  `public/favicon.svg` is the skylands.ai favicon.

Astro needs Node 22.23.4 or newer.

```sh
npm ci
npm run dev      # serves the site with live reload at http://localhost:4321
npm run build    # builds it into dist/
```

From the repository root, `make docs` installs and builds in one step.

## Publishing

`.github/workflows/docs.yml` builds the site with `make docs` and publishes
`dist/` to GitHub Pages when a release tag (`v*`) is pushed, so the site
describes the binaries people download. Running the workflow by hand from the
Actions tab publishes the branch it runs on, `main` unless you pick another. A
pull request that changes `docsite/` only builds it.
