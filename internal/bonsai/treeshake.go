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

	// prize is the other axis: the full-graph retained size — bytes at stake if this module
	// vanished, computed across the controlled boundary, so weight pinned by uncontrolled deps
	// (which FreedBytes hides at zero) still shows. PrizeBytes >= FreedBytes always. See the
	// prize/effort design in specs/mcp-test/design-latent-wins.md.
	PrizeBytes          uint64         `json:"prizeBytes"`
	PinnedBy            []string       `json:"pinnedBy,omitempty"`            // locked, uncontrolled deps importing this that hold the prize against a controlled cut
	PrizeByEntryPackage []EntryPackage `json:"prizeByEntryPackage,omitempty"` // where the prize concentrates, so the ceiling becomes an achievable slice
	Effort              string         `json:"effort"`                        // how to realize the prize: quickWin | coordinated | pinnedByDep | core
}

// effort import-site cutoff for "core": so wired into first-party code that removal is a
// project, not a cut.
// ponytail: heuristic threshold; tune if it mislabels. The prize/EXCL bytes and the import-site
// count in the row carry the real nuance, the label is deliberately coarse.
const coreEffortSites = 20

// pruneEffort classifies how a candidate's prize would be realized, given its first-party
// coupling. pinnedByDep: an uncontrolled locked dep holds the prize (PinnedBy) — replace, patch,
// or upstream it. coordinated: frees nothing alone but its subtree is freeable — co-prune the
// other targets. core: too many import sites to be a clean cut. quickWin: a controlled cut banks
// the prize.
func pruneEffort(p *PruneResult, importSites int) string {
	switch {
	case len(p.PinnedBy) > 0 && p.PrizeBytes > p.FreedBytes:
		return "pinnedByDep"
	case p.FreedBytes == 0 && p.PotentialBytes > 0:
		return "coordinated"
	case p.FreedBytes == 0 && p.PrizeBytes > 0:
		// kept in the build with latent weight but no named locked importer (a transitive pin):
		// still not a clean cut, so never "quickWin" — the blocker just is not named.
		return "pinnedByDep"
	case importSites >= coreEffortSites:
		return "core"
	default:
		return "quickWin"
	}
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
