package bonsai

import "sort"

// The pruning question — "how many bytes do I actually save if I stop importing module B
// everywhere I can?" — is the heap-profiler "retained size" question with multiple GC roots
// (see the package doc for the full GC-heap ↔ Go-build mapping). The bytes freed by pruning
// B are exactly the bytes B *dominates*: nodes every root→node path passes through B to
// reach. Shared weight (reachable another way) is dominated by the super-root, not by B, so
// it is correctly never credited to B alone — the same reason heap profilers (Chrome
// DevTools, Eclipse Memory Analyzer, dotMemory) hang objects reachable from multiple GC
// roots off the super-root rather than crediting one root.
//
// Roots here are the real entrypoints PLUS the always-present backbone (controlled code,
// locked deps, shared std): anything the backbone retains has zero prunable size by
// construction, exactly like an extra GC root keeping memory alive.
//
// One wrinkle that makes this richer than a plain heap dominator tree: a prune action is not
// deleting a node, it is cutting a *set* of edges — every import of B that originates in code
// we control. We model that as a single synthetic gateway node g_B that all those cuttable
// edges route through, so removing g_B == cutting them all. The dominator tree of this
// gateway-augmented graph then yields, in one pass, the exact exclusive savings of every
// prune target simultaneously (retained size of its gateway), replacing the per-module
// reachability sweeps. Correctness is pinned by a test: exclusive savings must equal an
// independent tree-shake oracle (dominator_test.go).
//
// Dominators are computed with Cooper, Harvey & Kennedy, "A Simple, Fast Dominance
// Algorithm" (2001) — an iterative data-flow formulation that beats Lengauer–Tarjan in
// practice below ~1000s of nodes (bonsai's scale) and is far simpler to implement correctly.
// "Retained size" terminology and the multi-root super-root trick come straight from the
// heap-profiler literature it mirrors.

// domModel is the gateway-augmented dominator tree over the build's reachable packages.
type domModel struct {
	pkgOf    []string       // node id -> import path ("" for the super-root and gateway nodes)
	size     []uint64       // node id -> self size (gateways and super-root are 0)
	idom     []int          // node id -> immediate dominator (idom[root] == root)
	children [][]int        // dominator-tree children
	retained []uint64       // node id -> retained size (own + all dominated)
	gateway  map[string]int // prune-target module -> its gateway node id
}

// buildDomModel constructs the gateway-augmented flow graph over base (the reachable
// package set), computes its dominator tree, and rolls up retained sizes. selfSize maps
// import paths to attributed bytes; c says which modules are controlled and which are
// prune targets.
func (g *buildGraph) buildDomModel(selfSize map[string]uint64, base map[string]bool, c *classification) *domModel { //nolint:funlen // builds the dominator-tree retained-size model in one cohesive pass
	const superRoot = 0

	m := &domModel{
		pkgOf:   []string{""}, // node 0 is the super-root
		size:    []uint64{0},
		gateway: map[string]int{},
	}
	nodeID := map[string]int{} // import path -> node id

	idOf := func(ip string) int {
		if id, ok := nodeID[ip]; ok {
			return id
		}
		id := len(m.pkgOf)
		nodeID[ip] = id
		m.pkgOf = append(m.pkgOf, ip)
		m.size = append(m.size, selfSize[ip])
		return id
	}
	gatewayOf := func(target string) int {
		if id, ok := m.gateway[target]; ok {
			return id
		}
		id := len(m.pkgOf)
		m.gateway[target] = id
		m.pkgOf = append(m.pkgOf, "")
		m.size = append(m.size, 0)
		return id
	}

	// assign ids to every reachable package up front so node ids are stable and dense.
	for ip := range base {
		idOf(ip)
	}

	// succ accumulates the flow graph; sets dedup parallel edges (harmless but wasteful).
	succ := map[int]map[int]bool{}
	addEdge := func(from, to int) {
		s := succ[from]
		if s == nil {
			s = map[int]bool{}
			succ[from] = s
		}
		s[to] = true
	}

	// the super-root feeds the real entrypoints.
	for _, root := range g.rootPackages {
		if base[root] {
			addEdge(superRoot, idOf(root))
		}
	}

	// rewrite every reachable import edge: cuttable edges into a prune target are rerouted
	// through that target's gateway; everything else stays a direct edge.
	for ip := range base {
		pkg := g.packages[ip]
		if pkg == nil {
			continue
		}
		srcMod := g.moduleOfPkg[ip]
		srcControlled := g.isControlled(srcMod)
		u := idOf(ip)
		for _, imp := range pkg.Imports {
			if !base[imp] {
				continue
			}
			dstMod := g.moduleOfPkg[imp]
			if srcControlled && c.isTarget(dstMod) {
				gw := gatewayOf(dstMod)
				addEdge(u, gw)
				addEdge(gw, idOf(imp))
				continue
			}
			addEdge(u, idOf(imp))
		}
	}

	n := len(m.pkgOf)
	adj := make([][]int, n)
	for from, tos := range succ {
		lst := make([]int, 0, len(tos))
		for to := range tos {
			lst = append(lst, to)
		}
		sort.Ints(lst) // determinism
		adj[from] = lst
	}

	m.idom = dominators(n, superRoot, adj)
	m.buildChildrenAndRetained(superRoot)
	return m
}

