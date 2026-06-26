# How bonsai works

This document explains the ideas underneath bonsai, start to finish. Each section builds
on the one before it, so it's meant to be read in order. By the end you should understand
not just what bonsai reports, but why those numbers are the right ones and where they come
from.

The short version: bonsai treats your binary like a memory heap and your dependencies like
objects on it, then borrows the math a memory profiler uses to answer "how much would I save
if this went away?" Everything below is an elaboration of that one sentence.

## The pipeline at a glance

Here is the whole tool in one picture. Each box is a section below; the arrows are what
flows between them.

```mermaid
flowchart TD
    SRC["your module<br/>(source)"]
    SRC -->|"go build -ldflags=-dumpdep"| BUILD["§3 build the target"]
    BUILD --> BIN["unstripped binary"]
    BUILD --> DUMP["-dumpdep stream<br/>(post-DCE symbol edges)"]

    BIN -->|"§4 symbol table + gopclntab"| SIZES["bytes per package"]
    DUMP -->|"§3 collapse to packages"| GRAPH["reachable import graph"]
    SRC -->|"§11 go/parser AST scan"| COUPLE["coupling + import sites<br/>(file:line edit locations)"]

    SIZES --> WEIGHTED["weighted, reachable<br/>dependency graph"]
    GRAPH --> WEIGHTED
    CLASS["§6 class model<br/>(controlled / locked gives<br/>cuttable edges, prune targets)"] --> WEIGHTED

    WEIGHTED -->|"§7 dominator tree<br/>+ gateway trick"| EXCL["exclusive savings<br/>(retained size)"]
    WEIGHTED -->|"§8 reachIndex: v(S)"| PLAN["greedy ordered<br/>prune plan"]
    WEIGHTED -->|"§9 Shapley value"| BLAME["fair-share blame"]
    WEIGHTED -->|"§10 go directives"| FLOOR["Go-version floor"]

    EXCL --> OUT["outputs:<br/>table · JSON · explore TUI · MCP"]
    PLAN --> OUT
    BLAME --> OUT
    FLOOR --> OUT
    COUPLE --> OUT
```

The left spine (build, then graph plus sizes) is about getting honest data. The class model
decides what you're even allowed to cut. The four analyses in the middle are the math, all of
them reading off the same weighted graph. The bottom is how you read it.

---

## 1. The question, and why the obvious answer is wrong

You add a dependency to do one small thing. It brings its own dependencies, those bring more,
and soon a single line in your `go.mod` is responsible for a surprising chunk of your binary.
The natural question is: if I drop this dependency, how much smaller does my binary get?

The natural (wrong) way to answer it is to measure how big the dependency is and call that
the savings. "This module is 8 MB, so removing it saves 8 MB."

That's almost never true, for one reason: most of a dependency's weight is shared. The 8 MB
module probably pulls in `math/big`, some encoding library, a logging package, and several of
those are also pulled in by other dependencies you have no intention of dropping. If you
remove the 8 MB module, the shared pieces stay, because something else is still holding them
in. The real savings is only the part that nothing else needs.

So the question isn't "how big is this dependency?" It's "how much is being kept alive only by
this dependency?" That distinction is the whole point, and it turns out to be an old,
well-studied problem in disguise.

---

## 2. The analogy that makes it tractable: your binary is a heap

Here is the reframing. Think about how a garbage collector and a memory profiler see a
running program:

- The program has objects in memory.
- Objects hold references to other objects.
- A handful of objects are roots (globals, stack variables), always reachable.
- An object stays alive as long as some path of references reaches it from a root.
- When you ask a profiler "how much memory frees if I drop this object?", it doesn't tell you
  the object's own size. It tells you the object's retained size: the object plus everything
  that would become unreachable along with it, i.e. everything only it was keeping alive.

Now map that onto a compiled Go binary:

| Garbage-collected heap            | Go binary (bonsai)                          |
|-----------------------------------|---------------------------------------------|
| object                            | package                                     |
| reference (object to object)      | import edge (package imports package)       |
| GC roots                          | program entrypoints plus the "backbone"     |
| object is reachable from a root   | package is linked into the binary           |
| bytes freed if object collected   | bytes freed if dependency pruned            |
| retained size of an object        | exclusive prune savings of a dependency     |

