package bonsai

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlatformLabel(t *testing.T) {
	tests := []struct {
		name string
		p    Platform
		want string
	}{
		{name: "os/arch", p: Platform{GOOS: "linux", GOARCH: "amd64"}, want: "linux/amd64"},
		{name: "tags sorted + deduped", p: Platform{GOOS: "linux", GOARCH: "amd64", Tags: []string{"netgo", "cgo", "netgo"}}, want: "linux/amd64+cgo,netgo"},
		{name: "zero value is host", p: Platform{}, want: "host/host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.p.Label())
		})
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "simple flags", in: "-trimpath -v", want: []string{"-trimpath", "-v"}},
		{name: "double-quoted ldflags stays one arg", in: `-trimpath -ldflags="-s -w"`, want: []string{"-trimpath", "-ldflags=-s -w"}},
		{name: "single-quoted value", in: `-ldflags='-X main.v=1'`, want: []string{"-ldflags=-X main.v=1"}},
		{name: "collapses extra whitespace", in: "a   b\tc", want: []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitArgs(tt.in))
		})
	}
}

// TestPlatformCacheKey is the cache-collision guard: distinct cells (GOOS, tags, env, args) must
// hash to distinct keys so matrix cells on the same commit don't collide on one entry.
func TestPlatformCacheKey(t *testing.T) {
	base := platformCacheKey("commit1", "target", Platform{GOOS: "linux", GOARCH: "amd64"}, BuildSettings{})

	assert.Equal(t, base, platformCacheKey("commit1", "target", Platform{GOOS: "linux", GOARCH: "amd64"}, BuildSettings{}),
		"identical inputs hash identically")

	distinct := map[string]string{
		"different goos":   platformCacheKey("commit1", "target", Platform{GOOS: "windows", GOARCH: "amd64"}, BuildSettings{}),
		"different tags":   platformCacheKey("commit1", "target", Platform{GOOS: "linux", GOARCH: "amd64", Tags: []string{"netgo"}}, BuildSettings{}),
		"different env":    platformCacheKey("commit1", "target", Platform{GOOS: "linux", GOARCH: "amd64"}, BuildSettings{Env: map[string]string{"CGO_ENABLED": "0"}}),
		"different args":   platformCacheKey("commit1", "target", Platform{GOOS: "linux", GOARCH: "amd64"}, BuildSettings{Args: "-trimpath"}),
		"different commit": platformCacheKey("commit2", "target", Platform{GOOS: "linux", GOARCH: "amd64"}, BuildSettings{}),
	}
	for name, key := range distinct {
		assert.NotEqual(t, base, key, name+" must change the key")
	}
}

func TestAggregateCells(t *testing.T) {
	// linux: floor 1.21 (bar pins it). windows: floor 1.23 (foo pins it, drops to 1.21). foo is
	// windows-only; bar is in both. So the matrix floor is 1.23, pinned by windows alone.
	cells := []cellData{
		{
			label: "linux/amd64",
			floor: GoFloor{Version: "1.21", Critical: []string{"bar"}, OwnedMax: "1.20"},
			modules: []moduleInfo{
				{module: "bar", goVersion: "1.21"},
			},
		},
		{
			label: "windows/amd64",
			floor: GoFloor{Version: "1.23", Critical: []string{"foo"}, NextVersion: "1.21", OwnedMax: "1.22"},
			modules: []moduleInfo{
				{module: "foo", goVersion: "1.23"},
				{module: "bar", goVersion: "1.21"},
			},
		},
	}

	rep := aggregateCells(cells, false)

	assert.Equal(t, "1.23", rep.WorstGo.Version, "worst floor is the MAX over cells")
	assert.Equal(t, []string{"foo"}, rep.WorstGo.Critical, "only the tied cell's critical deps pin the worst floor")
	assert.Equal(t, "1.21", rep.WorstGo.NextVersion, "pruning foo drops windows to 1.21, matching linux")
	assert.Equal(t, "1.22", rep.WorstGo.OwnedMax, "OwnedMax is the MAX over cells")
	assert.Equal(t, []string{"bar"}, rep.Universal, "bar is in every cell")

	byMod := map[string]MatrixModule{}
	for _, m := range rep.Modules {
		byMod[m.Module] = m
	}
	assert.False(t, byMod["foo"].Universal)
	assert.Equal(t, []string{"windows/amd64"}, byMod["foo"].InCells)
	assert.True(t, byMod["bar"].Universal)
	assert.Equal(t, []string{"linux/amd64", "windows/amd64"}, byMod["bar"].InCells)
	assert.Len(t, rep.Cells, 2)
}

