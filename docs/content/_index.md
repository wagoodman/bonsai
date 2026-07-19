---
title: bonsai
layout: hextra-home
---

{{< hextra/hero-badge >}}
  <div class="hx-w-2 hx-h-2 hx-rounded-full hx-bg-primary-400"></div>
  <span>Go dependency dieting</span>
{{< /hextra/hero-badge >}}

<div class="hx-mt-6 hx-mb-6">
{{< hextra/hero-headline >}}
  Make smaller dependency&nbsp;<br class="sm:hx-block hx-hidden" />trees for your Go projects
{{< /hextra/hero-headline >}}
</div>

<div class="hx-mb-12">
{{< hextra/hero-subtitle >}}
  bonsai builds your binary, finds the dependencies driving its size and your&nbsp;<br class="sm:hx-block hx-hidden" />
  minimum Go version, and tells you how much each one would <em>actually</em> save if you cut it.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx-mb-6">
{{< hextra/hero-button text="Get started" link="docs/getting-started" >}}
&nbsp;&nbsp;
{{< hextra/hero-button text="How it works" link="docs/methodology" style="background: transparent; border: 1px solid var(--bonsai-gold);" >}}
</div>

<div class="hx-mt-6"></div>

<p align="center">
  <img src="https://github.com/user-attachments/assets/13915b6a-ef67-4e81-b415-6e08bbf12556" alt="bonsai TUI" width="820" style="border-radius: 8px; max-width: 100%;" />
</p>

## Why would I want this?

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="\"8 MB dep = 8 MB saved\" is a lie"
    subtitle="Most of a dependency's weight is shared with deps you're keeping. bonsai reports retained size — what only that dep keeps alive — the same number a memory profiler shows you."
  >}}
  {{< hextra/feature-card
    title="Ground truth, not go.mod guesses"
    subtitle="It compiles your target and reads the linker's own post-DCE reachability. No over-counting code that dead-code elimination already stripped."
  >}}
  {{< hextra/feature-card
    title="Ranked, ordered prune plan"
    subtitle="Candidates ranked by prize (bytes at stake) and effort (how hard to cut), with the cuts ordered so shared weight frees in the right sequence."
  >}}
  {{< hextra/feature-card
    title="Finds wins below your go.mod"
    subtitle="Mark code you control and bonsai suggests cutting imports inside deps-of-deps — where the real savings usually hide, invisible to tools that only see direct deps."
  >}}
  {{< hextra/feature-card
    title="Also lowers your Go floor"
    subtitle="The same lever that shrinks the binary can drop your minimum go directive. bonsai matrix reports the worst-case floor across every platform you ship."
  >}}
  {{< hextra/feature-card
    title="CLI, TUI, JSON, and MCP"
    subtitle="Tables at the terminal, an interactive explorer for what-ifs, JSON for CI gates, and an MCP server so an AI agent can find and make cuts instead of guessing."
    link="docs/commands"
  >}}
{{< /hextra/feature-grid >}}

<div class="hx-mt-6"></div>

{{< hextra/hero-button text="Read the docs →" link="docs" >}}
