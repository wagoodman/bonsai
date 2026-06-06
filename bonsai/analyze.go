package bonsai

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wagoodman/bonsai/bonsai/event"
	"github.com/wagoodman/bonsai/internal/bus"
)

// Config is the input to an analysis run.
//
// The primary mode is source-first: given a module Dir (and optionally Target), bonsai
// builds the binary itself and analyzes the artifact it produced, so it always has
// matching source + binary and the linker's exact reachability graph. Setting Binary
// switches to fallback mode: analyze a prebuilt binary, resolving its source for the build
// graph (no build, no dumpdep — reachability falls back to source-level imports).
type Config struct {
	Dir         string   // module directory to build/analyze (default: current directory)
	Target      string   // build target package (default: the module's sole main package)
	Binary      string   // analyze this prebuilt binary instead of building from source (fallback mode)
	Ignore      []string // module patterns never suggested for pruning (exact, glob, or "path/...")
	HideIgnored bool     // omit ignored modules from output entirely (default: show them de-emphasized)
}

// ModuleSize is the aggregated size and metadata for one module in the binary.
type ModuleSize struct {
	Module   string       `json:"module"`
	Size     uint64       `json:"size"`
	Direct   bool         `json:"direct"`
	InBuild  bool         `json:"inBuild"`
	Ignored  bool         `json:"ignored,omitempty"` // on the user's never-prune list
	Prune    *PruneResult `json:"prune,omitempty"`
	Coupling *Coupling    `json:"coupling,omitempty"`
}

// SharedModule is a dependency pulled into the binary by more than one direct dep — shared,
// load-bearing weight that no single prune can remove. SharedBy counts the distinct direct
// dependencies whose subtree reaches it; higher means more structural.
type SharedModule struct {
	Module   string `json:"module"`
	Bytes    uint64 `json:"bytes"`
	SharedBy int    `json:"sharedBy"`
	Ignored  bool   `json:"ignored,omitempty"`
}

// Analysis is the complete result of attributing a binary's size to its modules and
// estimating the cost/benefit of pruning each direct dependency.
type Analysis struct {
	BinarySize    uint64         `json:"binarySize"`    // analyzed file size on disk
	AccountedSize uint64         `json:"accountedSize"` // file-backed, non-debug sections (~ stripped binary size)
	CodeSize      uint64         `json:"codeSize"`      // executable code
	DataSize      uint64         `json:"dataSize"`      // named data (rodata/data globals)
	PclntabSize   uint64         `json:"pclntabSize"`   // gopclntab metadata (distributed proportionally)
	StdSize       uint64         `json:"stdSize"`       // standard library
	MainSize      uint64         `json:"mainSize"`      // main module
	MainModule    string         `json:"mainModule"`
	GeneratedSize uint64         `json:"generatedSize"` // compiler-generated + anonymous (pooled constants, type metadata)
	Stripped      bool           `json:"stripped"`      // true if only code could be attributed
	Sections      []SectionInfo  `json:"sections"`
	Modules       []ModuleSize   `json:"modules"`
	Shared        []SharedModule `json:"shared"` // load-bearing deps pulled in by 2+ direct deps
	Actions       []PruneAction  `json:"pruneActions"`
	HideIgnored   bool           `json:"-"` // presentation: drop ignored modules instead of dimming them
}

// Analyze resolves the build graph for the configured module (or prebuilt binary) and
// joins size attribution, tree-shake, and coupling signals into a single Analysis. With
// cfg.Binary set it analyzes that prebuilt artifact; otherwise it builds cfg.Target from
// cfg.Dir and analyzes the result.
func Analyze(cfg Config) (*Analysis, error) {
	if cfg.Binary != "" {
		return analyzePrebuilt(cfg)
	}
	return analyzeFromSource(cfg)
}

// analyzeFromSource is the primary path: build the target ourselves (unstripped, capturing
// the linker's -dumpdep reachability), then analyze the artifact we produced. Source and
// binary always match, and reachability reflects what actually linked.
func analyzeFromSource(cfg Config) (*Analysis, error) {
	dir := cfg.Dir
	if dir == "" {
		dir = "."
	}
	target := cfg.Target
	if target == "" {
		t, err := detectTarget(dir)
		if err != nil {
			return nil, err
		}
		target = t
	}

	buildTask := startTask("Build binary", "building binary", "binary built")
	arts, cleanup, err := buildForAnalysis(dir, target)
	if err != nil {
		buildTask.SetError(err)
		return nil, err
	}
	defer cleanup()
	buildTask.SetCompleted()

	loadTask := startTask("Load binary", "loading binary", "binary loaded")
	bin, err := loadBinary(arts.Binary)
	if err != nil {
		loadTask.SetError(err)
		return nil, err
	}
	loadTask.SetCompleted()

	graphTask := startTask("Resolve build graph", "resolving build graph", "build graph resolved")
	g, err := loadBuildGraph(dir, target, "", "") // built for host
	if err != nil {
		graphTask.SetError(err)
		return nil, err
	}
	// upgrade the source-level import edges to the linker's post-DCE reference graph; on a
	// parse miss (zero edges) keep the go list imports so reachability still works.
	if n, derr := applyReferenceEdges(g, arts.Dumpdep); derr != nil || n == 0 {
		bus.Notify("note: could not use linker reachability (-dumpdep); falling back to source imports")
	}
	graphTask.SetCompleted()

	return analyze(bin, g, optsFrom(cfg)), nil
}

