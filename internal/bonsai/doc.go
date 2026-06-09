/*
Package bonsai attributes the size of a compiled Go binary to its module dependencies and
estimates the realistic cost/benefit of pruning each one.

It joins three signals that no single existing tool combines:
  - size:     per-module code bytes in the linked binary
  - prune:    bytes (and transitive modules) freed if a dependency were dropped
  - coupling: how deeply first-party code is wired into each dep (removal effort)

By default it builds the target from source (capturing the linker's -dumpdep reachability
graph for exact, post-dead-code-elimination analysis) and analyzes the artifact it produced;
a prebuilt binary can be analyzed instead via Config.Binary. Size attribution parses the
binary's symbol table and gopclntab via debug/gosym (the latter works even on stripped
binaries), and the graph/coupling analyses use `go list` and a go/parser AST scan.

# Methodology: prune analysis is the garbage-collector retained-size problem

The central question — "how many bytes do I actually save if I stop importing module B
everywhere I can?" — decomposes into three sub-problems the literature treats separately:

 1. reachability — what is actually linked (so we only consider genuinely-removable weight).
    bonsai gets this for free from the linker's -dumpdep reference graph (dumpdep.go), which
    is ground truth post-DCE: strictly better than the static call graphs academic tools
    build (DepClean / Soto-Valero et al., "A Comprehensive Study of Bloated Dependencies in
    the Maven Ecosystem", EMSE 2021; Google Capslock for Go). The reachability "mark" is the
    same one a tracing GC runs from its roots (treeshake.go: reachable).

 2. attribution — crediting the size of a *shared* transitive dep among the deps that pull it
    in. This is the key realization: it is identical to what heap profilers (Chrome DevTools,
    Eclipse MAT, dotMemory) call retained size. Map the domains:

    GC heap                         ->  Go build
    object                          ->  package
    GC roots                        ->  entrypoints + locked/uncontrolled "backbone"
    edge (object references object) ->  import edge
    "bytes freed if X is collected" ->  "bytes freed if dependency X is pruned"
    retained size of X              ->  exclusive prune savings of X

    Heap profilers compute retained size with a dominator tree (node d dominates v if every
    root->v path goes through d; retained(d) = its own size + everything it dominates). A dep
    shared by two consumers is dominated by the super-root, not by either consumer, so its
    bytes are correctly credited to neither alone — exactly the realism we need. bonsai builds
    this dominator tree (dominator.go) and reads exclusive savings off it in one pass, the
    same way a heap profiler reports "what frees if this object becomes unreachable."

 3. counterfactual — "if I cut this set of edges, what becomes unreachable from the roots?"
    This is decremental reachability; for many candidate cuts and ordered plans it is a
    submodular maximization, so a greedy order carries the standard (1-1/e) guarantee
    (reachindex.go). Fair splitting of shared cost across consumers is the Shapley value from
    cooperative game theory (shapley.go).

What bonsai generalizes beyond a heap profiler: a prune action is not deleting one node, it
is cutting a *set* of edges — every import of B that originates in code the user controls.
The "controlled" set (1st-class modules; see classify.go) is the analog of "code I can edit",
and the model reduces to the original main-module-only behavior when only the main module is
controlled. The per-target gateway trick (dominator.go) turns each edge-set cut back into a
single-node removal so the dominator tree handles it natively.

This is debloating in the tree-shaking sense (additively keep what's reachable from roots),
not subtractive DCE — see Harris, "Tree-shaking versus dead code elimination". The reachable
set IS the GC live set; the prunable set is everything reachable only through cuttable edges.
*/
package bonsai