The mapping is close enough that the algorithms built for the heap problem carry over. Retained
size is the realistic savings number we wanted in section 1: it credits shared weight to nobody,
because shared weight doesn't disappear when one of its several holders leaves.

The rest of this document is the work of making that mapping precise: getting an accurate
graph, defining "roots" correctly, defining what a "prune" even is, and then reading the
retained sizes off.

---

## 3. Ground truth: build it, don't guess from `go.mod`

Before any of the analysis can run, bonsai needs an accurate picture of what is actually in
your binary. There are two traps to avoid:

1. `go.mod` lies about reach. Your `go.mod` lists modules, but the linker only keeps the code
   paths that are actually used. A module can be in your `go.mod` and contribute almost
   nothing, because dead-code elimination (DCE) stripped the parts you never call. Analyzing
   the source-level import graph systematically over-counts.

2. Source-level call graphs are approximations. Academic debloating tools build a static call
   graph and reason about what might be reachable. That's conservative; it has to assume more
   is live than really is, to stay safe.

bonsai sidesteps both by compiling the target itself and reading the linker's own answer. When
it builds, it passes `-ldflags=-dumpdep`, which makes the Go linker emit the symbol-to-symbol
reference graph it used after dead-code elimination: the reachability the linker actually
computed to decide what to put in the binary. (See `build.go` and `dumpdep.go`.)

A couple of details that matter for accuracy:

- The dump is at the symbol level (`pkg.Func` references `otherpkg.Func`). bonsai collapses
  each edge down to the packages owning the two symbols, keeping only edges between packages it
  already knows about. Imports the linker dropped simply aren't there.
- It filters edges that aren't real source-level calls: compiler scaffolding (argument metadata,
  stack maps, deferred-call bookkeeping) and a stdlib-to-third-party artifact from generic
  instantiation. Left in, these would falsely pin modules as always reachable and zero out their
  prune savings.

If you'd rather not build (or you only have the artifact), you can point bonsai at a prebuilt
binary instead; it then falls back to the source-level import graph, which over-counts (section
1) because it can't see what DCE dropped. Building it ourselves is the default and the accurate
path.

---

## 4. Putting bytes on the graph: size attribution

The graph tells us what's connected. We also need each package's byte weight, so retained sizes
come out in megabytes and not just node counts. This is `binary.go`, working directly on the
compiled artifact.

When the binary is unstripped (what bonsai produces when it builds for you), its symbol table
lists every function and global with an address. bonsai sorts symbols by address and takes the
gap to the next one as that symbol's size (a standard "delta-fill" trick), charging each symbol's
bytes to the package its name belongs to. This is precise for code and named data.

