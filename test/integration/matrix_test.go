package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// the platform fixture (testdata/platform) gates dependencies per build cell so the matrix rollup
// has something real to aggregate: liblinux is imported only on GOOS=linux, libwin only on
// GOOS=windows, libextra only under `-tags extra`, and libcommon always. go directives are set so
// each gated dep drives a different floor (liblinux 1.24, libwin 1.23, libextra 1.22, common 1.21),
// which is what makes the worst-case-across-cells floor meaningful. Everything is pure Go, so the
// cross-`go list` cells are hermetic with no C toolchain.
const (
	platCommon = "example.com/plat/libcommon"
	platLinux  = "example.com/plat/liblinux"
	platWin    = "example.com/plat/libwin"
	platExtra  = "example.com/plat/libextra"
)

func platformDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "platform", "app"))
	require.NoError(t, err)
	return dir
}

func findCell(t *testing.T, rep bonsai.MatrixReport, label string) bonsai.CellResult {
	t.Helper()
	for _, c := range rep.Cells {
		if c.Label == label {
			return c
		}
	}
	t.Fatalf("cell %q not found in matrix", label)
	return bonsai.CellResult{}
}

func findMatrixMod(t *testing.T, rep bonsai.MatrixReport, module string) bonsai.MatrixModule {
	t.Helper()
	for _, m := range rep.Modules {
		if m.Module == module {
			return m
		}
	}
	t.Fatalf("module %q not found in matrix", module)
	return bonsai.MatrixModule{}
}

// TestEndToEndMatrixFloor exercises the default (build-free, cross-`go list`) matrix across three
// real platforms. The payoff is the worst-case floor and the universal-vs-platform-specific split,
// neither of which any single-cell analysis can produce.
func TestEndToEndMatrixFloor(t *testing.T) {
	requireLong(t)
	cells := []bonsai.Platform{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "amd64"},
		{GOOS: "darwin", GOARCH: "arm64"},
	}
	rep, err := bonsai.Matrix(bonsai.Config{Dir: platformDir(t)}, cells, false, 3)
	require.NoError(t, err)
	require.Equal(t, 3, rep.SuccessfulCells(), "every cross-list cell must succeed (failures hide in CellResult.Err)")

	// worst-case floor: liblinux (1.24) is the constraint across the whole matrix, and dropping it
	// leaves windows' libwin (1.23) as the next floor -- a rollup no single cell shows.
	assert.Equal(t, "1.24", rep.WorstGo.Version)
	assert.Contains(t, rep.WorstGo.Critical, platLinux)
	assert.Equal(t, "1.23", rep.WorstGo.NextVersion)

	// per-cell floors diverge by platform.
	assert.Equal(t, "1.24", findCell(t, rep, "linux/amd64").Floor.Version)
	assert.Equal(t, "1.23", findCell(t, rep, "windows/amd64").Floor.Version)
	assert.Equal(t, "1.21", findCell(t, rep, "darwin/arm64").Floor.Version)

	// universal vs platform-specific: libcommon is everywhere; the OS-gated deps are not.
	assert.Contains(t, rep.Universal, platCommon)
	assert.NotContains(t, rep.Universal, platLinux)

	lin := findMatrixMod(t, rep, platLinux)
	assert.False(t, lin.Universal)
	assert.ElementsMatch(t, []string{"linux/amd64"}, lin.InCells, "liblinux is a linux-only dep")

	assert.ElementsMatch(t, []string{"windows/amd64"}, findMatrixMod(t, rep, platWin).InCells)

	common := findMatrixMod(t, rep, platCommon)
	assert.True(t, common.Universal)
	assert.ElementsMatch(t, []string{"linux/amd64", "windows/amd64", "darwin/arm64"}, common.InCells)
}

// TestEndToEndMatrixBuildTag isolates build-tag gating: both cells use the same GOOS/GOARCH, so the
// only difference is `-tags extra`. That proves the tag flows through to `go list -tags` and the
// cell label, and that a tag-gated dep can move the worst-case floor.
func TestEndToEndMatrixBuildTag(t *testing.T) {
	requireLong(t)
	cells := []bonsai.Platform{
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "darwin", GOARCH: "arm64", Tags: []string{"extra"}},
	}
	rep, err := bonsai.Matrix(bonsai.Config{Dir: platformDir(t)}, cells, false, 2)
	require.NoError(t, err)
	require.Equal(t, 2, rep.SuccessfulCells())

	// libextra appears only in the +extra cell...
	extra := findMatrixMod(t, rep, platExtra)
	assert.False(t, extra.Universal)
	assert.ElementsMatch(t, []string{"darwin/arm64+extra"}, extra.InCells)

	// ...and it raises the worst-case floor to its 1.22 directive (the plain darwin cell is 1.21).
	assert.Equal(t, "1.22", rep.WorstGo.Version)
	assert.Contains(t, rep.WorstGo.Critical, platExtra)
}
