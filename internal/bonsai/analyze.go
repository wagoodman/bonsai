package bonsai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/event"
)

// Config is the input to an analysis run.
//
// The primary mode is source-first: given a module Dir (and optionally Target), bonsai
// builds the binary itself and analyzes the artifact it produced, so it always has
// matching source + binary and the linker's exact reachability graph. Setting Binary
// switches to fallback mode: analyze a prebuilt binary, resolving its source for the build
// graph (no build, no dumpdep — reachability falls back to source-level imports).
type Config struct {
	Dir    string // module directory to build/analyze (default: current directory)
	Target string // build target package (default: the module's sole main package)
	Binary string // analyze this prebuilt binary instead of building from source (fallback mode)

	// Controlled lists the 1st-class modules whose source the user can edit — the modules
	// whose imports are "cuttable". The main module is always controlled. Widening this
	// beyond the main module lets bonsai reason about pruning a dependency from a module you
	// own but didn't author (e.g. stereoscope dropping go-containerregistry). Patterns are
	// exact paths, "path/..." subtrees, or globs.
	Controlled []string
	// Locked lists modules never proposed for pruning. Every controlled module is locked by
	// default (you keep what you own); add load-bearing deps you will always carry. Ignore is
	// a deprecated alias merged into Locked.
	Locked []string
	Ignore []string
	// Unlock re-opens specific locked modules (including controlled ones) as prune candidates,
	// overriding the default lock on controlled modules.
	Unlock []string

	HideIgnored bool // omit locked modules from output entirely (default: show them de-emphasized)
	Blame       bool // also compute Shapley fair-blame attribution (splits shared weight across targets)
	Why         bool // include the import-why trees (the "← imported by" traces); off by default
}

// ModuleSize is the aggregated size and metadata for one module in the binary.
type ModuleSize struct {
	Module    string       `json:"module"`
	Size      uint64       `json:"size"`
	Direct    bool         `json:"direct"`
	Class     string       `json:"class"`               // "main", "1st", "2nd", or "3rd" relative to controlled code
	GoVersion string       `json:"goVersion,omitempty"` // module's declared `go` directive (go.mod), if any
	InBuild   bool         `json:"inBuild"`
	Ignored   bool         `json:"ignored,omitempty"` // locked: on the never-prune list
	Prune     *PruneResult `json:"prune,omitempty"`
	Coupling  *Coupling    `json:"coupling,omitempty"`
	Why       *ImportNode  `json:"why,omitempty"` // who imports this, traced back to 1st-class code
}

// resolve produces the analyzed binary and its build graph for cfg, shared by the static
// report and the interactive Session. It builds from source (capturing -dumpdep reachability)
// unless cfg.Binary selects a prebuilt artifact. The returned cleanup removes any temporary
// build artifact and must always be called once bin/g are no longer being constructed.
func resolve(cfg Config) (*binaryInfo, *buildGraph, func(), error) {
	if cfg.Binary != "" {
		bin, g, err := resolvePrebuilt(cfg)
		return bin, g, func() {}, err
	}
	return resolveFromSource(cfg)
}

