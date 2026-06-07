package bonsai

import "sort"

// reachIndex is an int-indexed view of the reachable build graph built once and queried
// many times to answer counterfactuals: "if I prune this set of targets, how many bytes
// become unreachable?" — i.e. decremental (multi-source) reachability under edge deletion.
// Each cuttable edge (controlled code → a prune target) is tagged with the target it depends
// on, so a query is a single graph sweep that skips the edges the pruned targets would
// remove. This backs the greedy prune plan and the Shapley blame pass, where the same v(S)
// is evaluated thousands of times.
//
// Data-structure choice: the reachability-indexing literature offers fancier structures
// (2-hop labels, GRAIL, interval/tree-cover labeling) for huge graphs, but at bonsai's scale
// (hundreds–low-thousands of packages) a plain int-indexed adjacency with a generation-
// stamped visited array wins on simplicity and supports exactly the set-algebra these
// queries need. v(S) is the GC "freed" set: total live bytes minus what stays reachable.
type reachIndex struct {
	adj   [][]reachEdge // node id -> out edges (reachable packages only)
	roots []int         // entrypoint node ids
	size  []uint64      // node id -> attributed bytes
	mod   []string      // node id -> owning module path ("" for std)
	pkg   []string      // node id -> package import path
	total uint64        // total reachable bytes (== freed when everything is cut)

	targets  []string       // target id -> module path
	targetID map[string]int // module path -> target id

	// importers[unit] = how many distinct modules directly import that unit (module or std
	// package) across the whole reachable build — its fan-in.
	importers map[freedKey]int

	// reusable DFS scratch: a generation stamp avoids reallocating/clearing the visited
	// slice on every query.
	seen []uint32
	gen  uint32
}

// reachEdge is an out-edge. cutTID >= 0 means the edge is cuttable and disappears when
// target cutTID is pruned; -1 means it is structural and always present.
type reachEdge struct {
	to     int
	cutTID int
}

// newReachIndex compiles base (the reachable package set) into the int-indexed query
// structure. Edge cuttability matches reachable(): an edge is cuttable iff it leaves
// controlled code and lands on a prune target.
func (g *buildGraph) newReachIndex(selfSize map[string]uint64, base map[string]bool, c *classification) *reachIndex {
	ri := &reachIndex{targetID: map[string]int{}}
	tid := func(mod string) int {
		if id, ok := ri.targetID[mod]; ok {
			return id
		}
		id := len(ri.targets)
		ri.targetID[mod] = id
		ri.targets = append(ri.targets, mod)
		return id
	}
	for _, t := range c.targets() {
		tid(t) // stable, sorted target ids
	}

	nodeID := map[string]int{}
	id := func(ip string) int {
		if v, ok := nodeID[ip]; ok {
			return v
		}
		v := len(ri.size)
		nodeID[ip] = v
		ri.size = append(ri.size, selfSize[ip])
		ri.mod = append(ri.mod, g.moduleOfPkg[ip])
		ri.pkg = append(ri.pkg, ip)
		ri.total += selfSize[ip]
		return v
	}
	for ip := range base {
		id(ip)
	}
	ri.adj = make([][]reachEdge, len(ri.size))

	for ip := range base {
		pkg := g.packages[ip]
		if pkg == nil {
			continue
		}
		srcControlled := g.isControlled(g.moduleOfPkg[ip])
		u := id(ip)
		for _, imp := range pkg.Imports {
			if !base[imp] {
				continue
			}
			cut := -1
			if dst := g.moduleOfPkg[imp]; srcControlled && c.isTarget(dst) {
				cut = ri.targetID[dst]
			}
			ri.adj[u] = append(ri.adj[u], reachEdge{to: id(imp), cutTID: cut})
		}
	}
	for _, root := range g.rootPackages {
		if base[root] {
			ri.roots = append(ri.roots, id(root))
		}
	}
	ri.seen = make([]uint32, len(ri.size))
	ri.computeImporters()
	return ri
}

