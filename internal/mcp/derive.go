package mcp

import "github.com/wagoodman/bonsai/internal/bonsai"

// goAction derives the actionable verdict from a GoFloor: if your owned modules declare a higher
// go directive than your deps require, you can drop it now; otherwise the floor is pinned by
// deps you'd have to prune or replace to go lower.
func goAction(f bonsai.GoFloor) GoAction {
	a := GoAction{
		BlockedBy:   f.Critical,
		ThenReaches: f.NextVersion,
	}
	if f.OwnedMax != "" && (f.Version == "" || bonsai.CompareGoVersions(f.OwnedMax, f.Version) > 0) {
		a.CanLowerNow = true
		a.LowerTo = f.Version
	}
	return a
}
