---
title: Configuration
prev: commands
next: methodology
weight: 4
---

bonsai reads a single `.bonsai.yaml` at your module root. Everything in it lives in version
control next to your code, and every command reads the same file — the CLI, the TUI, and the
MCP server all honor it.

## Keep the cut cut: `check`

Pruning a dependency once doesn't keep it gone: a `go get` re-adds it, a transitive bump quietly
grows the binary, a new dep raises your `go` floor. `bonsai check` turns the analysis into a CI
gate. It reads a committed budget, runs the same build-and-resolve as the other commands, and
exits non-zero when the budget is violated.

```yaml
check:
  max-binary-size: 25MB          # gates the accounted (~ stripped / release) size; --binary gates on-disk size instead
  max-go-version: "1.23"         # fail if the dep-imposed go floor climbs above this
  deny:                          # modules that must never reappear in the build
    - github.com/aws/aws-sdk-go
    - cloud.google.com/go/...
  max-module-size:               # optional per-module size caps (pattern -> size)
    github.com/klauspost/compress: 2MB
  action: fail                   # what a violation does: fail (non-zero exit) | warn (print only)
```

```sh
bonsai check .                   # exit 0 = pass, 2 = budget violated, 1 = tool/config error
```

Exit code **2** means "the gate failed" and **1** means "the tool broke", so CI can tell them
apart. `deny` and `max-module-size` take the same patterns as `--lock`/`--controlled`
(`github.com/org/...`, globs). `--output json` emits the machine form. An absent `check:` block
exits 0 with a note. Set `action: warn` to print violations without failing the build.

## The analysis block

`controlled` and `matrix` live under `analysis`, so the class lists and platform cells sit
together in one place:

```yaml
analysis:
  controlled:
    - github.com/yourorg/...
  matrix:
    - { goos: linux,   goarch: amd64 }
    - { goos: darwin,  goarch: arm64 }
    - { goos: windows, goarch: amd64 }
    - { goos: linux,   goarch: amd64, tags: [netgo] }
```

### goreleaser auto-detection

Already ship with goreleaser? Then you don't declare a matrix at all: if there's a
`.goreleaser.yaml`, bonsai uses it automatically, deriving the cells (and each build's
tags/env/flags) from your release config — so the cells you analyze are exactly the cells you
release. An explicit `matrix:` or `--platform` takes precedence, and you can turn it off:

```yaml
analysis:
  goreleaser: false             # ignore a .goreleaser.yaml that's present (default: use it)
```

### Persisted build settings

If your build needs specific flags, tags, or env to resolve the same graph the real build does,
persist them under `analysis.build`. They apply to every command, and the matrix's per-cell
`tags` extend `build.tags`. (When a `.goreleaser.yaml` is in play, bonsai fills these in from
your release config instead, and that wins over anything here; set `goreleaser: false` to use
these by hand.)

```yaml
analysis:
  build:
    tags: [netgo]                 # extra build tags, merged into every cell
    env:                          # env overrides (CGO_ENABLED affects which files compile)
      CGO_ENABLED: "0"
    args: "-trimpath"             # freeform go build flags (-gcflags, -buildmode, ...)
```

{{< callout >}}
bonsai's own `-o`/`-ldflags=-dumpdep` always win over `analysis.build.args`, so a stray
`-ldflags` here can't strip the build out from under the analysis.
{{< /callout >}}
