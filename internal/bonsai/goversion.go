package bonsai

import (
	"go/version"
	"sort"
)

// The "what's the lowest go directive I could declare?" question. A module's go.mod `go` line
// is a floor: Go requires the main module's directive to be at least as high as every module
// in the build. So the minimum a 1st-class (owned) module can declare is set by the highest
// `go` directive among the modules it pulls in that the user does NOT control. Pruning those
// modules out of the build is what lets the floor drop — which is exactly the lever the prune
// explorer already pulls, so the floor recomputes live as modules are deselected.

// GoFloor is the dep-imposed minimum Go version for the owned (main + 1st-class) modules under
// a given build/prune view. Version is the highest `go` directive among the non-owned modules
// still in the build; Critical names the modules pinned exactly at it (the reason for the
// floor — prune all of them and it drops to NextVersion). OwnedMax is the highest directive
// your own modules already declare, so the report can show the headroom you can reclaim now
// (lower your `go` line to Version) versus what pruning would buy.
type GoFloor struct {
	Version     string   `json:"version,omitempty"`     // dep-imposed floor (raw go.mod string, e.g. "1.24.0"); "" if no dep declares one
	Critical    []string `json:"critical,omitempty"`    // non-owned modules pinned exactly at Version
	NextVersion string   `json:"nextVersion,omitempty"` // floor after pruning every Critical module; "" if they're the only floor
	OwnedMax    string   `json:"ownedMax,omitempty"`    // highest `go` directive among main + 1st-class modules
}

// goVersionOf returns a module's declared `go` directive, or "" if the module is unknown or
// declares none.
func (g *buildGraph) goVersionOf(mod string) string {
	if m := g.allModules[mod]; m != nil {
		return m.GoVersion
	}
	return ""
}

// CompareGoVersions orders two raw go.mod directive strings ("1.24.0", "1.21", "") the way the
// toolchain does, with "" (no directive) sorting below every real version. Exposed so callers
// like the explorer can sort modules by their declared minimum.
func CompareGoVersions(a, b string) int { return cmpGo(a, b) }

// cmpGo orders two raw go.mod directive strings ("1.24.0", "1.21", "") the way the toolchain
// does, with "" (no directive) sorting below every real version.
func cmpGo(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	}
	return version.Compare("go"+a, "go"+b)
}

// goFloor computes the dep-imposed Go-version floor over inBuild (the modules with at least one
// reachable package in the current view). Owned modules — main and 1st-class, the ones whose
// `go` line the user is trying to minimize — are excluded from the floor itself but tracked in
// OwnedMax for context.
func (g *buildGraph) goFloor(inBuild map[string]bool, c *classification) GoFloor {
	var floor GoFloor
	for mod := range inBuild {
		gv := g.goVersionOf(mod)
		if gv == "" {
			continue
		}
		if owned(c.classOf(mod)) {
			if cmpGo(gv, floor.OwnedMax) > 0 {
				floor.OwnedMax = gv
			}
			continue
		}
		if cmpGo(gv, floor.Version) > 0 {
			floor.Version = gv
		}
	}
	if floor.Version == "" {
		return floor // no dependency declares a directive — nothing forces a floor
	}

	// split the non-owned modules into those pinned at the floor (Critical) and the next floor
	// you'd land on if they were all pruned.
	for mod := range inBuild {
		if owned(c.classOf(mod)) {
			continue
		}
		gv := g.goVersionOf(mod)
		if gv == "" {
			continue
		}
		switch cmpGo(gv, floor.Version) {
		case 0:
			floor.Critical = append(floor.Critical, mod)
		default: // strictly below the floor (nothing is above it by construction)
			if cmpGo(gv, floor.NextVersion) > 0 {
				floor.NextVersion = gv
			}
		}
	}
	sort.Strings(floor.Critical)
	return floor
}