// resolveFromSource is the primary path: build the target ourselves (unstripped, capturing
// the linker's -dumpdep reachability), then load the artifact we produced. Source and binary
// always match, and reachability reflects what actually linked.
func resolveFromSource(cfg Config) (*binaryInfo, *buildGraph, func(), error) {
	dir := cfg.Dir
	if dir == "" {
		dir = "."
	}
	target := cfg.Target
	if target == "" {
		t, err := detectTarget(dir)
		if err != nil {
			return nil, nil, nil, err
		}
		target = t
	}

	// a clean git checkout resolves identically every time; reuse the cached snapshot to skip
	// the expensive -dumpdep re-link (the linker can't be served from cache, see cache.go).
	key, cacheable := resolveCacheKey(dir, target)
	if cacheable {
		if bin, g, err := loadResolveCache(key); err == nil {
			cacheTask := startTask("Load cached analysis", "Loading cached analysis", "Loaded cached analysis (same commit)")
			cacheTask.SetCompleted()
			return bin, g, func() {}, nil
		}
	}

	buildTask := startTask("Build binary", "Building binary", "Binary built")
	arts, cleanup, err := buildForAnalysis(dir, target)
	if err != nil {
		buildTask.SetError(err)
		return nil, nil, nil, err
	}
	buildTask.SetCompleted()

	loadTask := startTask("Load binary", "Loading binary", "Binary loaded")
	bin, err := loadBinary(arts.Binary)
	if err != nil {
		loadTask.SetError(err)
		cleanup()
		return nil, nil, nil, err
	}
	loadTask.SetCompleted()

	graphTask := startTask("Resolve build graph", "Resolving build graph", "Build graph resolved")
	g, err := loadBuildGraph(dir, target, "", "") // built for host
	if err != nil {
		graphTask.SetError(err)
		cleanup()
		return nil, nil, nil, err
	}
	// upgrade the source-level import edges to the linker's post-DCE reference graph; on a
	// parse miss (zero edges) keep the go list imports so reachability still works.
	if n, derr := applyReferenceEdges(g, arts.Dumpdep); derr != nil || n == 0 {
		bus.Notify("note: could not use linker reachability (-dumpdep); falling back to source imports")
	}
	graphTask.SetCompleted()

	if cacheable {
		storeResolveCache(key, bin, g) // best-effort; the analysis is correct regardless
	}
	return bin, g, cleanup, nil
}

// optsFrom derives the internal analysis options (classification inputs) from the public
// Config. The deprecated Ignore list is merged into Locked.
func optsFrom(cfg Config) analyzeOpts {
	locked := append(append([]string(nil), cfg.Locked...), cfg.Ignore...)
	return analyzeOpts{
		controlled:  newPatternMatcher(cfg.Controlled),
		locked:      newPatternMatcher(locked),
		unlock:      newPatternMatcher(cfg.Unlock),
		hideIgnored: cfg.HideIgnored,
		blame:       cfg.Blame,
		why:         cfg.Why,
	}
}

// analyzeOpts carries the non-binary inputs that shape the joined analysis.
type analyzeOpts struct {
	controlled  patternMatcher
	locked      patternMatcher
	unlock      patternMatcher
	hideIgnored bool
	blame       bool
	why         bool
}

// resolvePrebuilt is the fallback path: load a binary the user already built. We locate its
// module source (for the build graph and coupling) but never rebuild — a stripped binary
// simply yields code-only attribution, and reachability uses source-level imports.
func resolvePrebuilt(cfg Config) (*binaryInfo, *buildGraph, error) {
	loadTask := startTask("Load binary", "Loading binary", "Binary loaded")
	bin, err := loadBinary(cfg.Binary)
	if err != nil {
		loadTask.SetError(err)
		return nil, nil, err
	}
	loadTask.SetCompleted()

	target := cfg.Target
	if target == "" {
		if bin.MainPkgPath == "" {
			return nil, nil, fmt.Errorf("could not determine target package from %s (no buildinfo); pass --target", cfg.Binary)
		}
		target = bin.MainPkgPath
	}
	dir := cfg.Dir
	if dir == "" {
		dir = findModuleDir(bin.MainModule)
		if dir == "" {
			return nil, nil, fmt.Errorf("could not locate source for module %q; run from within its checkout, "+
				"pass --dir, or drop --binary to build from source", bin.MainModule)
		}
	}

	graphTask := startTask("Resolve build graph", "Resolving build graph", "Build graph resolved")
	g, err := loadBuildGraph(dir, target, bin.GOOS, bin.GOARCH)
	if err != nil {
		graphTask.SetError(err)
		return nil, nil, err
	}
	graphTask.SetCompleted()

	return bin, g, nil
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
