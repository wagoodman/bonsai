package bonsai

import (
	"fmt"
	"sort"
)

// EntryPackage is one package of the inspected module that first-party code imports directly,
// with the dominator-retained bytes attributable to it and which first-party packages import
// it. The retained figure scopes a partial rewrite: it is the weight that leaves if you stop
// importing this specific package, so an agent can see which imported package is worth the
// rewrite effort rather than guessing at the module as a whole.
type EntryPackage struct {
	ImportPath         string   `json:"importPath"`
	RetainedBytes      uint64   `json:"retainedBytes"`
	ImportedByPackages []string `json:"importedByPackages,omitempty"`
}

// FloorDelta reports the dep-imposed Go-version floor before and after pruning the inspected
// module — the cross-goal payoff of a size cut. MovesFloor is true when the cut lowers the floor.
type FloorDelta struct {
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
	MovesFloor bool   `json:"movesFloor"`
}

// InspectReport is the single-module drill-down: where first-party code imports the module (the
// edit locations), the per-entry-package weight (the rewrite scope), what leaves vs survives if
// it is pruned, the go-version floor delta, and the import-why trace. It is the machine-readable
// equivalent of the explorer's detail pane — the data an agent needs to act on one prune
// candidate without searching in the dark.
type InspectReport struct {
	Module     string `json:"module"`
	Class      string `json:"class"`
	Locked     bool   `json:"locked,omitempty"`
	Controlled bool   `json:"controlled,omitempty"`
	Target     bool   `json:"target"`
	Size       uint64 `json:"size"`
	GoVersion  string `json:"goVersion,omitempty"`

	FreedBytes     uint64 `json:"freedBytes"`     // exclusive: freed by pruning this module alone
	PotentialBytes uint64 `json:"potentialBytes"` // freeable weight in its subtree if co-holders go too

	EntryPackages []EntryPackage `json:"entryPackages,omitempty"` // the rewrite-scope map
	Sites         []ImportSite   `json:"sites,omitempty"`         // the edit locations
	DragOut       []DepStatus    `json:"dragOut,omitempty"`       // what leaves vs survives + who holds it
	FloorDelta    FloorDelta     `json:"floorDelta"`
	Why           *ImportNode    `json:"why,omitempty"` // who pulled it in, traced back to 1st-class code
}

// Inspect produces the single-module drill-down for module: the edit locations, the
// per-entry-package rewrite scope, the drag-out consequences, and the go-version floor delta.
// It errors if module is not in the build. This is the focused report behind `bonsai inspect`
// and the MCP locate-cuts tool.
func (r *Resolved) Inspect(module string) (InspectReport, error) {
	inspectTask := startTask("Inspect module", "Inspecting module", "Module inspected")
	defer inspectTask.SetCompleted()

	bin, g := r.bin, r.g
	selfSize := bin.SelfSize
	base := g.reachable(nil)

	// attribute self-size per module, and require the requested module to actually be in the build.
	moduleSz := map[string]uint64{}
	for ip := range base {
		if m := g.moduleOfPkg[ip]; m != "" {
			moduleSz[m] += selfSize[ip]
		}
	}
	if _, ok := moduleSz[module]; !ok {
		return InspectReport{}, fmt.Errorf("module %q is not in the build", module)
	}

	cls := classify(g, r.opts.controlled, r.opts.locked, r.opts.unlock)
	dom := g.buildDomModel(selfSize, base, cls)
	importers := g.moduleImporters(base)

	rep := InspectReport{
		Module:     module,
		Class:      cls.classOf(module).String(),
		Locked:     cls.isLocked(module),
		Controlled: g.isControlled(module),
		Target:     cls.isTarget(module),
		Size:       moduleSz[module],
		GoVersion:  g.goVersionOf(module),
		FreedBytes: dom.exclusiveBytes(module),
		Why:        importWhy(module, importers, cls),
	}

	// edit locations + per-import-path first-party importers, scoped to this module.
	sites, importedBy := g.importSitesForModule(module)
	rep.Sites = sites

	// entry-package weights: the rewrite-scope map (gateway children + retained bytes).
	for _, ew := range dom.entryWeights(module) {
		rep.EntryPackages = append(rep.EntryPackages, EntryPackage{
			ImportPath:         ew.pkg,
			RetainedBytes:      ew.retained,
			ImportedByPackages: importedBy[ew.pkg],
		})
	}

	// potential + drag-out: what leaves vs survives if this module is pruned.
	rep.PotentialBytes, rep.DragOut = g.inspectDragOut(module, selfSize, base, moduleSz, importers, cls)

	// floor delta: the floor now vs after severing first-party imports of this module. Only a
	// real prune target can move the floor, so leave After empty for locked/owned modules.
	before := g.goFloor(modulesIn(g, base), cls)
	rep.FloorDelta.Before = before.Version
	if rep.Target {
		after := g.goFloor(modulesIn(g, g.reachable(map[string]bool{module: true})), cls)
		rep.FloorDelta.After = after.Version
		rep.FloorDelta.MovesFloor = cmpGo(after.Version, before.Version) < 0
	}
	return rep, nil
}

// inspectDragOut computes the freeable weight in the inspected module's subtree (PotentialBytes)
// and the per-dependency drag-out status: which modules in its forward subtree leave the build
// if it is pruned, and which survive (held by other still-present importers).
func (g *buildGraph) inspectDragOut(module string, selfSize map[string]uint64, base map[string]bool,
	moduleSz map[string]uint64, importers map[string]map[string]bool, cls *classification) (uint64, []DepStatus) {
	// kept-regardless: packages still reachable when controlled code drops every prune target —
	// the always-present backbone that no single prune can free.
	cutAll := map[string]bool{}
	for _, m := range cls.targets() {
		cutAll[m] = true
	}
	keptRegardless := g.reachable(cutAll)

	// modules still reachable after severing first-party imports of just this module.
	reached := modulesIn(g, g.reachable(map[string]bool{module: true}))

	var potential uint64
	deps := map[string]bool{}
	for ip := range g.reachableFromModule(module) {
		if !base[ip] {
			continue
		}
		if !keptRegardless[ip] {
			potential += selfSize[ip]
		}
		if d := g.moduleOfPkg[ip]; d != "" && d != module {
			deps[d] = true
		}
	}

	out := make([]DepStatus, 0, len(deps))
	for d := range deps {
		st := DepStatus{Module: d, Bytes: moduleSz[d], Freed: !reached[d]}
		if !st.Freed {
			// the other still-present modules that import it — who keeps it besides this one.
			for imp := range importers[d] {
				if imp != module && reached[imp] {
					st.NeededBy = append(st.NeededBy, imp)
				}
			}
			sort.Strings(st.NeededBy)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Module < out[j].Module
	})
	return potential, out
}

// modulesIn collapses a reachable package set to the set of modules with at least one package
// in it — the "modules in the build" view the go-floor and drag-out passes work over.
func modulesIn(g *buildGraph, pkgs map[string]bool) map[string]bool {
	mods := map[string]bool{}
	for ip := range pkgs {
		if m := g.moduleOfPkg[ip]; m != "" {
			mods[m] = true
		}
	}
	return mods
}
