package bonsai

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The diff subject: what did this branch do to my size and my go floor? It builds two source
// states — the working tree (current, possibly dirty) and a baseline commit checked out into a
// throwaway git worktree — runs the same Size + GoFloor analysis on each, and reports the delta.
// JSON is first-class here: a CI bot runs `bonsai diff --output json origin/main` and renders the
// DiffReport into a PR comment.

// DiffReport is the delta between a baseline source state and the current working tree: the net
// size change, the modules added/removed/changed in the build, and any go-floor movement. Every
// field is in JSON — it is the machine contract a CI bot renders into a PR comment.
type DiffReport struct {
	Ref            string `json:"ref"`            // the ref the user named
	BaselineCommit string `json:"baselineCommit"` // resolved baseline (merge-base or ref)
	CurrentCommit  string `json:"currentCommit"`  // HEAD
	Dirty          bool   `json:"dirty"`          // current side had uncommitted changes
	MainModule     string `json:"mainModule"`

	SizeDelta    int64  `json:"sizeDelta"` // current.AccountedSize - baseline (signed)
	BaselineSize uint64 `json:"baselineSize"`
	CurrentSize  uint64 `json:"currentSize"`

	Added   []ModuleDiff `json:"added,omitempty"`   // in current, not baseline
	Removed []ModuleDiff `json:"removed,omitempty"` // in baseline, not current
	Changed []ModuleDiff `json:"changed,omitempty"` // in both, different bytes

	GoFloor GoFloorDiff `json:"goFloor"`
}

// ModuleDiff is one module's contribution to the delta. Bytes is signed: positive for an addition
// or growth, negative for a removal or shrink.
type ModuleDiff struct {
	Module    string `json:"module"`
	Direct    bool   `json:"direct"` // direct vs transitive (the "new transitive dep" signal)
	Bytes     int64  `json:"bytes"`  // signed byte delta this module accounts for
	GoVersion string `json:"goVersion,omitempty"`
}

// GoFloorDiff is the movement in the dep-imposed go floor between baseline and current.
type GoFloorDiff struct {
	Before        string   `json:"before,omitempty"`        // baseline GoFloor.Version
	After         string   `json:"after,omitempty"`         // current GoFloor.Version
	Direction     int      `json:"direction"`               // -1 lowered, 0 unchanged, +1 raised
	NewlyCritical []string `json:"newlyCritical,omitempty"` // deps now pinning the floor that weren't before
}

// Diff builds and analyzes both the current working tree (cfg.Dir) and a baseline source state
// (the merge-base of HEAD and ref, or ref directly), and returns the size + go-floor delta. The
// current side is built in place so uncommitted edits count; the baseline is checked out into a
// throwaway worktree that never disturbs the caller's tree, index, or branch.
func Diff(cfg Config, ref string) (*DiffReport, error) {
	if cfg.Binary != "" {
		// both sides must be built from source for matching linker reachability.
		return nil, fmt.Errorf("--binary cannot be used with diff: both sides are built from source")
	}
	dir := cfg.Dir
	if dir == "" {
		dir = "."
	}

	cur, err := Resolve(cfg)
	if err != nil {
		return nil, fmt.Errorf("building current working tree: %w", err)
	}
	defer cur.Close()

	curCommit, clean := gitState(dir)

	base, baseCommit, cleanup, err := resolveBaseline(cfg, dir, ref)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	rep := diffResolved(cur, base)
	rep.Ref = ref
	rep.BaselineCommit = baseCommit
	rep.CurrentCommit = curCommit
	rep.Dirty = !clean
	return &rep, nil
}

// resolveBaseline materializes the baseline commit in a worktree and resolves it against the same
// module-relative subpath the user invoked from. Returns the baseline handle, the resolved commit,
// and a cleanup that closes the handle and removes the worktree.
func resolveBaseline(cfg Config, dir, ref string) (*Resolved, string, func(), error) {
	root, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", nil, fmt.Errorf("diff requires a git repository: %w", err)
	}
	root = strings.TrimSpace(root)

	commit := baselineCommit(dir, ref)

	wt, wtCleanup, err := checkoutWorktree(root, commit)
	if err != nil {
		return nil, "", nil, err
	}

	// build the same subdir on the baseline side (e.g. invoked from ./cmd/app → build that subpath
	// inside the worktree).
	abs, err := filepath.Abs(dir)
	if err != nil {
		wtCleanup()
		return nil, "", nil, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		wtCleanup()
		return nil, "", nil, err
	}

	baseCfg := cfg
	baseCfg.Dir = filepath.Join(wt, rel)
	base, err := Resolve(baseCfg)
	if err != nil {
		wtCleanup()
		return nil, "", nil, fmt.Errorf("building baseline %s: %w", commit, err)
	}
	return base, commit, func() { base.Close(); wtCleanup() }, nil
}

