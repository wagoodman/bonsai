package bonsai

import "sort"

// Resolved is a built-and-loaded analysis target ready to answer focused questions about the
// binary. The expensive shared work — building the binary and resolving its import graph — is
// done once by Resolve; each report method (Size, Prune, GoFloor) then computes only the slice
// of analysis its subject needs. This mirrors Session (the interactive model) but for the
// static, one-shot report commands: anatomy, prune, and go-version are three separate subjects,
// so each runs as little of the engine as possible and emits only its own result type.
type Resolved struct {
	bin     *binaryInfo
	g       *buildGraph
	opts    analyzeOpts
	cleanup func()
}

// Resolve builds the configured module (or loads the prebuilt binary) and resolves its import
// graph, returning a handle for the focused report methods. Call Close when done to remove any
// temporary build artifact.
func Resolve(cfg Config) (*Resolved, error) {
	bin, g, cleanup, err := resolve(cfg)
	if err != nil {
		return nil, err
	}
	return &Resolved{bin: bin, g: g, opts: optsFrom(cfg), cleanup: cleanup}, nil
}

// Close releases the resolved target's temporary build artifact, if any.
func (r *Resolved) Close() {
	if r.cleanup != nil {
		r.cleanup()
	}
}

// SizeReport is the binary's anatomy: how big it is and what occupies the space, attributed
// by content (code / data / pclntab) and by owner (which module). It is the default `bonsai`
// subject and intentionally carries no prune or go-version analysis.
type SizeReport struct {
	BinarySize    uint64        `json:"binarySize"`    // analyzed file size on disk
	AccountedSize uint64        `json:"accountedSize"` // file-backed, non-debug sections (~ stripped size)
	CodeSize      uint64        `json:"codeSize"`      // executable code
	DataSize      uint64        `json:"dataSize"`      // named data (rodata/data globals)
	PclntabSize   uint64        `json:"pclntabSize"`   // gopclntab metadata
	StdSize       uint64        `json:"stdSize"`       // standard library
	MainSize      uint64        `json:"mainSize"`      // main module
	MainModule    string        `json:"mainModule"`
	GeneratedSize uint64        `json:"generatedSize"` // compiler-generated + anonymous
	Stripped      bool          `json:"stripped"`
	Sections      []SectionInfo `json:"sections"`
	Modules       []ModuleSize  `json:"modules"` // anatomy view: Prune/Coupling left nil
	HideLocked    bool          `json:"-"`       // presentation: drop locked modules instead of dimming
}

// PruneReport is the prune subject: which dependencies, if removed, free the most bytes, with
// per-target coupling (removal effort) and a greedy ordered plan. Shapley fair-blame is opt-in.
type PruneReport struct {
	AccountedSize uint64          `json:"accountedSize"` // baseline the savings project down from
	MainModule    string          `json:"mainModule"`
	Modules       []ModuleSize    `json:"modules"` // prune candidates carry Prune + Coupling
	Plan          []PrunePlanStep `json:"prunePlan,omitempty"`
	Blame         []ModuleBlame   `json:"blame,omitempty"`
	HideLocked    bool            `json:"-"`
}

// Size attributes the binary's bytes by content and by owning module. It runs classification
// (for class/kind labels) but none of the prune/dominator machinery.
func (r *Resolved) Size() SizeReport {
	attrTask := startTask("Attribute size", "Attributing size", "Size attributed")
	defer attrTask.SetCompleted()

	bin, g := r.bin, r.g
	attr := attributeSizes(bin, g)
	rep := SizeReport{
		BinarySize:    bin.FileSize,
		AccountedSize: bin.SectionsSize,
		CodeSize:      bin.CodeSize,
		DataSize:      bin.DataSize,
		PclntabSize:   bin.PclntabSize,
		StdSize:       attr.std,
		MainSize:      attr.main,
		MainModule:    g.mainModule,
		GeneratedSize: attr.generated,
		Stripped:      bin.Stripped,
		Sections:      bin.Sections,
		HideLocked:    r.opts.hideLocked,
	}

	cls := classify(g, r.opts.controlled, r.opts.locked, r.opts.unlock)
	var importers map[string]map[string]bool
	if r.opts.why {
		importers = g.moduleImporters(g.reachable(nil))
	}
	for mod, sz := range attr.bySize {
		ms := ModuleSize{
			Module:    mod,
			Size:      sz,
			Direct:    g.directMods[mod],
			Class:     cls.classOf(mod).String(),
			GoVersion: g.goVersionOf(mod),
			InBuild:   true,
			Locked:    cls.isLocked(mod),
		}
		if importers != nil && !owned(cls.classOf(mod)) {
			ms.Why = importWhy(mod, importers, cls)
		}
		rep.Modules = append(rep.Modules, ms)
	}
	sort.Slice(rep.Modules, func(i, j int) bool { return rep.Modules[i].Size > rep.Modules[j].Size })
	return rep
}

