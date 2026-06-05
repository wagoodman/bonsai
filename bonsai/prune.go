package bonsai

import (
	"sort"
	"strings"
)

// PruneAction is one actionable group: dropping first-party imports of every direct
// dependency in Deps frees the listed modules (and StdBytes of standard-library code).
// A single-element Deps is a plain "drop this dep" win; a multi-element Deps is a
// co-prune — those modules stay until ALL the listed deps are dropped, because each one
// independently pulls them in.
type PruneAction struct {
	Deps     []string      `json:"deps"`
	Bytes    uint64        `json:"bytes"` // total freed (modules + std)
	Modules  []FreedModule `json:"modules"`
	StdBytes uint64        `json:"stdBytes"`
}

type FreedModule struct {
	Module string `json:"module"`
	Bytes  uint64 `json:"bytes"`
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

// pruneActions groups freeable packages by their blocker set — the set of direct deps
// that must all be dropped for the package to leave the binary — and reports the bytes
// freed per group. Single-dep groups are exclusive wins; multi-dep groups are co-prunes.
func (g *buildGraph) pruneActions(selfSize map[string]uint64) []PruneAction {
	base := g.reachable(nil)

	// packages still reachable when first-party imports NO direct dep are "kept
	// regardless" (used directly by first-party or std) and can never be pruned away.
	allDirect := map[string]bool{}
	for m := range g.directMods {
		allDirect[m] = true
	}
	keptRegardless := g.reachable(allDirect)

	// TODO: support a configurable ignore list of dependencies we will never drop (e.g.
	// core deps like stereoscope/containerd), so we don't bother computing or listing
	// "what if we dropped X" actions for them. Accept module paths and globs (maybe
	// package paths too) — anything matched is excluded from `directs` here, which also
	// removes it as a blocker so co-prune groups collapse to only the droppable deps.
	directs := make([]string, 0, len(g.directMods))
	for m := range g.directMods {
		directs = append(directs, m)
	}
	sort.Strings(directs)

	// blockers[pkg] = direct deps whose subtree reaches pkg (built one dep at a time to
	// avoid holding many reachability sets at once).
	blockers := map[string][]string{}
	for _, m := range directs {
		for pkg := range g.reachableFromModule(m) {
			if base[pkg] && !keptRegardless[pkg] {
				blockers[pkg] = append(blockers[pkg], m)
			}
		}
	}

	type group struct {
		deps     []string
		modBytes map[string]uint64
		std      uint64
		total    uint64
	}
	groups := map[string]*group{}
	for pkg, deps := range blockers {
		sort.Strings(deps)
		key := strings.Join(deps, "\x00")
		gp := groups[key]
		if gp == nil {
			gp = &group{deps: deps, modBytes: map[string]uint64{}}
			groups[key] = gp
		}
		sz := selfSize[pkg]
		gp.total += sz
		if mod := g.moduleOfPkg[pkg]; mod != "" {
			gp.modBytes[mod] += sz
		} else {
			gp.std += sz
		}
	}

	actions := make([]PruneAction, 0, len(groups))
	for _, gp := range groups {
		a := PruneAction{Deps: gp.deps, Bytes: gp.total, StdBytes: gp.std}
		for mod, b := range gp.modBytes {
			a.Modules = append(a.Modules, FreedModule{Module: mod, Bytes: b})
		}
		sort.Slice(a.Modules, func(i, j int) bool { return a.Modules[i].Bytes > a.Modules[j].Bytes })
		actions = append(actions, a)
	}
	// fewest deps first (cheapest actions), then biggest savings.
	sort.Slice(actions, func(i, j int) bool {
		if len(actions[i].Deps) != len(actions[j].Deps) {
			return len(actions[i].Deps) < len(actions[j].Deps)
		}
		return actions[i].Bytes > actions[j].Bytes
	})
	return actions
}
