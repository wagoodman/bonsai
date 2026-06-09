package bonsai

// PruneResult describes what disappears from the build if a prune target module is no
// longer imported by controlled (1st-class) code. The three byte figures tell the realistic
// story: FreedBytes is what you actually bank by pruning this one target (its dominated,
// exclusive weight); PotentialBytes is the freeable weight in its subtree (what could be
// clawed back if this target and everything sharing its subtree were pruned together); and
// SharedBytes is the difference — freeable weight that stays because other targets reach it
// too (enumerated in SharedWith).
type PruneResult struct {
	Module         string         `json:"module"`
	FreedBytes     uint64         `json:"freedBytes"`     // exclusive: freed by pruning THIS target alone
	FreedPackages  int            `json:"freedPackages"`  // count of packages that become unreachable
	FreedModules   []string       `json:"freedModules"`   // modules fully removed (all their pkgs freed)
	PotentialBytes uint64         `json:"potentialBytes"` // freeable weight in the subtree (this + all co-holders pruned)
	SharedBytes    uint64         `json:"sharedBytes"`    // PotentialBytes - FreedBytes: held alive by other targets
	SharedWith     []SharedHolder `json:"sharedWith,omitempty"`
}

// SharedHolder is a slice of a target's subtree that pruning the target would NOT free,
// because other prune targets also reach it. AlsoVia names those other targets.
type SharedHolder struct {
	Module  string   `json:"module"`
	Bytes   uint64   `json:"bytes"`
	AlsoVia []string `json:"alsoVia"`
}

// reachable computes the set of packages reachable from the roots, where edges are
// package imports, severing every edge from a controlled (1st-class) package into any
// module in cut (simulating "the code I own stops importing these dependencies"). With
// only the main module controlled — the default before classify() widens it — this is the
// original "first-party code stops importing these" model.
//
// This is the tracing-GC mark phase: the returned set is the build's "live" packages for a
// given cut, and everything in the base build but absent here is what that cut frees. The
// dominator and what-if passes are just efficient ways to ask this for many cuts at once.
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
		curControlled := g.isControlled(g.moduleOfPkg[cur])
		for _, imp := range pkg.Imports {
			if curControlled && cut[g.moduleOfPkg[imp]] {
				continue
			}
			if !seen[imp] {
				stack = append(stack, imp)
			}
		}
	}
	return seen
}
