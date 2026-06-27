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

	// Platform selects the build cell: a GOOS/GOARCH target and a set of build tags. The zero
	// value means "host platform, no extra tags" — exactly the original behavior, so existing
	// call sites are unchanged. The matrix command sets it per cell.
	Platform Platform

	// Build holds persisted build defaults (extra tags, env overrides, freeform go-toolchain
	// args) applied to every build/list. Platform.Tags extend Build.Tags per cell. Zero value
	// is the host toolchain with no extra flags.
	Build BuildSettings

	// Controlled lists the 1st-class modules whose source the user can edit — the modules
	// whose imports are "cuttable". The main module is always controlled. Widening this
	// beyond the main module lets bonsai reason about pruning a dependency from a module you
	// own but didn't author (e.g. stereoscope dropping go-containerregistry). Patterns are
	// exact paths, "path/..." subtrees, or globs.
	Controlled []string
	// Locked lists modules never proposed for pruning. Every controlled module is locked by
	// default (you keep what you own); add load-bearing deps you will always carry.
	Locked []string
	// Unlock re-opens specific locked modules (including controlled ones) as prune candidates,
	// overriding the default lock on controlled modules.
	Unlock []string

	HideLocked bool // omit locked modules from output entirely (default: show them de-emphasized)
	Blame      bool // also compute Shapley fair-blame attribution (splits shared weight across targets)
	Why        bool // include the import-why trees (the "← imported by" traces); off by default
}

// ModuleSize is the aggregated size and metadata for one module in the binary.
type ModuleSize struct {
	Module    string       `json:"module"`
	Size      uint64       `json:"size"`
	Direct    bool         `json:"direct"`
	Class     string       `json:"class"`               // "main", "1st", "2nd", or "3rd" relative to controlled code
	GoVersion string       `json:"goVersion,omitempty"` // module's declared `go` directive (go.mod), if any
	InBuild   bool         `json:"inBuild"`
	Locked    bool         `json:"locked,omitempty"` // on the never-prune list
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
	dir, target, err := dirTarget(cfg)
	if err != nil {
		return nil, nil, nil, err
	}

	// a clean git checkout resolves identically every time; reuse the cached snapshot to skip
	// the expensive -dumpdep re-link (the linker can't be served from cache, see cache.go).
	key, cacheable := resolveCacheKey(dir, target, cfg.Platform, cfg.Build)
	if cacheable {
		if bin, g, err := loadResolveCache(key); err == nil {
			cacheTask := startTask("Load cached analysis", "Loading cached analysis", "Loaded cached analysis (same commit)")
			cacheTask.SetCompleted()
			return bin, g, func() {}, nil
		}
	}

	buildTask := startTask("Build binary", "Building binary", "Binary built")
	arts, cleanup, err := buildForAnalysis(dir, target, cfg.Platform, cfg.Build)
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
	g, err := loadBuildGraph(dir, target, cfg.Platform, cfg.Build)
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

// dirTarget resolves the effective module directory and build target from cfg, defaulting the
// directory to "." and detecting the sole main package when Target is unset. Shared by the
// source resolvers and the build-free graph path.
func dirTarget(cfg Config) (dir, target string, err error) {
	dir = cfg.Dir
	if dir == "" {
		dir = "."
	}
	target = cfg.Target
	if target == "" {
		t, derr := detectTarget(dir)
		if derr != nil {
			return "", "", derr
		}
		target = t
	}
	return dir, target, nil
}

// optsFrom derives the internal analysis options (classification inputs) from the public
// Config.
func optsFrom(cfg Config) analyzeOpts {
	return analyzeOpts{
		controlled: newPatternMatcher(cfg.Controlled),
		locked:     newPatternMatcher(cfg.Locked),
		unlock:     newPatternMatcher(cfg.Unlock),
		hideLocked: cfg.HideLocked,
		blame:      cfg.Blame,
		why:        cfg.Why,
	}
}

// analyzeOpts carries the non-binary inputs that shape the joined analysis.
type analyzeOpts struct {
	controlled patternMatcher
	locked     patternMatcher
	unlock     patternMatcher
	hideLocked bool
	blame      bool
	why        bool
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
	g, err := loadBuildGraph(dir, target, Platform{GOOS: bin.GOOS, GOARCH: bin.GOARCH}, cfg.Build)
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
