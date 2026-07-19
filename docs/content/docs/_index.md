---
title: Docs
next: getting-started
weight: 1
---

`bonsai` keeps your Go binary small the way you keep a tree small: deliberate pruning. It
builds your target, works out which dependencies are actually driving its size and your
minimum Go version, and tells you how much each one would save if you cut it — including the
transitive ones you never imported directly.

The thing it's built around: **"this dep is 8 MB, so dropping it saves 8 MB" is almost never
true.** Most of that weight is shared with other dependencies that aren't going anywhere. What
matters is how much *only it* keeps alive. bonsai gives you that number so the call stays yours.

## Start here

{{< cards >}}
  {{< card link="getting-started" title="Getting started" subtitle="Install, run your first prune, and read the output." >}}
  {{< card link="commands" title="Commands" subtitle="One subcommand per question — size, prune, go floor, diff, matrix, check." >}}
  {{< card link="configuration" title="Configuration" subtitle="The .bonsai.yaml file: controlled/locked deps, budgets, and the platform matrix." >}}
  {{< card link="methodology" title="How it works" subtitle="The retained-size idea, dominator trees, and the Shapley split — start to finish." >}}
{{< /cards >}}

## The mental model in one minute

bonsai treats your binary like a memory heap and your dependencies like objects on it. A
dependency's honest cost isn't its own size — it's its **retained size**: itself plus
everything that becomes unreachable if it goes away. Shared weight is credited to nobody,
because it doesn't disappear when just one of its holders leaves.

Everything bonsai reports is a reading off that one idea. If you only remember one thing:
rank by what a cut *actually frees*, not by what's biggest.