// TestAggregateCellsErrorTolerance: a failed cell is recorded but excluded from the rollups, and
// universality is judged only over the cells that succeeded.
func TestAggregateCellsErrorTolerance(t *testing.T) {
	cells := []cellData{
		{label: "linux/amd64", floor: GoFloor{Version: "1.21"}, modules: []moduleInfo{{module: "bar", goVersion: "1.21"}}},
		{label: "cgo/cell", err: fmt.Errorf("no cross toolchain")},
		{label: "darwin/arm64", floor: GoFloor{Version: "1.21"}, modules: []moduleInfo{{module: "bar", goVersion: "1.21"}}},
	}

	rep := aggregateCells(cells, false)

	assert.Equal(t, "1.21", rep.WorstGo.Version, "the failed cell doesn't sink the floor")
	assert.Equal(t, []string{"bar"}, rep.Universal, "bar is universal across the two successful cells")

	var failed CellResult
	for _, c := range rep.Cells {
		if c.Label == "cgo/cell" {
			failed = c
		}
	}
	assert.Contains(t, failed.Err, "no cross toolchain", "the failure is reported on its cell")
}

// TestAggregateCellsAllFailed: when every cell errors, the rollups are empty and SuccessfulCells
// reports 0 so the caller can flag it instead of printing a misleading "no floor" success.
func TestAggregateCellsAllFailed(t *testing.T) {
	cells := []cellData{
		{label: "linux/amd64", err: fmt.Errorf("boom")},
		{label: "windows/amd64", err: fmt.Errorf("boom")},
	}

	rep := aggregateCells(cells, false)

	assert.Equal(t, 0, rep.SuccessfulCells())
	assert.Equal(t, "", rep.WorstGo.Version, "no successful cell means no floor")
	assert.Empty(t, rep.Universal)
	assert.Len(t, rep.Cells, 2, "failed cells are still recorded")
}

// TestWorstFloorThreeWayTie: three cells tied at the max floor contribute the UNION of their
// critical sets, and NextVersion is the MAX of where the tied cells drop after pruning.
func TestWorstFloorThreeWayTie(t *testing.T) {
	cells := []cellData{
		{label: "a", floor: GoFloor{Version: "1.23", Critical: []string{"foo"}, NextVersion: "1.21"}},
		{label: "b", floor: GoFloor{Version: "1.23", Critical: []string{"bar"}, NextVersion: "1.22"}},
		{label: "c", floor: GoFloor{Version: "1.20", Critical: []string{"baz"}, NextVersion: "1.19"}},
	}

	f := worstFloor(cells)

	assert.Equal(t, "1.23", f.Version)
	assert.Equal(t, []string{"bar", "foo"}, f.Critical, "union of the tied cells' critical sets, sorted")
	assert.Equal(t, "1.22", f.NextVersion, "MAX over: tied cells' NextVersion and untied cells' Version")
}

// TestMatrixModulesOrdering pins the report-driving sort: platform-specific before universal,
// then highest go directive, then module name.
func TestMatrixModulesOrdering(t *testing.T) {
	// two successful cells. uni is in both (universal); only-a and only-b are platform-specific.
	cells := []cellData{
		{label: "a", modules: []moduleInfo{{module: "uni", goVersion: "1.21"}, {module: "only-a", goVersion: "1.20"}, {module: "z-spec", goVersion: "1.23"}}},
		{label: "b", modules: []moduleInfo{{module: "uni", goVersion: "1.21"}, {module: "only-b", goVersion: "1.23"}}},
	}

	mods := matrixModules(cells, false)

	got := make([]string, len(mods))
	for i, m := range mods {
		got[i] = m.Module
	}
	// platform-specific first (by go desc then name: z-spec & only-b both 1.23 -> name; only-a last), then universal.
	assert.Equal(t, []string{"only-b", "z-spec", "only-a", "uni"}, got)
}

// TestAggregateCellsSize: per-cell size flows into SizeByCell and the max-size rollup.
func TestAggregateCellsSize(t *testing.T) {
	cells := []cellData{
		{
			label:   "linux/amd64",
			floor:   GoFloor{Version: "1.21"},
			modules: []moduleInfo{{module: "foo"}},
			size:    &SizeReport{Modules: []ModuleSize{{Module: "foo", Size: 1000}}},
		},
		{
			label:   "windows/amd64",
			floor:   GoFloor{Version: "1.21"},
			modules: []moduleInfo{{module: "foo"}},
			size:    &SizeReport{Modules: []ModuleSize{{Module: "foo", Size: 1500}}},
		},
	}

	rep := aggregateCells(cells, true)
	assert.True(t, rep.WithSize)

	var foo MatrixModule
	for _, m := range rep.Modules {
		if m.Module == "foo" {
			foo = m
		}
	}
	assert.Equal(t, map[string]uint64{"linux/amd64": 1000, "windows/amd64": 1500}, foo.SizeByCell)
}
