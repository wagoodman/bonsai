---
title: Getting started
prev: /docs
next: commands
weight: 2
---

## Install

```sh
curl -sSfL https://raw.githubusercontent.com/wagoodman/bonsai/main/install.sh | sh
```

That picks a sensible spot on its own: an existing writable dir on your `PATH` (like
`~/.local/bin`) if you have one, otherwise `/usr/local/bin`, prompting for `sudo` only when it
has to.

Want a specific location instead? Pass `-b DIR`:

```sh
curl -sSfL https://raw.githubusercontent.com/wagoodman/bonsai/main/install.sh | sh -s -- -b /usr/local/bin
```

Pin a version by appending a tag (`... | sh -s -- v0.1.0`), or grab a prebuilt archive from the
[releases page](https://github.com/wagoodman/bonsai/releases).

## Your first run

```sh
bonsai prune .          # rank what's worth pruning in your go module
```

It compiles your target, which is how it gets ground truth instead of guessing from `go.mod`.
Already have a built binary? Add `--binary ./mything` to skip the build.

## Reading the output

`bonsai prune` ranks candidates on two axes: the **prize** (bytes at stake if the module left
the binary) and the **effort** to realize it. Ranking by prize instead of "what's easiest to
remove" keeps the big wins from hiding.

```text
biggest win: github.com/hashicorp/go-getter → 17.9 MB at stake, pinned by github.com/anchore/syft (replace or patch); easiest win: github.com/glebarez/sqlite → 3.9 MB now

  PRIZE     EXCL     EFFORT       BLOCKER  POT       GET%   IMP-SITES  MODULE
  17.9 MB   0 B      pinnedByDep  syft     0 B       -      2          github.com/hashicorp/go-getter
  3.9 MB    3.9 MB   quickWin              4.5 MB    85.7%  4          github.com/glebarez/sqlite
  904 kB    0 B      coordinated           907 kB    0.0%   23         gorm.io/gorm
```

- **PRIZE** is the full-graph retained size — bytes gone if the module vanishes by any means.
- **EXCL** is what you bank by cutting your *own* imports alone.
- When they differ, **EFFORT** says why: `quickWin` (cut your imports), `coordinated` (co-prune
  the sibling targets), `pinnedByDep` (a dep you don't control holds it — replace or patch that
  dep), or `core` (too wired in to cut).

The go-getter row is the case worth the reframe: 0 bytes freed by a clean cut, but 17.9 MB at
stake behind another dependency.

## The one knob worth setting

{{< callout type="warning" >}}
`--controlled` is the flag that makes bonsai useful. Without it you get shallow results.
{{< /callout >}}

By default bonsai assumes the only code you can edit is the module you're scanning, so the only
refactors it considers are cutting your direct dependencies. But "code you can edit" is usually
bigger than one module: your org's libraries, a fork you maintain, anything you can send a PR
to. Mark one of those as controlled and bonsai can suggest cutting an import inside *that*
library — a dependency of a dependency, below anything your `go.mod` mentions. That's usually
where the real savings hide.

```sh
bonsai prune . --controlled "github.com/yourorg/..."
```

A few more flags:

- `--lock <pattern>` — lock things you'll never drop so they stop showing up as suggestions.
- `--unlock <module>` — the opposite: treat something you own as fair game to drop wholesale.
- `--blame` — split each dependency's fair share of the shared weight so the numbers add up to
  the real total (the Shapley value).

## Next: keep the cut cut

Pruning a dependency once doesn't keep it gone — a `go get` re-adds it, a transitive bump
quietly grows the binary. Turn the analysis into a CI gate with `bonsai check` and a committed
budget. See [Configuration](../configuration) for the `.bonsai.yaml` file.
