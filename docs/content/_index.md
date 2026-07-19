---
title: bonsai
layout: hextra-home
---

{{< hextra/hero-badge >}}
  <div class="hx:w-2 hx:h-2 hx:rounded-full hx:bg-primary-400"></div>
  <span>Binary size analysis for Go</span>
{{< /hextra/hero-badge >}}

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  Find what's driving the size&nbsp;<br class="hx:sm:block hx:hidden" />of your Go binary
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  bonsai compiles your binary, measures which dependencies drive its size and&nbsp;<br class="hx:sm:block hx:hidden" />
  its minimum Go version, and reports how much each one would save if removed.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mb-6">
{{< hextra/hero-button text="Get started" link="docs/getting-started" >}}
&nbsp;&nbsp;
{{< hextra/hero-button text="How it works" link="docs/methodology" style="background: transparent; border: 1px solid var(--bonsai-violet); color: var(--bonsai-violet);" >}}
</div>

<div class="hx:mt-6"></div>

<p align="center" style="align-self: center;">
  <img src="https://github.com/user-attachments/assets/13915b6a-ef67-4e81-b415-6e08bbf12556" alt="bonsai TUI" width="820" style="border-radius: 8px; max-width: 100%; display: block; margin: 0 auto;" />
</p>

<div class="hx:mt-16 hx:mb-8">
  <h2 style="font-size: 1.875rem; font-weight: 700; letter-spacing: -0.02em; border: 0;">What it does</h2>
</div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Reports retained size"
    subtitle="Each dependency is measured by the bytes that would leave the binary if it were removed, not its source or download size. Weight shared with dependencies you keep is not double counted."
  >}}
  {{< hextra/feature-card
    title="Reads the linker, not go.mod"
    subtitle="bonsai compiles the target and uses the linker's post-DCE reachability, so code that dead-code elimination already stripped is not counted. Nothing is inferred from the module graph."
  >}}
  {{< hextra/feature-card
    title="Ranks candidates by size and effort"
    subtitle="Candidates are ordered by bytes at stake and by how hard they are to remove. Cuts are sequenced so that weight shared between dependencies is attributed and freed in the right order."
  >}}
  {{< hextra/feature-card
    title="Sees inside transitive dependencies"
    subtitle="Mark the code you control and bonsai reports savings from cutting imports inside dependencies-of-dependencies, which tools that only read direct dependencies cannot account for."
  >}}
  {{< hextra/feature-card
    title="Reports the Go version floor"
    subtitle="The minimum Go version your dependencies require, per target platform. Shows which dependency sets the floor and how removing it changes the minimum."
  >}}
  {{< hextra/feature-card
    title="CLI, TUI, JSON, and MCP"
    subtitle="Tables in the terminal, an interactive explorer, JSON output for CI, and an MCP server for agents."
    link="docs/commands"
  >}}
{{< /hextra/feature-grid >}}

<div class="hx:mt-16 hx:mb-4">
  <h2 style="font-size: 1.875rem; font-weight: 700; letter-spacing: -0.02em; border: 0;">How bonsai sees your dependencies</h2>
</div>

<p class="hx:mb-8" style="opacity: 0.82; max-width: 70ch;">Every module lands in one class, and the class decides whether a cut is even possible. The <code>--controlled</code> boundary is the one thing you move, and moving it is how you reach the weight buried deeper in the graph.</p>

<div class="bonsai-lens">
<div class="lens-strata">
<div class="stratum s-first"><div class="k">1st-class</div><div class="t">Your code</div><div class="d">Your main module plus anything <code>--controlled</code> matches. Never pruned; bonsai looks for imports to cut <em>out</em> of it.</div><div class="tag">locked</div></div>
<div class="stratum s-second"><div class="k">2nd-class</div><div class="t">Direct dependencies</div><div class="d">Imported directly by your 1st-class code. The real prune candidates: the surface bonsai can actually cut.</div><div class="tag">cuttable</div></div>
<div class="lens-shift">&darr; widen <code>--controlled</code> to push this boundary deeper</div>
<div class="stratum s-third"><div class="k">3rd-class</div><div class="t">Transitive dependencies</div><div class="d">Reached only <em>through</em> other deps. Most of your graph, and <span class="weight">where most of the binary's weight hides</span>. A module here only leaves when whatever pulls it in leaves.</div></div>
</div>
<p class="lens-note">Widening <code>--controlled</code> promotes a whole layer of 3rd-class deps into 2nd-class candidates. That is the lever: it exposes the cuttable surface further down and lets bonsai wring out weight that tools reading only your <code>go.mod</code> never see.</p>
</div>

<div class="hx:mt-12"></div>

{{< hextra/hero-button text="Read the docs →" link="docs" >}}
