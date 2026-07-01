// Package integration drives the real bonsai pipeline end to end: fixture Go workspaces
// (testdata/*, local-replace modules so they're offline and version-stable) are compiled by the
// actual go toolchain, the linker's -dumpdep reachability is parsed, and the resulting graph flows
// through classification, the dominator engine, the reach-index what-if engine, Shapley blame, the
// go-floor rollup, the matrix, and the diff. The synthetic-graph unit tests prove those algorithms
// in isolation; these prove the seam between the real toolchain output and those algorithms.
//
// Assertions check structure and relations, never absolute byte counts (those drift across go
// versions). The one numeric invariant -- the dominator and reach-index engines agreeing on
// exclusive bytes -- is the real-graph analogue of the synthetic dominator==treeShake oracle.
//
// This file holds the shared fixtures and helpers; each command/concern has its own _test.go.
package integration

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// the default fixture (testdata/workspace), every byte authored here so the answers are known:
//
//	app ──> liba ──> libb        (libb reached only via liba)
//	 │       └─────> libc        (libc shared: reached via liba AND libs)
//	 └────> libs ──> libc
//
// go directives: app 1.25, liba/libs/libb 1.21, libc 1.23 -> libc pins the dep floor at 1.23.
const (
	app  = "example.com/e2e/app"
	liba = "example.com/e2e/liba"
	libs = "example.com/e2e/libs"
	libb = "example.com/e2e/libb"
	libc = "example.com/e2e/libc"
	libz = "example.com/e2e/libz" // imported in source but dead-code-eliminated (testdata/workspace)
)

// the deep fixture (testdata/deep) stresses the shared-dependency handling past one level:
//
//	app ──> a ─┐
//	 ├───> b ──┼──> s ──> t
//	 └───> c ──┘          ^
//	       └──────────────┘   (c imports t directly too)
//
// so s is shared three ways (a, b, c) and t is shared two ways (s and c) one level deeper.
// a and b are byte-identical modules in symmetric positions, which makes their Shapley blame
// provably equal without depending on absolute sizes. go directives: a and b 1.24 (tied at the
// floor), s 1.23, c and t 1.21, app 1.25.
const (
	deepApp = "example.com/deep/app"
	deepA   = "example.com/deep/a"
	deepB   = "example.com/deep/b"
	deepC   = "example.com/deep/c"
	deepS   = "example.com/deep/s"
	deepT   = "example.com/deep/t"
)

func fixtureDir(t *testing.T) string { return appDir(t, "workspace") }
func deepDir(t *testing.T) string    { return appDir(t, "deep") }

func appDir(t *testing.T, fixture string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", fixture, "app"))
	require.NoError(t, err)
	return dir
}

// requireLong skips build-heavy tests under `go test -short`.
func requireLong(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("compiles a fixture binary; skipped under -short")
	}
}

// newSession compiles+analyzes the default fixture once for a given controlled set.
func newSession(t *testing.T, controlled ...string) *bonsai.Session {
	t.Helper()
	return newSessionAt(t, fixtureDir(t), controlled...)
}

// newSessionAt compiles+analyzes the fixture rooted at dir.
func newSessionAt(t *testing.T, dir string, controlled ...string) *bonsai.Session {
	t.Helper()
	requireLong(t)
	s, err := bonsai.NewSession(bonsai.Config{Dir: dir, Controlled: controlled})
	require.NoError(t, err)
	return s
}

// buildBinary compiles the fixture at dir to a throwaway executable and returns its path, for
// exercising bonsai's prebuilt (--binary) mode against a real artifact.
func buildBinary(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "app")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building fixture binary: %s", out)
	return bin
}

func modulesByPath(s *bonsai.Session) map[string]bonsai.Module {
	out := map[string]bonsai.Module{}
	for _, m := range s.Modules() {
		out[m.Module] = m
	}
	return out
}
