package bonsai

import "sort"

// PruneResult describes what disappears from the build if a single direct
// dependency module is no longer imported by first-party (main module) code.
type PruneResult struct {
	Module        string   `json:"module"`
	FreedBytes    uint64   `json:"freedBytes"`    // total size attributed to freed packages
	FreedPackages int      `json:"freedPackages"` // count of packages that become unreachable
	FreedModules  []string `json:"freedModules"`  // modules fully removed (all their pkgs freed)
}

// reachable computes the set of packages reachable from the roots, where edges are
// package imports, severing every edge from a main-module package into any module in
// cut (simulating "first-party code stops importing these dependencies").
func (g *buildGraph) reachable(cut map[string]bool) map[string]bool {
	seen := make(map[string]bool, len(g.packages))
	stack := append([]string(nil), g.rootPackages...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		pkg := g.packages[cur]
		if pkg == nil {
			continue
		}
		curIsFirstParty := g.moduleOfPkg[cur] == g.mainModule
		for _, imp := range pkg.Imports {
			if curIsFirstParty && cut[g.moduleOfPkg[imp]] {
				continue
			}
			if !seen[imp] {
				stack = append(stack, imp)
			}
		}
	}
	return seen
}

// treeShake computes, for the given module, the packages and modules that would be
// removed from the binary if first-party code stopped importing it, and the bytes saved.
func (g *buildGraph) treeShake(module string, selfSize map[string]uint64, baseReachable map[string]bool) PruneResult {
	after := g.reachable(map[string]bool{module: true})

	res := PruneResult{Module: module}
	freedByModule := map[string]int{}
	totalByModule := map[string]int{}
	for ip := range g.packages {
		if m := g.moduleOfPkg[ip]; m != "" {
			totalByModule[m]++
		}
	}

	for ip := range baseReachable {
		if after[ip] {
			continue
		}
		res.FreedPackages++
		res.FreedBytes += selfSize[ip]
		if m := g.moduleOfPkg[ip]; m != "" {
			freedByModule[m]++
		}
	}

	for m, freed := range freedByModule {
		if freed == totalByModule[m] {
			res.FreedModules = append(res.FreedModules, m)
		}
	}
	sort.Strings(res.FreedModules)
	return res
}
