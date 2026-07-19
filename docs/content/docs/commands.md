---
title: Commands
prev: getting-started
next: configuration
weight: 3
---

There are a handful of subcommands, each answering one question:

| Command | The question |
|---|---|
| `bonsai .` | What's in my binary, and which module owns each byte? |
| `bonsai prune .` | Which dependencies are worth cutting, ranked, and in what order? |
| `bonsai go-version .` | How low can my `go` directive go, and which deps pin it? |
| `bonsai diff REF .` | What did this branch do to my size and go floor versus REF? |
| `bonsai matrix .` | Across every platform I ship, what's the worst-case floor, and which deps are platform-specific? |
| `bonsai inspect MODULE .` | I'm cutting module X, so which files do I edit, and what happens? |
| `bonsai check .` | Is the committed budget still met? (a CI gate; non-zero exit on violation) |

## How bonsai sorts your dependencies

Every dependency lands in one of four buckets, and the bucket decides whether bonsai will ever
suggest cutting it:

- **1st-class** — code you control: your main module plus anything `--controlled` matches.
  bonsai never prunes these; it looks for imports to cut *out* of them.
- **2nd-class** — a dependency your 1st-class code imports directly. These are the real prune
  candidates.
- **3rd-class** — a dependency reached only *through* other dependencies. You can't drop it
  directly; it leaves only when whatever pulls it in leaves. (Most of your graph.)
- **locked** — off-limits, never suggested. Everything 1st-class is locked by default; `--lock`
  locks more, `--unlock` re-opens a module for consideration.

Widening `--controlled` promotes 3rd-class deps into 2nd-class candidates. That's the lever.

## The prune plan

Prunes interact — shared weight only frees once the last thing holding it is gone — so the plan
orders the cuts and shows, for each one, whether the weight is the dep's own code or the stuff
it drags out behind it:

```text
1.  +1.1 MB  (cumulative 1.1 MB)  github.com/modelcontextprotocol/go-sdk
     own code    277.8 kB  24.8%
     drags out   841.9 kB  75.2%
        249.5 kB  math/big (std)
        212.1 kB  github.com/segmentio/encoding
        143.5 kB  github.com/google/jsonschema-go
        ...
```

So the go-sdk module is a quarter its own code; the rest is the cluster it pulls in. Worth
knowing before you start removing imports.

## One build is one platform

Every subcommand except `matrix` analyzes a *single* build: whatever the host produces. That
quietly makes two answers platform-specific. Binary size shifts across `GOOS`/`GOARCH` and build
tags, and the go-version floor is worse — the set of modules in the build changes with the
platform, so your repo can be 1.21 on linux and 1.23 on windows. The floor that actually
constrains your `go.mod` is the max across every platform you ship.

```sh
bonsai matrix .                 # worst-case floor, no builds (just `go list` per cell)
bonsai matrix . --size          # also build each cell and attribute per-cell size
bonsai matrix . --platform linux/amd64 --platform windows/amd64   # ad-hoc, ignore the config
```

Declare the cells once in `.bonsai.yaml` (see [Configuration](../configuration)) so they live in
version control. Already ship with goreleaser? Then you don't declare a matrix at all — bonsai
derives the cells from your `.goreleaser.yaml` automatically.

## Four ways to look at it

The same analysis comes out in whatever form fits who's reading it:

- **Tables** (`--output table`, the default) — for you, at the terminal. The quick read.
- **JSON** (`--output json`) — for machines: scripts, CI gates, your own tooling.
- **The TUI** (`bonsai` with no subcommand) — for grokking. Try prunes and watch them interact.
- **MCP** (`bonsai mcp`) — for an AI agent editing your codebase.

### The TUI

```sh
bonsai .
```

Everything starts checked (in your build). Uncheck a dependency and the header reprojects the
size right away, while the side panes show what it drags out, what survives because something
else still needs it, and why it's in the build. The `M`/`1`/`2`/`3`/`L` column is the four
classes above; reclassify on the fly and watch the candidate set move. Nothing is applied
(enter just prints the prune set), but lock and class edits save to `.bonsai.yaml`. Press `?`
for the legend.

### MCP

```sh
bonsai mcp        # a Model Context Protocol server over stdio
```

Why a server instead of just shelling out to the CLI? Because the CLI rebuilds your target on
every run, and an agent doesn't ask once: it orients, locates cuts, edits, re-measures, repeats.
The server builds once and keeps it warm, rebuilding only when you actually change the source,
so that loop stays cheap. It honors the lock and class lists in your `.bonsai.yaml` just like
every other command.

| Tool | The agent's question |
|---|---|
| `bonsai_size_targets` | Where are the biggest size wins (ranked by prize), and what effort does each take? |
| `bonsai_go_floor` | How low can my `go` directive go, and which deps pin it? |
| `bonsai_locate_cuts` | I'm cutting module X, so which files/lines do I edit, and what happens? |
| `bonsai_anatomy` | What's the binary's size shape now? |
| `bonsai_measure` | Did my edit shrink it / lower the floor? (a cheap re-check) |

`bonsai_locate_cuts` returns the concrete first-party import sites (`file:line`) to edit and the
per-entry-package bytes behind them, so a partial rewrite is scoped to what's actually worth it.