// Prune computes the cost/benefit of pruning each direct dependency: the dominator-based
// retained size, per-target coupling, and a greedy ordered plan. Shapley fair-blame is computed
// only when the analysis was configured with Blame.
func (r *Resolved) Prune() PruneReport {
	bin, g := r.bin, r.g
	bySize := attributeSizes(bin, g).bySize

	attrTask := startTask("Attribute size", "Attributing size", "Size attributed")
	cls := classify(g, r.opts.controlled, r.opts.locked, r.opts.unlock)
	base := g.reachable(nil)
	dom := g.buildDomModel(bin.SelfSize, base, cls)
	blockers := g.blockerSets(cls)
	prunes := g.pruneResults(bin.SelfSize, base, cls, dom, blockers)

	var importers map[string]map[string]bool
	if r.opts.why {
		importers = g.moduleImporters(base)
	}
	var coup map[string]*Coupling
	if g.mainModDir != "" {
		coup, _ = scanCoupling(g)
	}

	rep := PruneReport{
		AccountedSize: bin.SectionsSize,
		MainModule:    g.mainModule,
		HideLocked:    r.opts.hideLocked,
	}
	for mod, sz := range bySize {
		ms := ModuleSize{
			Module:    mod,
			Size:      sz,
			Direct:    g.directMods[mod],
			Class:     cls.classOf(mod).String(),
			GoVersion: g.goVersionOf(mod),
			InBuild:   true,
			Locked:    cls.isLocked(mod),
		}
		if mod != g.mainModule {
			ms.Coupling = coup[mod]
		}
		if p := prunes[mod]; p != nil {
			ms.Prune = p
		}
		if importers != nil && !owned(cls.classOf(mod)) {
			ms.Why = importWhy(mod, importers, cls)
		}
		rep.Modules = append(rep.Modules, ms)
	}
	sort.Slice(rep.Modules, func(i, j int) bool { return rep.Modules[i].Size > rep.Modules[j].Size })
	attrTask.SetCompleted()

	planTask := startTask("Compute prune plan", "Computing prune plan", "Prune plan computed")
	rep.Plan = g.greedyPlan(bin.SelfSize, base, cls)
	if importers != nil {
		attachPlanWhy(rep.Plan, importers, cls)
	}
	planTask.SetCompleted()

	if r.opts.blame {
		blameTask := startTask("Compute blame", "Computing fair-blame attribution", "Blame computed")
		rep.Blame = g.shapleyBlame(bin.SelfSize, base, cls)
		blameTask.SetCompleted()
	}
	return rep
}

// GoFloor reports the lowest `go` directive the owned modules could declare given the modules
// actually in the build, and the dependencies pinning that floor. It runs classification and a
// single reachability sweep — none of the prune/dominator machinery.
func (r *Resolved) GoFloor() GoFloor {
	floorTask := startTask("Compute go floor", "Computing go version floor", "Go version floor computed")
	defer floorTask.SetCompleted()

	g := r.g
	cls := classify(g, r.opts.controlled, r.opts.locked, r.opts.unlock)
	inBuild := map[string]bool{}
	for ip := range g.reachable(nil) {
		if mod := g.moduleOfPkg[ip]; mod != "" {
			inBuild[mod] = true
		}
	}
	return g.goFloor(inBuild, cls)
}

// sizeAttribution holds per-module self-size totals along with the std-library, main-module,
// and compiler-generated buckets that have no real owning third-party module.
type sizeAttribution struct {
	bySize    map[string]uint64
	std       uint64
	main      uint64
	generated uint64
}

// attributeSizes buckets each package's self-size onto its owning module, accumulating the
// std-library, main-module, and compiler-generated/anonymous totals separately (these have no
// real owning third-party module). Shared by the Size and Prune reports.
func attributeSizes(bin *binaryInfo, g *buildGraph) sizeAttribution {
	a := sizeAttribution{bySize: map[string]uint64{}}
	for pkgPath, sz := range bin.SelfSize {
		mod, ok := g.moduleForImportPath(pkgPath)
		switch {
		case !ok && (pkgPath == "" || pkgPath[0] == '<'):
			a.generated += sz // compiler-generated / anonymous, no real package
		case !ok:
			a.std += sz // standard library (no module)
		case mod == g.mainModule:
			a.main += sz
			a.bySize[mod] += sz
		default:
			a.bySize[mod] += sz
		}
	}
	return a
}
