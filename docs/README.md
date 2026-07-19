# bonsai docs site

The [bonsai.dev docs](https://wagoodman.github.io/bonsai/) — a Hugo site using the
[Hextra](https://github.com/imfing/hextra) theme, published to GitHub Pages.

## For humans and agents reading the source

All the prose lives as plain Markdown under [`content/`](content/), so you can read the docs
straight from a local checkout without building anything:

```
content/
  _index.md              landing / hero page
  docs/
    _index.md            docs overview + "mental model in one minute"
    getting-started.md   install, first run, reading the output
    commands.md          every subcommand, the TUI, and the MCP server
    configuration.md     the .bonsai.yaml file
    methodology.md       the deep-dive (mirrors ../../METHODOLOGY.md)
```

Content is *derived* from the repo's [`README.md`](../README.md) and
[`METHODOLOGY.md`](../METHODOLOGY.md) but rewritten for readers rather than repo browsers. When
behavior changes, update the root docs first (they're canonical) and reflect it here.

## Running it locally

Needs **Hugo Extended** and **Go** (Go fetches the theme module; no Node required). On macOS:

```sh
brew install hugo
```

Then, from the repo root:

```sh
task docs:serve      # live-reloading server at http://localhost:1313/bonsai/
task docs:build      # one-off production build into docs/public/
```

Or directly, from this directory: `hugo server` / `hugo --minify`.

## How it's wired

- **Theme**: pulled in as a Hugo module (`go.mod` here), pinned to a Hextra release. No vendored
  theme in-tree. Bump it with `hugo mod get -u github.com/imfing/hextra`.
- **Colors**: [`assets/css/custom.css`](assets/css/custom.css) — the violet/gold/dark-grey
  palette lifted from the TUI demo.
- **Publishing**: `.github/workflows/pages.yaml` builds this site and deploys it to GitHub Pages
  on every push to `main` that touches `docs/`.