// computeImporters counts, for every module and stdlib package in the reachable build, how
// many distinct OTHER modules directly import it — its fan-in. All standard-library importers
// collapse to one bucket, so std-internal edges never inflate the count and a stdlib package's
// number answers "how many of my modules pull this in". This is the "how shared is it"
// annotation each prune-plan row hangs off.
func (ri *reachIndex) computeImporters() {
	keyOf := func(n int) freedKey {
		if ri.mod[n] != "" {
			return freedKey{name: ri.mod[n]}
		}
		return freedKey{name: ri.pkg[n], std: true}
	}
	sets := map[freedKey]map[string]bool{}
	for u := range ri.adj {
		importer := ri.mod[u] // "" collapses every std importer into a single bucket
		for _, e := range ri.adj[u] {
			k := keyOf(e.to)
			owner := "" // a std unit's owning label is the collapsed std bucket
			if !k.std {
				owner = k.name
			}
			if importer == owner {
				continue // intra-module or std-internal: not an external importer
			}
			if sets[k] == nil {
				sets[k] = map[string]bool{}
			}
			sets[k][importer] = true
		}
	}
	ri.importers = make(map[freedKey]int, len(sets))
	for k, s := range sets {
		ri.importers[k] = len(s)
	}
}

// sweep marks every node reachable from the roots under cut (ri.seen[n]==ri.gen) and returns
// the reached bytes. The generation stamp lets callers test reachability afterward without a
// separate allocation.
func (ri *reachIndex) sweep(cut []bool) uint64 {
	ri.gen++
	var reached uint64
	stack := append([]int(nil), ri.roots...)
	for _, r := range ri.roots {
		ri.seen[r] = ri.gen
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		reached += ri.size[u]
		for _, e := range ri.adj[u] {
			if e.cutTID >= 0 && cut[e.cutTID] {
				continue // this import is severed by a pruned target
			}
			if ri.seen[e.to] != ri.gen {
				ri.seen[e.to] = ri.gen
				stack = append(stack, e.to)
			}
		}
	}
	return reached
}

// freedBytes returns the bytes that become unreachable when the targets flagged in cut are
// pruned — v(S) for the prune game. cut is indexed by target id.
func (ri *reachIndex) freedBytes(cut []bool) uint64 {
	return ri.total - ri.sweep(cut)
}

// freedKey identifies a bucket in a freed-weight breakdown: a third-party module, or a
// standard-library package (std has no module, so the package is the meaningful unit — it is
// what reveals "x/tools dragged in go/types", not just "some stdlib").
type freedKey struct {
	name string
	std  bool
}

// freedBreakdown returns the freed (unreachable-under-cut) bytes bucketed by third-party
// module and, for the standard library, by package. This is the "where does the space come
// from" view behind each prune-plan step.
func (ri *reachIndex) freedBreakdown(cut []bool) map[freedKey]uint64 {
	ri.sweep(cut)
	out := map[freedKey]uint64{}
	for n := range ri.size {
		if ri.seen[n] == ri.gen {
			continue // still reachable, not freed
		}
		if mod := ri.mod[n]; mod != "" {
			out[freedKey{name: mod}] += ri.size[n]
		} else {
			out[freedKey{name: ri.pkg[n], std: true}] += ri.size[n]
		}
	}
	return out
}

// PrunePlanStep is one move in an ordered prune plan: the marginal bytes freed by pruning
// Module after every earlier step has already been applied, and the running total. Because
// prunes interact (shared deps only free once both holders are gone), the realistic order
// matters — this greedy plan always takes the biggest next win.
//
// The breakdown answers "where does the freed space come from?": OwnBytes is the pruned
// module's own code, and Freed lists the dependency modules it drags out with it (newly
// orphaned at this step), largest first. OwnBytes + sum(Freed) == Marginal.
type PrunePlanStep struct {
	Module     string        `json:"module"`
	Marginal   uint64        `json:"marginal"`            // additional bytes freed at this step
	Cumulative uint64        `json:"cumulative"`          // total bytes freed through this step
	OwnBytes   uint64        `json:"ownBytes"`            // freed bytes that are the pruned module's own code
	Importers  int           `json:"importers,omitempty"` // distinct modules that directly import the pruned module
	Freed      []FreedModule `json:"freed,omitempty"`     // dependency modules orphaned at this step, largest first
}

