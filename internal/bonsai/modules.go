package bonsai

import "sort"

// ModuleRef is a dependency module of the analysis target, used to populate the lock-list
// editor without running a full build.
type ModuleRef struct {
	Path   string `json:"path"`
	Direct bool   `json:"direct"`
}

// Modules resolves the dependency modules of cfg's target via `go list` (no build), sorted
// by path. The main module is excluded. This is the candidate universe for the lock list.
func Modules(cfg Config) ([]ModuleRef, error) {
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

	g, err := loadBuildGraph(dir, target, "", "")
	if err != nil {
		return nil, err
	}

	out := make([]ModuleRef, 0, len(g.allModules))
	for path := range g.allModules {
		if path == g.mainModule {
			continue
		}
		out = append(out, ModuleRef{Path: path, Direct: g.directMods[path]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
