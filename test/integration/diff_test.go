package integration

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// the diff fixture ships two full snapshots (testdata/diff/base and .../head) that occupy the same
// relative layout -- app/ is the main module in both -- because resolveBaseline rebuilds the same
// module-relative subpath inside a throwaway git worktree, so the two states must be one tree
// mutated in place, not two different dirs. The authored delta: libold (floor 1.24) is dropped,
// libnew (floor 1.25) is added, and libc gains code, so size grows and the go floor rises.
const (
	diffApp    = "example.com/diff/app"
	diffLibA   = "example.com/diff/liba"
	diffLibC   = "example.com/diff/libc"
	diffLibOld = "example.com/diff/libold"
	diffLibNew = "example.com/diff/libnew"
)

// copyTree recursively copies src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	require.NoError(t, err)
}

// gitCmd runs a git command in dir, failing the test on error and returning trimmed stdout+stderr.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// diffRepo builds a self-contained temp git repo: the base snapshot committed first (returned as
// the baseline ref), then the head snapshot committed as HEAD, laid over the same app/ path. Git
// identity + signing are configured locally so the test never depends on the developer's global
// git config. Returns the repo root and the baseline commit.
func diffRepo(t *testing.T) (repo, baseline string) {
	t.Helper()
	requireLong(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo = t.TempDir()
	gitCmd(t, repo, "init", "-q")
	gitCmd(t, repo, "config", "user.email", "e2e@bonsai.test")
	gitCmd(t, repo, "config", "user.name", "bonsai e2e")
	gitCmd(t, repo, "config", "commit.gpgsign", "false")

	snap := func(name string) string {
		d, err := filepath.Abs(filepath.Join("testdata", "diff", name))
		require.NoError(t, err)
		return d
	}

	copyTree(t, snap("base"), repo)
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "base")
	baseline = gitCmd(t, repo, "rev-parse", "HEAD")

	// replace the working tree with the head snapshot (keeping .git) and commit it, so HEAD carries
	// the head state at the same app/ path the baseline used.
	entries, err := os.ReadDir(repo)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		require.NoError(t, os.RemoveAll(filepath.Join(repo, e.Name())))
	}
	copyTree(t, snap("head"), repo)
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "head")
	return repo, baseline
}

func moduleDiffByPath(ms []bonsai.ModuleDiff) map[string]bonsai.ModuleDiff {
	out := map[string]bonsai.ModuleDiff{}
	for _, m := range ms {
		out[m.Module] = m
	}
	return out
}

// TestEndToEndDiff drives the diff command end to end against a real two-commit git repo: it builds
// both the working tree and a baseline worktree from source and reports the delta. Assertions cover
// the added/removed/changed split, net size direction, and go-floor movement -- structure only,
// never byte magnitudes.
func TestEndToEndDiff(t *testing.T) {
	repo, baseline := diffRepo(t)

	rep, err := bonsai.Diff(bonsai.Config{Dir: filepath.Join(repo, "app")}, baseline)
	require.NoError(t, err)

	assert.Equal(t, baseline, rep.BaselineCommit)
	assert.False(t, rep.Dirty, "HEAD equals the working tree right after the head commit")
	assert.Equal(t, diffApp, rep.MainModule)
	assert.Positive(t, rep.SizeDelta, "head adds libnew + code and drops libold, so size grows")

	added := moduleDiffByPath(rep.Added)
	require.Contains(t, added, diffLibNew, "libnew is new in head")
	assert.True(t, added[diffLibNew].Direct, "app imports libnew directly")
	assert.Positive(t, added[diffLibNew].Bytes)

	removed := moduleDiffByPath(rep.Removed)
	require.Contains(t, removed, diffLibOld, "libold was dropped in head")
	assert.Negative(t, removed[diffLibOld].Bytes)

	changed := moduleDiffByPath(rep.Changed)
	assert.Contains(t, changed, diffLibC, "libc gained code between the two states")
	assert.NotContains(t, changed, diffLibA, "liba is untouched; its size must not be smeared by libc's growth")

	// the go floor rose from libold's 1.24 to libnew's 1.25.
	assert.Equal(t, "1.24", rep.GoFloor.Before)
	assert.Equal(t, "1.25", rep.GoFloor.After)
	assert.Equal(t, 1, rep.GoFloor.Direction, "+1 == floor raised")
	assert.Contains(t, rep.GoFloor.NewlyCritical, diffLibNew)
}

// TestEndToEndDiffDirty confirms the working-tree cleanliness signal: an uncommitted edit flips
// Dirty, which a CI bot uses to warn that the "current" side isn't a committed state.
func TestEndToEndDiffDirty(t *testing.T) {
	repo, baseline := diffRepo(t)

	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "libc", "dirty.go"),
		[]byte("package libc\n\nvar dirtyMarker = 1\n"), 0o644))

	rep, err := bonsai.Diff(bonsai.Config{Dir: filepath.Join(repo, "app")}, baseline)
	require.NoError(t, err)
	assert.True(t, rep.Dirty, "an uncommitted working-tree edit must set Dirty")
}
