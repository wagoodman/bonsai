package bonsai

import "sort"

// FreedModule is a unit of freed weight. Module is a module path, or — when Std is true — a
// standard-library package import path (stdlib has no module, so the package is the natural
// unit for "which stdlib did this dependency switch on"). Importers is how many distinct
// modules across the whole build directly import this unit — its fan-in, a quick read on how
// shared (load-bearing) it is without cross-referencing the shared table.
type FreedModule struct {
	Module    string      `json:"module"`
	Bytes     uint64      `json:"bytes"`
	Std       bool        `json:"std,omitempty"`
	Importers int         `json:"importers,omitempty"`
	CoPrune   []string    `json:"coPrune,omitempty"` // other prune targets that must also be dropped to free this
	Why       *ImportNode `json:"why,omitempty"`     // who imports this, traced back to 1st-class code
}

// reachableFromModule returns every package reachable by following imports from the
// packages of module m — i.e. everything in m's dependency subtree.
func (g *buildGraph) reachableFromModule(m string) map[string]bool {
	seen := map[string]bool{}
	var stack []string
	for ip := range g.packages {
		if g.moduleOfPkg[ip] == m {
			stack = append(stack, ip)
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if p := g.packages[cur]; p != nil {
			for _, imp := range p.Imports {
				if !seen[imp] {
					stack = append(stack, imp)
				}
			}
		}
	}
	return seen
}

// blockerSets returns, for each freeable package, the set of prune targets whose subtree
// reaches it — i.e. the targets that must all be dropped for the package to leave the
// binary. Locked deps are treated as permanent: they are excluded as blockers, and any
// package reachable only through them (or through std / controlled code) is "kept
// regardless" and never appears here.
func (g *buildGraph) blockerSets(c *classification) map[string][]string {
	base := g.reachable(nil)

	// every prune target is droppable; cutting all of them at once leaves only the
	// kept-regardless backbone.
	droppable := c.targets()
	cut := map[string]bool{}
	for _, m := range droppable {
		cut[m] = true
	}

	// packages still reachable when controlled code imports none of the droppable targets
	// are "kept regardless" (reached via std, controlled code, or a locked dep) and can
	// never be pruned away.
	keptRegardless := g.reachable(cut)

	// blockers[pkg] = droppable targets whose subtree reaches pkg (built one target at a
	// time to avoid holding many reachability sets at once).
	blockers := map[string][]string{}
	for _, m := range droppable {
		for pkg := range g.reachableFromModule(m) {
			if base[pkg] && !keptRegardless[pkg] {
				blockers[pkg] = append(blockers[pkg], m)
			}
		}
	}
	return blockers
}

// pruneResults builds, for every prune target, the realistic savings breakdown: exclusive
// bytes (from the dominator tree — what pruning this target alone frees), the full subtree
// potential, and the shared remainder enumerated by holder. blockers (from blockerSets)
// names the other targets that keep each shared package alive.
//
//nolint:funlen,gocognit // per-target savings breakdown built in one pass over the dominator tree
func (g *buildGraph) pruneResults(selfSize map[string]uint64, base map[string]bool, c *classification,
	dom *domModel, blockers map[string][]string) map[string]*PruneResult {
	// total reachable package count per module, to decide which modules are fully freed.
	totalByModule := map[string]int{}
	for ip := range base {
		if mod := g.moduleOfPkg[ip]; mod != "" {
			totalByModule[mod]++
		}
	}

	// freeable packages are exactly the keys of blockers: reachable, but only via prune
	// targets (not via the always-present backbone of controlled code, locked deps, and the
	// shared standard library). Potential is measured against this set so it reflects weight
	// that can actually be clawed back, not the entire transitive closure into std.
	freeable := make(map[string]bool, len(blockers))
	for pkg := range blockers {
		freeable[pkg] = true
	}

	out := map[string]*PruneResult{}
	for _, target := range c.targets() {
		res := &PruneResult{Module: target, FreedBytes: dom.exclusiveBytes(target)}

		// exclusive set: packages that actually leave when this target is pruned.
		exclusive := map[string]bool{}
		freedByModule := map[string]int{}
		for _, ip := range dom.exclusivePkgs(target) {
			exclusive[ip] = true
			res.FreedPackages++
			if mod := g.moduleOfPkg[ip]; mod != "" {
				freedByModule[mod]++
			}
		}
		for mod, freed := range freedByModule {
			if freed == totalByModule[mod] {
				res.FreedModules = append(res.FreedModules, mod)
			}
		}
		sort.Strings(res.FreedModules)

		// potential: the freeable weight in this target's subtree — what could be clawed back
		// if this target and everything sharing its subtree were all pruned together.
		sharedBytes := map[string]uint64{}
		sharedVia := map[string]map[string]bool{}
		for ip := range g.reachableFromModule(target) {
			if !freeable[ip] {
				continue
			}
			res.PotentialBytes += selfSize[ip]
			if exclusive[ip] {
				continue
			}
			// shared: stays because another target reaches it too.
			mod := g.moduleOfPkg[ip]
			if mod == "" {
				mod = modStd // standard-library weight has no module; bucket it together
			}
			sharedBytes[mod] += selfSize[ip]
			if sharedVia[mod] == nil {
				sharedVia[mod] = map[string]bool{}
			}
			for _, b := range blockers[ip] {
				if b != target {
					sharedVia[mod][b] = true
				}
			}
		}
		res.SharedBytes = res.PotentialBytes - res.FreedBytes

		for mod, b := range sharedBytes {
			if b == 0 {
				continue
			}
			via := make([]string, 0, len(sharedVia[mod]))
			for t := range sharedVia[mod] {
				via = append(via, t)
			}
			sort.Strings(via)
			res.SharedWith = append(res.SharedWith, SharedHolder{Module: mod, Bytes: b, AlsoVia: via})
		}
		sort.Slice(res.SharedWith, func(i, j int) bool {
			if res.SharedWith[i].Bytes != res.SharedWith[j].Bytes {
				return res.SharedWith[i].Bytes > res.SharedWith[j].Bytes
			}
			return res.SharedWith[i].Module < res.SharedWith[j].Module
		})
		out[target] = res
	}
	return out
}