// baselineCommit resolves the ref the diff compares against. Default semantics: the merge-base of
// HEAD and ref, i.e. where this branch diverged, so the delta reads as "what this branch added".
// If there's no common ancestor (a tag/sha with no branch relationship), fall back to ref directly.
func baselineCommit(dir, ref string) string {
	out, err := gitOutput(dir, "merge-base", "HEAD", ref)
	if err != nil {
		return ref
	}
	return strings.TrimSpace(out)
}

// checkoutWorktree materializes commit into a temp worktree and returns its path plus a cleanup
// that removes the worktree (and its admin entry). It never disturbs the caller's working tree.
func checkoutWorktree(repoDir, commit string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "bonsai-baseline-*")
	if err != nil {
		return "", nil, err
	}
	// --detach: don't create a branch; just park HEAD at the commit in an isolated tree.
	if _, err := gitOutput(repoDir, "worktree", "add", "--detach", tmp, commit); err != nil {
		os.RemoveAll(tmp)
		return "", nil, fmt.Errorf("checking out baseline %s: %w", commit, err)
	}
	cleanup := func() {
		// remove via git so its bookkeeping is pruned too, then nuke the dir.
		_, _ = gitOutput(repoDir, "worktree", "remove", "--force", tmp)
		os.RemoveAll(tmp)
	}
	return tmp, cleanup, nil
}

// diffResolved compares two resolved analyses: the net accounted-size delta, the per-module
// added/removed/changed split (signed bytes), and the go-floor movement. Pure comparison — no new
// analysis.
func diffResolved(cur, base *Resolved) DiffReport {
	return buildDiff(cur.Size(), base.Size(), cur.GoFloor(), base.GoFloor())
}

// buildDiff is the pure set-diff over two size reports and two go floors. Split out from
// diffResolved so the comparison logic is testable without building anything.
func buildDiff(curSize, baseSize SizeReport, curFloor, baseFloor GoFloor) DiffReport {
	rep := DiffReport{
		MainModule:   curSize.MainModule,
		BaselineSize: baseSize.AccountedSize,
		CurrentSize:  curSize.AccountedSize,
		SizeDelta:    int64(curSize.AccountedSize) - int64(baseSize.AccountedSize),
	}

	// the module sections report dependency churn; the main module is always present on both
	// sides and its own-code delta is already folded into the net size headline, so drop it (and
	// avoid mislabeling it direct/transitive — it's neither).
	curMods := indexModules(curSize.Modules)
	baseMods := indexModules(baseSize.Modules)
	delete(curMods, curSize.MainModule)
	delete(baseMods, baseSize.MainModule)

	for mod, cm := range curMods {
		bm, ok := baseMods[mod]
		switch {
		case !ok:
			rep.Added = append(rep.Added, moduleDiff(cm, int64(cm.Size)))
		case cm.Size != bm.Size:
			rep.Changed = append(rep.Changed, moduleDiff(cm, int64(cm.Size)-int64(bm.Size)))
		}
	}
	for mod, bm := range baseMods {
		if _, ok := curMods[mod]; !ok {
			rep.Removed = append(rep.Removed, moduleDiff(bm, -int64(bm.Size)))
		}
	}

	sortByAbsBytes(rep.Added)
	sortByAbsBytes(rep.Removed)
	sortByAbsBytes(rep.Changed)

	rep.GoFloor = diffGoFloor(baseFloor, curFloor)
	return rep
}

func indexModules(mods []ModuleSize) map[string]ModuleSize {
	m := make(map[string]ModuleSize, len(mods))
	for _, ms := range mods {
		m[ms.Module] = ms
	}
	return m
}

func moduleDiff(m ModuleSize, bytes int64) ModuleDiff {
	return ModuleDiff{Module: m.Module, Direct: m.Direct, Bytes: bytes, GoVersion: m.GoVersion}
}

func sortByAbsBytes(d []ModuleDiff) {
	// module name is the tiebreaker: map iteration order is random, so without it equal-delta rows
	// flap run-to-run and the CI PR comment churns.
	sort.Slice(d, func(i, j int) bool {
		if ai, aj := abs64(d[i].Bytes), abs64(d[j].Bytes); ai != aj {
			return ai > aj
		}
		return d[i].Module < d[j].Module
	})
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// diffGoFloor reports whether the dep-imposed floor rose, fell, or held, and which deps now pin it
// that didn't before.
func diffGoFloor(before, after GoFloor) GoFloorDiff {
	d := GoFloorDiff{
		Before:    before.Version,
		After:     after.Version,
		Direction: CompareGoVersions(after.Version, before.Version),
	}
	was := make(map[string]bool, len(before.Critical))
	for _, m := range before.Critical {
		was[m] = true
	}
	for _, m := range after.Critical {
		if !was[m] {
			d.NewlyCritical = append(d.NewlyCritical, m)
		}
	}
	sort.Strings(d.NewlyCritical)
	return d
}