// optsFrom derives the internal analysis options (ignore handling) from the public Config.
func optsFrom(cfg Config) analyzeOpts {
	return analyzeOpts{
		ignore:      newIgnoreMatcher(cfg.Ignore),
		hideIgnored: cfg.HideIgnored,
	}
}

// analyzeOpts carries the non-binary inputs that shape the joined analysis.
type analyzeOpts struct {
	ignore      ignoreMatcher
	hideIgnored bool
}

// analyzePrebuilt is the fallback path: analyze a binary the user already built. We locate
// its module source (for the build graph and coupling) but never rebuild — a stripped
// binary simply yields code-only attribution, and reachability uses source-level imports.
func analyzePrebuilt(cfg Config) (*Analysis, error) {
	loadTask := startTask("Load binary", "loading binary", "binary loaded")
	bin, err := loadBinary(cfg.Binary)
	if err != nil {
		loadTask.SetError(err)
		return nil, err
	}
	loadTask.SetCompleted()

	target := cfg.Target
	if target == "" {
		if bin.MainPkgPath == "" {
			return nil, fmt.Errorf("could not determine target package from %s (no buildinfo); pass --target", cfg.Binary)
		}
		target = bin.MainPkgPath
	}
	dir := cfg.Dir
	if dir == "" {
		dir = findModuleDir(bin.MainModule)
		if dir == "" {
			return nil, fmt.Errorf("could not locate source for module %q; run from within its checkout, "+
				"pass --dir, or drop --binary to build from source", bin.MainModule)
		}
	}

	graphTask := startTask("Resolve build graph", "resolving build graph", "build graph resolved")
	g, err := loadBuildGraph(dir, target, bin.GOOS, bin.GOARCH)
	if err != nil {
		graphTask.SetError(err)
		return nil, err
	}
	graphTask.SetCompleted()

	return analyze(bin, g, optsFrom(cfg)), nil
}

func analyze(bin *binaryInfo, g *buildGraph, opts analyzeOpts) *Analysis {
	an := &Analysis{
		BinarySize:    bin.FileSize,
		AccountedSize: bin.SectionsSize,
		CodeSize:      bin.CodeSize,
		DataSize:      bin.DataSize,
		PclntabSize:   bin.PclntabSize,
		MainModule:    g.mainModule,
		Stripped:      bin.Stripped,
		Sections:      bin.Sections,
		HideIgnored:   opts.hideIgnored,
	}

	bySize := map[string]uint64{}
	for pkgPath, sz := range bin.SelfSize {
		mod, ok := g.moduleForImportPath(pkgPath)
		switch {
		case !ok && (pkgPath == "" || pkgPath[0] == '<'):
			an.GeneratedSize += sz // compiler-generated / anonymous, no real package
		case !ok:
			an.StdSize += sz // standard library (no module)
		case mod == g.mainModule:
			an.MainSize += sz
			bySize[mod] += sz
		default:
			bySize[mod] += sz
		}
	}

	attrTask := startTask("Attribute size", "attributing size", "size attributed")
	baseReachable := g.reachable(nil)
	// coupling needs the main module source tree; skip it when analyzing a prebuilt binary
	// whose source we couldn't locate.
	var coup map[string]*Coupling
	if g.mainModDir != "" {
		coup, _ = scanCoupling(g)
	}

	for mod, sz := range bySize {
		ignored := opts.ignore.match(mod)
		ms := ModuleSize{
			Module:  mod,
			Size:    sz,
			Direct:  g.directMods[mod],
			InBuild: true,
			Ignored: ignored,
		}
		if mod != g.mainModule {
			ms.Coupling = coup[mod]
		}
		// only offer a prune estimate for droppable direct deps; ignored deps are core and
		// never suggested for removal.
		if g.directMods[mod] && !ignored {
			p := g.treeShake(mod, bin.SelfSize, baseReachable)
			ms.Prune = &p
		}
		an.Modules = append(an.Modules, ms)
	}
	sort.Slice(an.Modules, func(i, j int) bool { return an.Modules[i].Size > an.Modules[j].Size })
	attrTask.SetCompleted()

	actionTask := startTask("Compute prune actions", "computing prune actions", "prune actions computed")
	an.Actions = g.pruneActions(bin.SelfSize, opts.ignore)
	an.Shared = g.sharedModules(bin.SelfSize, opts.ignore)
	actionTask.SetCompleted()

	return an
}

// startTask publishes an indeterminate progress task to the bus; callers should
// SetCompleted (or SetError) when the phase finishes. With no bus configured this is a
// harmless no-op that still returns a usable progress handle.
func startTask(title, running, success string) *event.ManualStagedProgress {
	return bus.PublishTask(event.Title{
		Default:      title,
		WhileRunning: running,
		OnSuccess:    success,
	}, "", -1)
}

// findModuleDir walks up from the current directory looking for a go.mod whose module
// path equals want, returning that directory. This lets the tool be invoked from a
// subdirectory and still find the source of the binary's main module. Returns "" if not
// found.
func findModuleDir(want string) string {
	if want == "" {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := cwd; ; {
		if modulePathOf(dir) == want {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// modulePathOf returns the module path declared in dir/go.mod, or "" if absent.
func modulePathOf(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if path, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

// shortModule returns the last path element of a module path, used as a friendly label.
func shortModule(module string) string {
	if module == "" {
		return "main"
	}
	if i := strings.LastIndex(module, "/"); i >= 0 {
		return module[i+1:]
	}
	return module
}