One section, `gopclntab` (the runtime's function metadata, used for stack traces), isn't owned
by any package, so bonsai spreads it across packages in proportion to their code size. It's an
approximation, but a stable one. Bytes with no symbol at all, notably protobuf descriptors, get
swept up by the delta-fill into whatever precedes them, usually a `<generated>` bucket rather
than a real package.

If the binary is stripped, bonsai degrades gracefully: it recovers code sizes from `gopclntab`
via `debug/gosym` (which survives stripping) and leaves data unattributed. A small format-agnostic
adapter handles Mach-O (macOS), ELF (Linux), and PE (Windows), so the rest of the tool never has
to care about binary formats.

The output is a map from package import path to attributed bytes. Combined with the graph from
section 3, we now have a weighted, reachable dependency graph. Everything from here is analysis
on that graph.

---

## 5. Reachability is the GC mark phase

With a weighted graph in hand, the foundational operation is: given the roots, what's alive?
This is literally a tracing garbage collector's mark phase. Start at the roots, follow every
edge, and mark everything you can reach. Whatever you marked is "live" (in the binary);
whatever you didn't is gone. (See `reachable` in `treeshake.go`.)

The interesting part is what a "prune" is in this model. Pruning module B doesn't mean deleting
B's nodes. It means: the code you control stops importing B. So bonsai computes reachability
with a twist: when it traverses, it refuses to follow any edge that goes from code you control
into a module you've decided to cut. Everything that was only reachable through those
now-severed edges falls out of the marked set. The bytes that were marked before but aren't
marked after are your savings.

This runs at package granularity: a package is live if anything still imports it, and savings are
credited per package, not per symbol. That can leave a few bytes on the table when a cut kills
only part of a package, but it keeps the model simple and matches what you actually edit. You
remove imports, not individual symbols.

This is the from-scratch way to answer "what does this cut free?": re-mark the graph with some
edges forbidden, and diff. It's slow to run for every possible cut, so it serves as the reference
that the faster machinery (sections 7 through 9) is tested against.

This framing is tree-shaking, not dead-code elimination: we additively keep what's reachable
from roots, rather than subtractively removing what looks unused. The reachable set is the live
set; the prunable set is everything reachable only through edges you're allowed to cut.

---

## 6. Who can you actually cut? The class model

Section 5 mentioned "code you control" and "modules you're allowed to cut." Those aren't vague;
they're a precise classification that everything else derives from. (See `classify.go`.)

The question bonsai needs to answer isn't "is this dependency used at all?" (a yes/no) but
"which import edges could you realistically stop writing?" An edge is cuttable if and only if it
leaves a module whose source you control and lands on a module that isn't locked (off-limits).
From that single definition, four classes fall out automatically:

- 1st-class: code you control. Always your main module, plus anything you mark with
  `--controlled` (your org's libraries, a fork you maintain). bonsai never proposes pruning
  these; instead it looks for imports to cut out of them.
- 2nd-class: a dependency your 1st-class code imports directly. These are the real prune
  candidates: the imports you could plausibly stop writing.
- 3rd-class: a dependency reached only through other dependencies. You can't drop it directly;
  it leaves only when whatever pulls it in leaves. Most of your graph lives here.
- locked: explicitly off-limits, never suggested. Everything 1st-class is locked by default;
  `--lock` locks more, `--unlock` re-opens a locked module (yours or a dependency) as fair
  game.

You only ever declare two things: which modules are controlled, and which are locked. The
1st/2nd/3rd taxonomy and the set of prune targets (non-locked modules with a cuttable edge into
them) are derived from the graph structure. You never hand-maintain them.

Two things are worth internalizing here:

1. It's a strict generalization. If you set "controlled = {just the main module}", the model
   collapses exactly to the old, familiar "which direct dependencies can I drop?" behavior.
   Widening `--controlled` is the one knob that opens up everything deeper.
2. The real wins usually hide a layer down. Widening `--controlled` promotes a whole layer of
   3rd-class deps into 2nd-class candidates. "My fork of X could stop importing Y" is often
   where the big savings are, a level or two below your `go.mod`, invisible to a tool that only
   looks at direct dependencies.

Back in the GC analogy, the roots are your program's entrypoints plus a backbone whose out-edges
no cut can touch: locked dependencies and the standard library. Whatever they hold up stays no
matter what you prune, so it shows up with zero prunable size, the right behavior for weight
you'll never remove. Controlled code is a root too, but it's where cuts originate, so its imports
into non-locked targets are the prunable edges rather than pinned ones.

---

## 7. The per-dependency savings number: dominator trees

Now we can finally compute the realistic per-dependency savings from section 1, the "retained
size", efficiently, instead of re-running the mark phase once per candidate.

Memory profilers (Chrome DevTools, Eclipse MAT, dotMemory) solve this with a dominator tree.
The defining idea: node *d* dominates node *v* if every path from a root to *v* passes through
*d*. The retained size of *d* is its own size plus everything it dominates: precisely the
weight that becomes unreachable if *d* goes away. Crucially, a dependency shared by two
consumers is dominated by neither (paths reach it two ways); it's dominated by the shared
super-root above them both. So its bytes are credited to nobody alone, which is exactly the
realism we wanted: shared weight doesn't free when one holder leaves. (See `dominator.go`.)

bonsai builds this dominator tree once over the reachable graph and reads every dependency's
exclusive savings straight off it in a single pass. No per-candidate re-marking.

To see why shared weight ends up credited to nobody, picture two dependencies A and Y that you
control imports into. A alone pulls in X; both A and Y pull in the shared library S:

```mermaid
flowchart TD
    R(("roots<br/>(your code +<br/>backbone)"))
    R --> A["A<br/>2 MB"]
    R --> Y["Y<br/>1 MB"]
    A --> X["X<br/>3 MB"]
    A --> S["S (shared)<br/>4 MB"]
    Y --> S

    classDef excl fill:#d8f5d8,stroke:#2e7d32,color:#1b3d1b;
    classDef shared fill:#fff0cc,stroke:#b8860b,color:#4d3a00;
    class A,X excl;
    class S shared;
```

Every root-to-X path goes through A, so A dominates X: pruning A frees X. But S is reachable
two ways (through A or through Y), so A dominates neither path to S. S is dominated by the
shared point above both (the super-root). The consequence:

- Prune A alone and you free A plus X = 5 MB. That's A's exclusive (retained) savings. S stays,
  because Y still holds it.
- The 4 MB of S is credited to neither A nor Y by itself; it only frees if both go. bonsai
  reports it as A's shared weight, with Y named as the co-holder.

That's the whole point of using retained size instead of raw size: A's honest number is 5 MB,
not 9 MB, and the tool tells you S is the 4 MB you'd unlock by dropping Y too.

There's one wrinkle that makes bonsai's version richer than a textbook heap profiler. In a
heap, "free X" means deleting one node. In bonsai, "prune B" means cutting a set of edges:
every import of B that originates in code you control. A dominator tree natively handles
single-node removal, not edge-set removal. bonsai bridges the gap with a small trick: for each
prune target B, it inserts a synthetic gateway node and reroutes all of B's cuttable in-edges
through it. Now "remove the gateway" means exactly "cut all those edges": the edge-set cut
becomes a single-node removal, and the dominator tree handles it without modification. The
retained size of B's gateway is B's exclusive prune savings.

Correctness is pinned by a test: the dominator-derived exclusive savings for every target must
match what the slower tree-shake from section 5 reports. If the fast path and the reference ever
disagree, the test fails.

This stage produces, for each prune candidate:

- FreedBytes: the exclusive, dominated savings, what you actually bank by pruning this one
  target alone.
- PotentialBytes: the freeable weight in its whole subtree, what could come back if this target
  and everything sharing its subtree were all pruned together.
- SharedBytes: the difference, freeable weight that stays put because other targets reach it
  too (with SharedWith naming those other targets, so you can see who to co-prune).

That three-number split is the honest story: you save this much by yourself, this much more is
theoretically there but shared, and here's who you'd have to drop alongside to get it.

---

## 8. Prunes interact: ordering the plan

Exclusive savings answer "what does pruning B alone free?" But in practice you prune several
things, and they interact: a shared dependency only frees once the last thing holding it is
gone. So "prune A saves 5 MB, prune B saves 4 MB" does not mean pruning both saves 9 MB; they
might share weight that only one of them gets credited for once both are cut.

To produce a realistic plan, bonsai needs to evaluate counterfactuals quickly: "if I cut this
whole set of targets, how many bytes go away?" Re-running the section-5 mark phase for every set
would work but is wasteful when you ask it thousands of times. So bonsai compiles the reachable
graph into an integer-indexed structure (`reachindex.go`) where each cuttable edge is tagged
with the target it depends on. Answering "what frees if I cut set S?" becomes a single fast
graph sweep that skips the tagged edges; call it `v(S)`, the value of cutting coalition S.

With cheap `v(S)` in hand, the plan is built greedily: repeatedly pick the target with the
largest marginal saving given everything already chosen, take it, repeat (up to a cap of the top
~25 steps, since the tail savings vanish). Each step reports:

- the marginal bytes it frees (on top of prior steps) and the running cumulative,
- a breakdown of where those bytes come from: the target's own code versus the modules it drags
  out with it (newly orphaned dependencies), largest first,
- for each dragged-out item, the co-prune set: the other targets you'd also have to cut to free
  it (empty if this prune frees it alone).

Greedy is a heuristic, not a proven optimum: shared weight means targets interact in both
directions (two deps that each free little can free a lot together once the last holder is gone),
so no simple ordering is guaranteed best. In practice it surfaces the realistic "prune A for 9 MB,
then B frees another 4 MB net" sequence you'd want to follow, and the per-step marginals show you
where the tail stops being worth it.

The co-prune computation is careful to use the real cut machinery rather than a shortcut. A
naive "anything that can reach this item must be co-pruned" over-counts, because it includes
controlled code whose path runs through the module you're already pruning (that edge gets cut
anyway). Instead, bonsai asks the honest question: un-cut a candidate target, does the item come
back? If yes, it was genuinely necessary. That's what keeps "prune go-sdk, 1.1 MB" reading as
one clean action instead of a noisy list of spurious "also prune X" requirements.

---

## 9. Fair blame: the Shapley value

There's a tension between two of the numbers above. Exclusive savings (section 7) credit shared
weight to nobody, which is honest for "what do I save right now," but it means the exclusive
numbers don't add up to the real total. If A and Y both pull in a 3 MB dependency, that 3 MB is
charged to neither, so summing exclusive savings undercounts what's actually prunable.

Sometimes you want the other view: counting its fair share of everything it drags in, what is
this dependency really costing me? And you want those shares to sum to the true total. This is a
cost-sharing problem, and cooperative game theory has an answer for it: the Shapley value (see
`shapley.go`).

Model pruning as a cooperative game. The players are the prune targets. The value of a coalition
S is `v(S)`, the bytes freed by pruning that set (the same fast function from section 8). The
Shapley value asks: averaged over every possible order in which the targets could be removed,
how much does each target contribute at the moment it's added? A shared dependency's weight
naturally splits among the targets that hold it, because each one is "the one that finally frees
it" in some fraction of orderings.

The Shapley value is the standard answer to fair cost-sharing: shares sum to the true total,
equal contributors get equal blame, and a target that frees nothing gets nothing. That's why
it's the choice here rather than an ad-hoc split.

The catch: computing it exactly means summing over all 2ⁿ coalitions, which is intractable in
general. Two facts rescue it at bonsai's scale:

- For few targets (12 or fewer), bonsai just enumerates all coalitions exactly: 2¹² = 4096
  evaluations of the cheap `v(S)`, no problem.
- For more, it switches to the standard Monte-Carlo permutation estimator: sample random removal
  orderings and average each target's marginal contribution. Because the marginals along any
  single ordering telescope to `v(all)`, the estimates always sum to the true total and converge
  quickly. The sampler is seeded with a fixed value so blame is reproducible across runs.

Blame is opt-in (`--blame`) because most of the time the exclusive "what do I save now" number
is what you want; the Shapley split is for when you want the accounting to balance.

---

## 10. The second goal: lowering your `go` version floor

Size isn't the only thing dependencies inflate; they also push up the minimum Go version you
must declare. Go requires your main module's `go` directive to be at least as high as every
module in your build. So your floor is set by the highest `go` directive among the dependencies
you don't control. (See `goversion.go`.)

bonsai computes this floor over exactly the modules currently in the build, and reports:

- Version: the dep-imposed floor (the highest `go` line among non-owned modules).
- Critical: the modules pinned exactly at that floor. These are the reason for it; prune all of
  them and the floor drops.
- NextVersion: where the floor would land if you pruned every Critical module.
- OwnedMax: the highest directive your own modules already declare, so you can see the headroom
  you can reclaim right now (just lower your `go` line) versus what pruning would buy you.

The nice part: lowering the floor is the same lever as pruning for size. Dropping a module
removes it from the build, which is exactly what can let the floor fall. So in the interactive
explorer, the floor recomputes live as you deselect modules: one action, two payoffs. The
`inspect` command reports this as a FloorDelta: prune this module, does the floor move?

---

## 11. The third signal: how hard is it to actually cut?

A dependency can be a huge size win and still not be worth removing if your code is wired deeply
into it. So bonsai measures coupling as a proxy for removal effort; lower numbers mean easier to
prune. (See `coupling.go`.) For each dependency it counts:

- how many distinct first-party packages import it,
- how many total import statements reference it,
- how many distinct symbols of it your code actually uses.

Symbol counting is a deliberately cheap syntactic approximation (matching `alias.Symbol`
selector expressions against each file's imports, no full type resolution), exact enough to rank
coupling depth, which is all it's for.

Closely related, and aimed at automation, is the import-site map: the concrete `file:line`
locations of every first-party import of a given module, the actual edit locations you'd change
to sever it. This is what powers the `inspect` command and the MCP `bonsai_locate_cuts` tool:
instead of "this dep is worth cutting," an agent gets "here are the four files and lines to
edit, and here's how much weight each entry package accounts for," so a partial rewrite can be
scoped to the parts that are actually worth it.

That per-entry-package weight comes straight back from the dominator tree of section 7: the
children of a target's gateway are exactly the packages of it that first-party code imports
directly, and each child's retained size is the weight reachable only through that one entry
package. So "stop importing this specific package" gets its own savings number, not just the
module as a whole.
