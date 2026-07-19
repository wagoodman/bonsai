# bonsai docs site

The [bonsai.dev docs](https://wagoodman.github.io/bonsai/) — a Hugo site using the [Hextra](https://github.com/imfing/hextra) theme, published to GitHub Pages.

## For humans and agents reading the source

All the prose lives as plain Markdown under [`content/`](content/), so you can read the docs straight from a local checkout without building anything:

```
content/
  _index.md              landing / hero page
  docs/
    _index.md            docs overview + "mental model in one minute"
    getting-started.md   install, first run, reading the output
    commands.md          every subcommand, the TUI, and the MCP server
    configuration.md     the .bonsai.yaml file
    methodology.md       the deep-dive on how bonsai works
```

These pages are canonical. The repo's [`README.md`](../README.md) is the short pitch (the GIF, install, the core idea) and points here for everything else, so when behavior changes, update the relevant page under `content/` and only touch the root `README.md` if the pitch itself moved.

## Running it locally

Needs only **Go** (it fetches the theme module; no Node required). Hugo Extended is pinned in
[`../.binny.yaml`](../.binny.yaml) and installed on demand by the tasks below, so you don't need
to install it yourself. From the repo root:

```sh
task docs:serve      # live-reloading server at http://localhost:1313/bonsai/
task docs:build      # one-off production build into docs/public/
```

Both install the pinned Hugo via [binny](https://github.com/anchore/binny) into `.tool/` first
(the same edition CI uses), then run it. To grab it by hand: `binny install hugo`.

## How it's wired

- **Theme**: pulled in as a Hugo module (`go.mod` here), pinned to a Hextra release. No vendored
  theme in-tree. Bump it with `hugo mod get -u github.com/imfing/hextra`.
- **Colors**: [`assets/css/custom.css`](assets/css/custom.css) — the violet/gold/dark-grey
  palette lifted from the TUI demo.
- **Publishing**: `.github/workflows/pages.yaml` builds this site and deploys it to GitHub Pages
  on every push to `main` that touches `docs/`.
