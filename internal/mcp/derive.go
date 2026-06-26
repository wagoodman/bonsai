package mcp

import "github.com/wagoodman/bonsai/internal/bonsai"

// effort thresholds for the size-target verdict, measured in first-party import sites — the
// count of statements an agent would edit to sever the dependency.
const (
	quickWinSites   = 4  // few touch points: a near-mechanical removal or small rewrite
	highEffortSites = 15 // deeply wired in: many statements across many packages to unpick
)

// sizeVerdict labels a prune candidate by effort-adjusted value, so an agent's first-pass sort
// is cheap. It is deliberately coarse — the raw freed/potential bytes and coupling counts carry
// the nuance, and locate_cuts gives the precise rewrite scope. "sharedOnly" means pruning it
// alone frees nothing (co-holders must go too); the rest scale with first-party coupling.
func sizeVerdict(c Candidate) string {
	if c.FreedBytes == 0 {
		return "sharedOnly"
	}
	switch {
	case c.ImportSites <= quickWinSites && c.ImportingPackages <= quickWinSites:
		return "quickWin"
	case c.ImportSites >= highEffortSites:
		return "highEffort"
	default:
		return "moderate"
	}
}

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
