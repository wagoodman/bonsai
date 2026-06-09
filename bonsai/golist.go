package bonsai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// listPackage mirrors the `go list -deps -json` records we need.
type listPackage struct {
	ImportPath string
	Name       string
	Standard   bool
	Imports    []string
	Module     *listModule
}

type listModule struct {
	Path      string
	Version   string
	Main      bool
	Indirect  bool
	Dir       string
	GoVersion string // the module's declared `go` directive (go.mod), e.g. "1.24.0"; "" if none
}

// buildGraph is the resolved view of everything that links into the target binary.
type buildGraph struct {
	mainModule   string
	mainModDir   string
	packages     map[string]*listPackage // import path -> package
	moduleOfPkg  map[string]string       // import path -> module path ("" for std)
	modulePaths  []string                // all known module paths, longest first (for prefix matching)
	directMods   map[string]bool         // direct (non-indirect) module dependencies
	allModules   map[string]*listModule  // module path -> module
	rootPackages []string                // entrypoint packages (main)

	// controlled is the set of 1st-class modules whose source we can edit — the modules
	// whose outgoing imports are "cuttable" in the reachability model. It always contains
	// the main module; classify() widens it from the user's controlled patterns. Reachability
	// only severs edges that originate in a controlled module (see reachable).
	controlled map[string]bool
}

// isControlled reports whether m is a 1st-class module (editable source). The main module
// is always controlled. Before classify() runs, controlled is nil and only the main module
// qualifies, which preserves the original "first-party = main module only" cut model.
func (g *buildGraph) isControlled(m string) bool {
	if m == g.mainModule {
		return true
	}
	return g.controlled[m]
}

// loadBuildGraph runs `go list -deps -json <target>` in dir and assembles the graph.
// goos/goarch, when set, constrain the build to match the analyzed binary's platform.
func loadBuildGraph(dir, target, goos, goarch string) (*buildGraph, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", target)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if goos != "" {
		cmd.Env = append(cmd.Env, "GOOS="+goos)
	}
	if goarch != "" {
		cmd.Env = append(cmd.Env, "GOARCH="+goarch)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list failed: %w\n%s", err, stderr.String())
	}

	g := &buildGraph{
		packages:    map[string]*listPackage{},
		moduleOfPkg: map[string]string{},
		directMods:  map[string]bool{},
		allModules:  map[string]*listModule{},
	}

	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p listPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		pkg := p
		g.packages[p.ImportPath] = &pkg
		if p.Module != nil {
			g.moduleOfPkg[p.ImportPath] = p.Module.Path
			if _, ok := g.allModules[p.Module.Path]; !ok {
				m := *p.Module
				g.allModules[p.Module.Path] = &m
			}
			if p.Module.Main {
				g.mainModule = p.Module.Path
				g.mainModDir = p.Module.Dir
				g.rootPackages = append(g.rootPackages, p.ImportPath)
			} else if !p.Module.Indirect {
				g.directMods[p.Module.Path] = true
			}
		}
	}

	// the root is the deepest main package; `go list -deps` lists the target last,
	// but be defensive: treat every main-package in the main module as a root.
	if len(g.rootPackages) == 0 {
		return nil, fmt.Errorf("no main module packages found for target %q", target)
	}
	// only the actual command package is a true entrypoint; main-module library
	// packages are reachable only if imported. Narrow roots to package name "main".
	var realRoots []string
	for _, ip := range g.rootPackages {
		if g.packages[ip].Name == "main" {
			realRoots = append(realRoots, ip)
		}
	}
	if len(realRoots) > 0 {
		g.rootPackages = realRoots
	}

	for path := range g.allModules {
		g.modulePaths = append(g.modulePaths, path)
	}
	// longest path first so prefix matching attributes to the most specific module.
	sort.Slice(g.modulePaths, func(i, j int) bool {
		return len(g.modulePaths[i]) > len(g.modulePaths[j])
	})

	return g, nil
}

// detectTarget finds the single buildable entrypoint (a package named "main") under dir,
// used when the user doesn't pass --target. Zero or multiple candidates are an error that
// asks the user to disambiguate.
func detectTarget(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{if eq .Name \"main\"}}{{.ImportPath}}{{end}}", "./...")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("listing packages in %s: %w\n%s", dir, err, stderr.String())
	}

	var mains []string
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			mains = append(mains, line)
		}
	}
	switch len(mains) {
	case 0:
		return "", fmt.Errorf("no main package found in %s; pass --target", dir)
	case 1:
		return mains[0], nil
	default:
		return "", fmt.Errorf("multiple main packages in %s (%s); pass --target to pick one",
			dir, strings.Join(mains, ", "))
	}
}

// moduleForImportPath resolves an arbitrary import path (which may not be present in the
// build graph) to a module via longest-prefix match.
func (g *buildGraph) moduleForImportPath(importPath string) (string, bool) {
	if m, ok := g.moduleOfPkg[importPath]; ok {
		return m, m != ""
	}
	for _, mp := range g.modulePaths {
		if importPath == mp || strings.HasPrefix(importPath, mp+"/") {
			return mp, true
		}
	}
	return "", false
}
