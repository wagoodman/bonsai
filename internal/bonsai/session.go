package bonsai

import "sort"

// detailTree* are the generous bounds for the explorer's scrollable why/deps trees — large
// enough to show everything on real graphs (the trees dedup globally, so this stays finite).
const (
	detailTreeBudget  = 2000
	detailTreeBreadth = 1000
)

// Session is a live analysis model for interactive exploration (the `explore` TUI). The
// expensive, immutable data — the built binary, its size attribution, the import graph, and
// coupling — is computed once. The classification "view" (which modules are controlled,
// locked, and therefore prune candidates) is mutable: Reclassify re-derives it cheaply, which
// is what lets the explorer toggle controlled/locked/unlock and watch the candidate set
// change. Every what-if query is a single sweep over the reachability index, so it is fast
// enough to recompute on every keystroke.
type Session struct {
	// immutable build/size data
	bin       *binaryInfo
	g         *buildGraph
	base      map[string]bool
	selfSize  map[string]uint64
	moduleSz  map[string]uint64          // attributed bytes per module (over base)
	coupling  map[string]*Coupling       // per-module first-party coupling (nil if no source)
	importers map[string]map[string]bool // reverse module graph: who imports m (go mod why)
	importees map[string]map[string]bool // forward module graph: what m imports (go mod graph)

	// mutable classification view, re-derived by Reclassify
	inputs ClassInputs
	cls    *classification
	ri     *reachIndex
	dom    *domModel
}

// ClassInputs are the pattern lists that drive classification. Mutate and pass to Reclassify
// to re-derive the candidate set live.
type ClassInputs struct {
	Controlled []string
	Locked     []string
	Unlock     []string
}

