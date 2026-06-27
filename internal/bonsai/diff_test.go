package bonsai

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildDiff(t *testing.T) {
	base := SizeReport{
		MainModule:    "example.com/app",
		AccountedSize: 1000,
		Modules: []ModuleSize{
			{Module: "example.com/app", Size: 400}, // main: excluded from sections
			{Module: "example.com/keep", Size: 300, Direct: true, GoVersion: "1.21"},
			{Module: "example.com/grow", Size: 200, Direct: true},
			{Module: "example.com/gone", Size: 100, Direct: false}, // removed
		},
	}
	cur := SizeReport{
		MainModule:    "example.com/app",
		AccountedSize: 1450,
		Modules: []ModuleSize{
			{Module: "example.com/app", Size: 500}, // main grew but is excluded
			{Module: "example.com/keep", Size: 300, Direct: true, GoVersion: "1.21"},
			{Module: "example.com/grow", Size: 350, Direct: true}, // changed +150
			{Module: "example.com/new", Size: 250, Direct: false}, // added, transitive
			{Module: "example.com/newdirect", Size: 50, Direct: true},
		},
	}
	baseFloor := GoFloor{Version: "1.21", Critical: []string{"example.com/gone"}}
	curFloor := GoFloor{Version: "1.24.0", Critical: []string{"example.com/new"}}

	got := buildDiff(cur, base, curFloor, baseFloor)

	assert.Equal(t, int64(450), got.SizeDelta)
	assert.Equal(t, uint64(1000), got.BaselineSize)
	assert.Equal(t, uint64(1450), got.CurrentSize)
	assert.Equal(t, "example.com/app", got.MainModule)

	// added: sorted by abs bytes desc, main excluded.
	require.Len(t, got.Added, 2)
	assert.Equal(t, ModuleDiff{Module: "example.com/new", Direct: false, Bytes: 250}, got.Added[0])
	assert.Equal(t, ModuleDiff{Module: "example.com/newdirect", Direct: true, Bytes: 50}, got.Added[1])

	// removed: signed negative.
	require.Len(t, got.Removed, 1)
	assert.Equal(t, ModuleDiff{Module: "example.com/gone", Direct: false, Bytes: -100}, got.Removed[0])

	// changed: only modules in both with different bytes (keep is unchanged → absent; main excluded).
	require.Len(t, got.Changed, 1)
	assert.Equal(t, ModuleDiff{Module: "example.com/grow", Direct: true, Bytes: 150}, got.Changed[0])

	// floor raised, new dep pins it.
	assert.Equal(t, "1.21", got.GoFloor.Before)
	assert.Equal(t, "1.24.0", got.GoFloor.After)
	assert.Equal(t, 1, got.GoFloor.Direction)
	assert.Equal(t, []string{"example.com/new"}, got.GoFloor.NewlyCritical)
}

func TestBuildDiff_NoChange(t *testing.T) {
	r := SizeReport{
		MainModule:    "example.com/app",
		AccountedSize: 1000,
		Modules:       []ModuleSize{{Module: "example.com/app", Size: 1000}},
	}
	floor := GoFloor{Version: "1.21"}

	got := buildDiff(r, r, floor, floor)

	assert.Zero(t, got.SizeDelta)
	assert.Empty(t, got.Added)
	assert.Empty(t, got.Removed)
	assert.Empty(t, got.Changed)
	assert.Equal(t, 0, got.GoFloor.Direction)
}

// equal abs-byte deltas must order by module name, not random map-walk order, or the CI PR comment
// flaps run-to-run. Run a few times since the bug only surfaces on some map orderings.
func TestBuildDiff_TieOrderDeterministic(t *testing.T) {
	base := SizeReport{MainModule: "m", Modules: []ModuleSize{{Module: "m"}}}
	cur := SizeReport{
		MainModule: "m",
		Modules: []ModuleSize{
			{Module: "m"},
			{Module: "example.com/zzz", Size: 100},
			{Module: "example.com/aaa", Size: 100}, // same bytes as zzz → name breaks the tie
			{Module: "example.com/mmm", Size: 100},
		},
	}
	want := []string{"example.com/aaa", "example.com/mmm", "example.com/zzz"}
	for range 5 {
		got := buildDiff(cur, base, GoFloor{}, GoFloor{})
		require.Len(t, got.Added, 3)
		names := []string{got.Added[0].Module, got.Added[1].Module, got.Added[2].Module}
		assert.Equal(t, want, names)
	}
}

