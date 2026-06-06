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

// blockerSets returns, for each freeable package, the set of droppable direct deps whose
// subtree reaches it — i.e. the deps that must all be dropped for the package to leave the
// binary. Ignored deps are treated as permanent: they are excluded as blockers, and any
// package reachable only through them is "kept regardless" and never appears here.
func (g *buildGraph) blockerSets(ignore ignoreMatcher) map[string][]string {
	base := g.reachable(nil)

	// droppable direct deps: every direct dependency the user hasn't pinned via ignore.
	droppable := make([]string, 0, len(g.directMods))
	cut := map[string]bool{}
	for m := range g.directMods {
		if ignore.match(m) {
			continue
		}
		droppable = append(droppable, m)
		cut[m] = true
	}
	sort.Strings(droppable)

	// packages still reachable when first-party imports none of the droppable deps are
	// "kept regardless" (reached via std, first-party, or an ignored dep) and can never be
	// pruned away.
	keptRegardless := g.reachable(cut)

	// blockers[pkg] = droppable deps whose subtree reaches pkg (built one dep at a time to
	// avoid holding many reachability sets at once).
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

// sharedModules reports dependencies pulled into the binary through more than one droppable
// direct dep — load-bearing weight that no single prune removes. SharedBy is the number of
// distinct direct deps whose subtrees reach the module. Modules reachable through a single
// dep are exclusive prune candidates (covered by the prune table) and excluded here.
func (g *buildGraph) sharedModules(selfSize map[string]uint64, ignore ignoreMatcher) []SharedModule {
	blockers := g.blockerSets(ignore)

	bytes := map[string]uint64{}
	deps := map[string]map[string]bool{}
	for pkg, bl := range blockers {
		mod := g.moduleOfPkg[pkg]
		if mod == "" {
			continue // standard library has no module to attribute shared weight to
		}
		bytes[mod] += selfSize[pkg]
		if deps[mod] == nil {
			deps[mod] = map[string]bool{}
		}
		for _, d := range bl {
			deps[mod][d] = true
		}
	}

	var out []SharedModule
	for mod, ds := range deps {
		if len(ds) < 2 {
			continue // single-dep modules are exclusive prune candidates, not shared weight
		}
		out = append(out, SharedModule{
			Module:   mod,
			Bytes:    bytes[mod],
			SharedBy: len(ds),
			Ignored:  ignore.match(mod),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Module < out[j].Module
	})
	return out
}

// pruneActions groups freeable packages by their blocker set — the set of direct deps
// that must all be dropped for the package to leave the binary — and reports the bytes
// freed per group. Single-dep groups are exclusive wins; multi-dep groups are co-prunes.
func (g *buildGraph) pruneActions(selfSize map[string]uint64, ignore ignoreMatcher) []PruneAction {
	blockers := g.blockerSets(ignore)

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