// NewSession builds and loads the target (or prebuilt binary) once and returns a model ready
// for interactive what-if queries. The initial classification comes from cfg.
func NewSession(cfg Config) (*Session, error) {
	bin, g, cleanup, err := resolve(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	s := &Session{
		bin:      bin,
		g:        g,
		base:     g.reachable(nil), // classification-independent (empty cut severs nothing)
		selfSize: bin.SelfSize,
		moduleSz: map[string]uint64{},
	}
	for ip := range s.base {
		if mod := g.moduleOfPkg[ip]; mod != "" {
			s.moduleSz[mod] += s.selfSize[ip]
		}
	}
	if g.mainModDir != "" {
		s.coupling, _ = scanCoupling(g)
	}
	s.importers = g.moduleImporters(s.base)
	s.importees = g.moduleImportees(s.base)

	s.Reclassify(ClassInputs{
		Controlled: cfg.Controlled,
		Locked:     cfg.Locked,
		Unlock:     cfg.Unlock,
	})
	return s, nil
}

// Reclassify re-derives the classification and the reachability/dominator structures from new
// controlled/locked/unlock inputs. Cheap enough to call on every toggle.
func (s *Session) Reclassify(in ClassInputs) {
	s.inputs = in
	s.cls = classify(s.g, newPatternMatcher(in.Controlled), newPatternMatcher(in.Locked), newPatternMatcher(in.Unlock))
	s.ri = s.g.newReachIndex(s.selfSize, s.base, s.cls)
	s.dom = s.g.buildDomModel(s.selfSize, s.base, s.cls)
}

// Inputs returns the current classification inputs (so the TUI can mutate and Reclassify).
func (s *Session) Inputs() ClassInputs { return s.inputs }

// MainModule is the path of the module being analyzed.
func (s *Session) MainModule() string { return s.g.mainModule }

// AccountedSize is the analyzed binary's attributable size (≈ a stripped release binary) — the
// baseline a what-if projects down from.
func (s *Session) AccountedSize() uint64 { return s.bin.SectionsSize }

// BinarySize is the analyzed file's size on disk: the original binary as built, including any
// debug info and symbols that stripping (`-s -w`) would remove. Equals AccountedSize when the
// input is already stripped.
func (s *Session) BinarySize() uint64 { return s.bin.FileSize }

// Module is one row for the explorer list: its size, classification state, prune value
// (Exclusive), and removal-effort signals (Coupling, Importers).
type Module struct {
	Module     string
	Class      string // "main", "1st", "2nd", "3rd"
	Locked     bool   // never proposed for pruning; not selectable
	Controlled bool   // 1st-class (yours) — shown in gold even when locked
	Target     bool   // a selectable prune candidate
	Size       uint64 // attributed bytes in the binary
	Exclusive  uint64 // bytes freed by pruning this alone (dominator retained size)
	GoVersion  string // the module's declared `go` directive (go.mod), if any
	Coupling   Coupling
	Importers  int // distinct modules that import it (fan-in)
}

// Modules returns every non-main module in the build, sorted by prune value (Exclusive) then
// size, so the list opens on the highest-leverage candidates while still showing locked and
// 1st-class modules (for the explorer to grey/gold).
func (s *Session) Modules() []Module {
	out := make([]Module, 0, len(s.moduleSz))
	for mod, sz := range s.moduleSz {
		if mod == s.g.mainModule {
			continue
		}
		m := Module{
			Module:     mod,
			Class:      s.cls.classOf(mod).String(),
			Locked:     s.cls.isLocked(mod),
			Controlled: s.g.isControlled(mod),
			Target:     s.cls.isTarget(mod),
			Size:       sz,
			Exclusive:  s.dom.exclusiveBytes(mod),
			GoVersion:  s.g.goVersionOf(mod),
			Importers:  len(s.importers[mod]),
		}
		if c := s.coupling[mod]; c != nil {
			m.Coupling = *c
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Exclusive != out[j].Exclusive {
			return out[i].Exclusive > out[j].Exclusive
		}
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Module < out[j].Module
	})
	return out
}

// WhatIf is the result of pruning a selected set of modules: how far the binary shrinks and
// which modules actually leave the build (accounting for deps shared across selections).
type WhatIf struct {
	OriginalSize  uint64
	FreedBytes    uint64
	ProjectedSize uint64
	PrunedModules []string // modules fully removed from the build by this selection
}

// WhatIf evaluates pruning the selected module set. Only selected modules that are current
// prune targets contribute; the rest are ignored (locked deps can't be dropped).
func (s *Session) WhatIf(selected map[string]bool) WhatIf {
	cut := s.cut(selected)
	freed := s.ri.freedBytes(cut)

	freedByMod := map[string]uint64{}
	for k, b := range s.ri.freedBreakdown(cut) {
		if !k.std {
			freedByMod[k.name] += b
		}
	}
	var pruned []string
	for mod, fb := range freedByMod {
		if total := s.moduleSz[mod]; total > 0 && fb >= total {
			pruned = append(pruned, mod) // every attributed byte of the module is gone
		}
	}
	sort.Strings(pruned)

	orig := s.AccountedSize()
	return WhatIf{
		OriginalSize:  orig,
		FreedBytes:    freed,
		ProjectedSize: satSub(orig, freed),
		PrunedModules: pruned,
	}
}

// GoFloor reports the dep-imposed minimum Go version for the owned modules given the current
// prune selection: as targets are selected, the modules they orphan leave the build and the
// floor can drop. The Critical modules are the surviving non-owned modules pinning it — the
// answer to "which dependencies are why my go directive can't go lower right now".
func (s *Session) GoFloor(selected map[string]bool) GoFloor {
	surviving := s.ri.reachedModules(s.cut(selected))
	return s.g.goFloor(surviving, s.cls)
}

// Marginal is the extra bytes freed by adding module to the current selection — the live
// "+X MB if I also drop this" number that shrinks as shared weight gets claimed.
func (s *Session) Marginal(selected map[string]bool, module string) uint64 {
	tid, ok := s.ri.targetID[module]
	if !ok {
		return 0
	}
	cut := s.cut(selected)
	before := s.ri.freedBytes(cut)
	cut[tid] = true
	return s.ri.freedBytes(cut) - before
}

// Detail is the right-pane summary for one module.
type Detail struct {
	Module     string
	Class      string
	Locked     bool
	Controlled bool
	Target     bool
	Size       uint64
	Own        uint64        // the module's own code among its exclusive savings
	Exclusive  uint64        // freed by pruning it alone (net: shared deps don't count)
	PullsIn    uint64        // gross weight of everything it reaches (incl. shared deps that stay)
	GoVersion  string        // the module's declared `go` directive (go.mod), if any
	DragOut    []FreedModule // other modules freed with it (its exclusive subtree), largest first
	Coupling   Coupling
	Importers  int
	Why        *ImportNode // who imports it (reverse / go mod why)
	Deps       *ImportNode // what it imports (forward / go mod graph)
}

// Detail builds the right-pane summary for module: size, coupling, the deps it would drag out
// if pruned, and the forward/reverse import trees.
func (s *Session) Detail(module string) Detail {
	d := Detail{
		Module:     module,
		Class:      s.cls.classOf(module).String(),
		Locked:     s.cls.isLocked(module),
		Controlled: s.g.isControlled(module),
		Target:     s.cls.isTarget(module),
		Size:       s.moduleSz[module],
		Exclusive:  s.dom.exclusiveBytes(module),
		GoVersion:  s.g.goVersionOf(module),
		Importers:  len(s.importers[module]),
		// the explorer's panes scroll, so build the full trees (not the report's bounded view);
		// global dedup keeps them finite.
		Why:  importTree(module, s.importers, s.cls, detailTreeBudget, detailTreeBreadth, true),
		Deps: importTree(module, s.importees, s.cls, detailTreeBudget, detailTreeBreadth, false),
	}
	if c := s.coupling[module]; c != nil {
		d.Coupling = *c
	}

	// pruning this module alone frees Exclusive; the modules it also reaches that survive (held
	// by other importers) make up the rest of what it pulls in. PullsIn = Exclusive + held, so
	// the UI can show both "frees X" and "of Y pulled in (Y−X held by others)" and always have
	// them reconcile. Std is excluded from held — it's shared by everything and never the story.
	if d.Target {
		survivors := s.ri.reachedModules(s.cut(map[string]bool{module: true}))
		var held uint64
		seen := map[string]bool{}
		for ip := range s.g.reachableFromModule(module) {
			dep := s.g.moduleOfPkg[ip]
			if !s.base[ip] || dep == "" || dep == module || seen[dep] {
				continue
			}
			seen[dep] = true
			if survivors[dep] { // still reachable without this module's edges → shared, stays
				held += s.moduleSz[dep]
			}
		}
		d.PullsIn = d.Exclusive + held
	}

	// split the exclusive subtree into the module's own bytes vs the deps it drags out.
	dragBytes := map[string]uint64{}
	for _, ip := range s.dom.exclusivePkgs(module) {
		mod := s.g.moduleOfPkg[ip]
		if mod == module {
			d.Own += s.selfSize[ip]
			continue
		}
		if mod == "" {
			mod = modStd
		}
		dragBytes[mod] += s.selfSize[ip]
	}
	for mod, b := range dragBytes {
		d.DragOut = append(d.DragOut, FreedModule{Module: mod, Bytes: b, Std: mod == modStd})
	}
	sort.Slice(d.DragOut, func(i, j int) bool {
		if d.DragOut[i].Bytes != d.DragOut[j].Bytes {
			return d.DragOut[i].Bytes > d.DragOut[j].Bytes
		}
		return d.DragOut[i].Module < d.DragOut[j].Module
	})
	return d
}

// DepStatus is one dependency a module pulls in, and whether it actually leaves the build
// under the current selection. When it survives (Freed false), NeededBy names the still-present
// modules that keep it — the answer to "I pruned this, why is X still here?".
type DepStatus struct {
	Module   string   `json:"module"`
	Bytes    uint64   `json:"bytes"`
	Freed    bool     `json:"freed"`
	NeededBy []string `json:"neededBy,omitempty"`
}

// DragOutStatus reports, for the modules that the given module pulls in, which actually leave
// under the current selection and which survive (and who holds the survivors). This is the
// live "what really gets pruned" view: a dep shared across several deps only disappears once
// every holder is deselected... er, selected for pruning.
func (s *Session) DragOutStatus(selected map[string]bool, module string) []DepStatus {
	cut := s.cut(selected)
	reached := s.ri.reachedModules(cut)

	// the modules this one pulls in (its forward subtree).
	deps := map[string]bool{}
	for ip := range s.g.reachableFromModule(module) {
		if !s.base[ip] {
			continue
		}
		if d := s.g.moduleOfPkg[ip]; d != "" && d != module {
			deps[d] = true
		}
	}

	out := make([]DepStatus, 0, len(deps))
	for d := range deps {
		st := DepStatus{Module: d, Bytes: s.moduleSz[d], Freed: !reached[d]}
		if !st.Freed {
			// the OTHER still-present modules that import it — who keeps it besides this one.
			// Empty means it's exclusive to this module (it would leave if this module were pruned).
			for imp := range s.importers[d] {
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
	return out
}

// cut translates a selection of module paths into the reachIndex's target-indexed cut,
// keeping only modules that are current prune targets.
func (s *Session) cut(selected map[string]bool) []bool {
	cut := make([]bool, len(s.ri.targets))
	for m := range selected {
		if tid, ok := s.ri.targetID[m]; ok {
			cut[tid] = true
		}
	}
	return cut
}

func satSub(a, b uint64) uint64 {
	if b >= a {
		return 0
	}
	return a - b
}