// SizeDelta can be 0 while individual modules churn and offset each other; the per-module sections
// must still report the movement (regression: the report's "no change" guard relied on this).
func TestBuildDiff_ZeroNetButChanged(t *testing.T) {
	base := SizeReport{
		MainModule:    "m",
		AccountedSize: 1000,
		Modules: []ModuleSize{
			{Module: "m", Size: 600},
			{Module: "example.com/a", Size: 200, Direct: true},
			{Module: "example.com/b", Size: 200, Direct: true},
		},
	}
	cur := SizeReport{
		MainModule:    "m",
		AccountedSize: 1000, // net unchanged
		Modules: []ModuleSize{
			{Module: "m", Size: 600},
			{Module: "example.com/a", Size: 300, Direct: true}, // +100
			{Module: "example.com/b", Size: 100, Direct: true}, // -100
		},
	}
	got := buildDiff(cur, base, GoFloor{}, GoFloor{})
	assert.Zero(t, got.SizeDelta)
	assert.Empty(t, got.Added)
	assert.Empty(t, got.Removed)
	require.Len(t, got.Changed, 2)
}

func TestBuildDiff_EmptySides(t *testing.T) {
	mods := SizeReport{
		MainModule: "m",
		Modules: []ModuleSize{{Module: "m"}, {Module: "example.com/dep", Size: 100}},
	}
	empty := SizeReport{MainModule: "m", Modules: []ModuleSize{{Module: "m"}}}

	// empty baseline → the dep is added.
	got := buildDiff(mods, empty, GoFloor{}, GoFloor{})
	require.Len(t, got.Added, 1)
	assert.Empty(t, got.Removed)

	// empty current → the dep is removed.
	got = buildDiff(empty, mods, GoFloor{}, GoFloor{})
	assert.Empty(t, got.Added)
	require.Len(t, got.Removed, 1)
	assert.Equal(t, int64(-100), got.Removed[0].Bytes)
}

func TestBuildDiff_FloorLowered(t *testing.T) {
	r := SizeReport{MainModule: "m", Modules: []ModuleSize{{Module: "m"}}}
	got := buildDiff(r, r, GoFloor{Version: "1.21"}, GoFloor{Version: "1.24.0"})
	assert.Equal(t, "1.24.0", got.GoFloor.Before)
	assert.Equal(t, "1.21", got.GoFloor.After)
	assert.Equal(t, -1, got.GoFloor.Direction)
}

// the floor version holds but a different dep now pins it — NewlyCritical must still report it.
func TestBuildDiff_FloorUnchangedButChurned(t *testing.T) {
	r := SizeReport{MainModule: "m", Modules: []ModuleSize{{Module: "m"}}}
	before := GoFloor{Version: "1.21", Critical: []string{"example.com/old"}}
	after := GoFloor{Version: "1.21", Critical: []string{"example.com/new"}}
	got := buildDiff(r, r, after, before)
	assert.Equal(t, 0, got.GoFloor.Direction)
	assert.Equal(t, []string{"example.com/new"}, got.GoFloor.NewlyCritical)
}

// TestResolveBaseline_Worktree is the one integration test: a throwaway git repo with two commits
// whose source differs. It asserts the baseline builds the older state and cleanup removes the
// worktree. Guarded on git + go being present (they are, in CI and dev).
func TestResolveBaseline_Worktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := gitOutput(repo, args...)
		require.NoError(t, err, "git %v", args)
		return out
	}
	// a minimal buildable module: v1 imports nothing extra, v2 unused but distinct source.
	writeRepoFile(t, repo, "go.mod", "module example.com/diffrepo\n\ngo 1.21\n")
	writeRepoFile(t, repo, "main.go", "package main\n\nfunc main() { println(\"v1\") }\n")
	run("init")
	run("config", "user.email", "t@t.test")
	run("config", "user.name", "t")
	run("add", ".")
	run("commit", "-m", "v1")
	v1 := strings.TrimSpace(run("rev-parse", "HEAD"))

	writeRepoFile(t, repo, "main.go", "package main\n\nfunc main() { println(\"v2 is bigger by a lot\") }\n")
	run("add", ".")
	run("commit", "-m", "v2")

	base, commit, cleanup, err := resolveBaseline(Config{Dir: repo}, repo, v1)
	require.NoError(t, err)
	assert.Equal(t, v1, commit)
	assert.NotZero(t, base.Size().AccountedSize)
	cleanup()

	// the worktree is gone after cleanup.
	list := run("worktree", "list")
	assert.NotContains(t, list, "bonsai-baseline")
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}