// maxPlanSteps bounds the greedy plan so its cost (O(steps × targets) sweeps) stays small
// and the output stays readable; later steps yield diminishing returns anyway.
const maxPlanSteps = 25

// greedyPlan produces an ordered prune plan by repeatedly choosing the target with the
// largest marginal saving given everything chosen so far. Retained size is submodular in the
// cut set, so this greedy order has the standard (1−1/e) quality guarantee and surfaces the
// realistic "prune A for 9 MB, then B frees another 4 MB net" sequence.
func (g *buildGraph) greedyPlan(selfSize map[string]uint64, base map[string]bool, c *classification) []PrunePlanStep {
	ri := g.newReachIndex(selfSize, base, c)
	n := len(ri.targets)
	if n == 0 {
		return nil
	}

	chosen := make([]bool, n)
	done := make([]bool, n)
	var steps []PrunePlanStep
	var prev uint64
	prevFreed := map[freedKey]uint64{} // cumulative freed breakdown before the current step
	limit := min(maxPlanSteps, n)
	for len(steps) < limit {
		best, bestFreed := -1, prev
		for t := range n {
			if done[t] {
				continue
			}
			chosen[t] = true
			f := ri.freedBytes(chosen)
			chosen[t] = false
			if f > bestFreed {
				best, bestFreed = t, f
			}
		}
		if best < 0 {
			break // no remaining target frees anything new
		}
		chosen[best] = true
		done[best] = true

		// attribute this step's marginal by diffing the cumulative freed breakdown before and
		// after taking best: the target's own code vs the modules (and stdlib packages) it
		// drags out of the build with it.
		targetMod := ri.targets[best]
		afterFreed := ri.freedBreakdown(chosen)
		step := PrunePlanStep{
			Module:     targetMod,
			Marginal:   bestFreed - prev,
			Cumulative: bestFreed,
			Importers:  ri.importers[freedKey{name: targetMod}],
		}
		for k, after := range afterFreed {
			delta := after - prevFreed[k] // monotonic: more cuts never un-frees
			if delta == 0 {
				continue
			}
			if !k.std && k.name == targetMod {
				step.OwnBytes = delta
				continue
			}
			step.Freed = append(step.Freed, FreedModule{Module: k.name, Bytes: delta, Std: k.std, Importers: ri.importers[k]})
		}
		sort.Slice(step.Freed, func(i, j int) bool {
			if step.Freed[i].Bytes != step.Freed[j].Bytes {
				return step.Freed[i].Bytes > step.Freed[j].Bytes
			}
			return step.Freed[i].Module < step.Freed[j].Module
		})
		steps = append(steps, step)
		prev = bestFreed
		prevFreed = afterFreed
	}
	return steps
}

// allTargetIDs returns a fresh slice of every target id, used by callers that permute or
// enumerate the full target set.
func (ri *reachIndex) allTargetIDs() []int {
	ids := make([]int, len(ri.targets))
	for i := range ids {
		ids[i] = i
	}
	return ids
}

// sortedBlame turns a per-target id blame slice into sorted, module-labelled output.
func (ri *reachIndex) sortedBlame(blame []uint64, exact bool) []ModuleBlame {
	out := make([]ModuleBlame, 0, len(blame))
	for t, b := range blame {
		out = append(out, ModuleBlame{Module: ri.targets[t], Blame: b, Exact: exact})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Blame != out[j].Blame {
			return out[i].Blame > out[j].Blame
		}
		return out[i].Module < out[j].Module
	})
	return out
}