// buildChildrenAndRetained derives dominator-tree children from idom and computes each
// node's retained size (own size plus everything it dominates) via a postorder roll-up.
func (m *domModel) buildChildrenAndRetained(root int) {
	n := len(m.idom)
	m.children = make([][]int, n)
	for v := range n {
		if v == root || m.idom[v] < 0 {
			continue
		}
		m.children[m.idom[v]] = append(m.children[m.idom[v]], v)
	}

	m.retained = make([]uint64, n)
	// iterative postorder so deep graphs don't blow the stack.
	type frame struct {
		node    int
		childIx int
	}
	stack := []frame{{root, 0}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.childIx < len(m.children[f.node]) {
			child := m.children[f.node][f.childIx]
			f.childIx++
			stack = append(stack, frame{child, 0})
			continue
		}
		// children done: fold this node's retained size into its parent's.
		m.retained[f.node] += m.size[f.node]
		if len(stack) >= 2 {
			parent := stack[len(stack)-2].node
			m.retained[parent] += m.retained[f.node]
		}
		stack = stack[:len(stack)-1]
	}
}

// exclusivePkgs returns the import paths of the packages dominated by target's gateway —
// the packages that actually leave the build when the target is pruned. nil if the target
// has no gateway (nothing controlled reaches it in the linked graph).
func (m *domModel) exclusivePkgs(target string) []string {
	gw, ok := m.gateway[target]
	if !ok {
		return nil
	}
	var out []string
	stack := []int{gw}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if ip := m.pkgOf[v]; ip != "" {
			out = append(out, ip)
		}
		stack = append(stack, m.children[v]...)
	}
	return out
}

// exclusiveBytes is the retained size of the target's gateway: the bytes freed by pruning
// this target alone.
func (m *domModel) exclusiveBytes(target string) uint64 {
	if gw, ok := m.gateway[target]; ok {
		return m.retained[gw]
	}
	return 0
}

// dominators computes the immediate dominator of every node reachable from entry using the
// Cooper–Harvey–Kennedy iterative algorithm ("A Simple, Fast Dominance Algorithm", 2001).
// For graphs of a few thousand nodes it outperforms Lengauer–Tarjan and is far simpler.
// idom[entry] == entry; unreachable nodes get -1.
func dominators(n, entry int, succ [][]int) []int { //nolint:funlen,gocognit // Cooper-Harvey-Kennedy iterative dominator algorithm kept intact as a unit
	// postorder traversal and numbering (entry finishes last → highest number).
	postorder := make([]int, 0, n)
	postNum := make([]int, n)
	for i := range postNum {
		postNum[i] = -1
	}
	type frame struct {
		node    int
		childIx int
	}
	visited := make([]bool, n)
	stack := []frame{{entry, 0}}
	visited[entry] = true
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.childIx < len(succ[f.node]) {
			w := succ[f.node][f.childIx]
			f.childIx++
			if !visited[w] {
				visited[w] = true
				stack = append(stack, frame{w, 0})
			}
			continue
		}
		postNum[f.node] = len(postorder)
		postorder = append(postorder, f.node)
		stack = stack[:len(stack)-1]
	}

	idom := make([]int, n)
	for i := range idom {
		idom[i] = -1
	}
	idom[entry] = entry

	preds := make([][]int, n)
	for u := range n {
		for _, v := range succ[u] {
			preds[v] = append(preds[v], u)
		}
	}

	intersect := func(b1, b2 int) int {
		for b1 != b2 {
			for postNum[b1] < postNum[b2] {
				b1 = idom[b1]
			}
			for postNum[b2] < postNum[b1] {
				b2 = idom[b2]
			}
		}
		return b1
	}

	// iterate in reverse postorder until the idom assignment stops changing.
	for changed := true; changed; {
		changed = false
		for i := len(postorder) - 1; i >= 0; i-- {
			b := postorder[i]
			if b == entry {
				continue
			}
			newIdom := -1
			for _, p := range preds[b] {
				if idom[p] == -1 {
					continue // predecessor not processed yet
				}
				if newIdom == -1 {
					newIdom = p
				} else {
					newIdom = intersect(p, newIdom)
				}
			}
			if newIdom != -1 && idom[b] != newIdom {
				idom[b] = newIdom
				changed = true
			}
		}
	}
	return idom
}
