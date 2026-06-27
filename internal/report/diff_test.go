package report

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func sampleDiff() *bonsai.DiffReport {
	return &bonsai.DiffReport{
		Ref:            "main",
		BaselineCommit: "abcdef0123456789",
		CurrentCommit:  "fedcba9876543210",
		MainModule:     "github.com/example/app",
		BaselineSize:   1000,
		CurrentSize:    1300,
		SizeDelta:      300,
		Added: []bonsai.ModuleDiff{
			{Module: "github.com/new/transitive", Direct: false, Bytes: 250},
			{Module: "github.com/new/direct", Direct: true, Bytes: 50},
		},
		Removed: []bonsai.ModuleDiff{
			{Module: "github.com/old/thing", Direct: true, Bytes: -100},
		},
		GoFloor: bonsai.GoFloorDiff{
			Before: "1.21", After: "1.24.0", Direction: 1,
			NewlyCritical: []string{"github.com/new/transitive"},
		},
	}
}

func TestWriteDiffTable(t *testing.T) {
	buf := &strings.Builder{}
	require.NoError(t, WriteDiffTable(buf, sampleDiff(), 40, false))
	out := buf.String()

	assert.Contains(t, out, "abcdef012345")  // 12-char commit prefix
	assert.Contains(t, out, "+300 B")        // net size delta
	assert.Contains(t, out, "1.21 → 1.24.0") // floor movement
	assert.Contains(t, out, "raised by github.com/new/transitive")
	assert.Contains(t, out, "added (2, +300 B)")
	assert.Contains(t, out, "github.com/new/transitive")
	assert.Contains(t, out, "transitive")
	assert.Contains(t, out, "removed (1, -100 B)")
	assert.Contains(t, out, "-100 B")
}

func TestWriteDiffTable_NoChange(t *testing.T) {
	buf := &strings.Builder{}
	rep := &bonsai.DiffReport{Ref: "main", BaselineCommit: "abcdef0123456789", GoFloor: bonsai.GoFloorDiff{After: "1.21"}}
	require.NoError(t, WriteDiffTable(buf, rep, 40, false))
	out := buf.String()

	assert.Contains(t, out, "no change in size or go floor")
	assert.NotContains(t, out, "added (")
	assert.NotContains(t, out, "removed (")
}

// a zero net size delta with offsetting changes must NOT print "no change" while a populated
// changed section renders right below it.
func TestWriteDiffTable_ZeroNetButChanged(t *testing.T) {
	buf := &strings.Builder{}
	rep := &bonsai.DiffReport{
		Ref: "main", BaselineCommit: "abcdef0123456789",
		BaselineSize: 1000, CurrentSize: 1000, SizeDelta: 0,
		Changed: []bonsai.ModuleDiff{
			{Module: "github.com/a", Direct: true, Bytes: 100},
			{Module: "github.com/b", Direct: true, Bytes: -100},
		},
		GoFloor: bonsai.GoFloorDiff{After: "1.21"},
	}
	require.NoError(t, WriteDiffTable(buf, rep, 40, false))
	out := buf.String()
	assert.NotContains(t, out, "no change")
	assert.Contains(t, out, "changed (2,")
}

// floor version held but a different dep now pins it: the churn must surface in the table.
func TestWriteDiffTable_FloorUnchangedChurn(t *testing.T) {
	buf := &strings.Builder{}
	rep := &bonsai.DiffReport{
		Ref: "main", BaselineCommit: "abcdef0123456789",
		BaselineSize: 1000, CurrentSize: 1000,
		GoFloor: bonsai.GoFloorDiff{After: "1.21", Direction: 0, NewlyCritical: []string{"github.com/new/pin"}},
	}
	require.NoError(t, WriteDiffTable(buf, rep, 40, false))
	out := buf.String()
	assert.Contains(t, out, "unchanged")
	assert.Contains(t, out, "now pinned by github.com/new/pin")
}

func TestWriteDiffTable_FloorLowered(t *testing.T) {
	buf := &strings.Builder{}
	rep := &bonsai.DiffReport{
		Ref: "main", BaselineCommit: "abcdef0123456789",
		BaselineSize: 1200, CurrentSize: 1000, SizeDelta: -200,
		GoFloor: bonsai.GoFloorDiff{Before: "1.24.0", After: "1.21", Direction: -1},
	}
	require.NoError(t, WriteDiffTable(buf, rep, 40, false))
	out := buf.String()
	assert.Contains(t, out, "1.24.0 → 1.21")
	assert.Contains(t, out, "lowered")
}

// more modules than --top collapses the tail into a "+N more" row.
func TestWriteDiffTable_TopTruncation(t *testing.T) {
	buf := &strings.Builder{}
	rep := &bonsai.DiffReport{
		Ref: "main", BaselineCommit: "abcdef0123456789", SizeDelta: 600,
		Added: []bonsai.ModuleDiff{
			{Module: "github.com/a", Bytes: 300},
			{Module: "github.com/b", Bytes: 200},
			{Module: "github.com/c", Bytes: 100},
		},
	}
	require.NoError(t, WriteDiffTable(buf, rep, 2, false))
	out := buf.String()
	assert.Contains(t, out, "github.com/a")
	assert.Contains(t, out, "github.com/b")
	assert.NotContains(t, out, "github.com/c")
	assert.Contains(t, out, "+1 more") // 3 added - top 2
}

// a symbolic (non-hex) baseline renders the ref rather than a truncated sha.
func TestWriteDiffTable_SymbolicBaseline(t *testing.T) {
	buf := &strings.Builder{}
	rep := &bonsai.DiffReport{Ref: "origin/main", BaselineCommit: "origin/main", GoFloor: bonsai.GoFloorDiff{After: "1.21"}}
	require.NoError(t, WriteDiffTable(buf, rep, 40, false))
	assert.Contains(t, buf.String(), "origin/main")
}

func TestWriteDiffMarkdown(t *testing.T) {
	buf := &strings.Builder{}
	require.NoError(t, WriteDiffMarkdown(buf, sampleDiff(), 40))
	out := buf.String()

	assert.Contains(t, out, "## ")       // markdown heading
	assert.Contains(t, out, "| BYTES |") // pipe table
	assert.NotContains(t, out, "\x1b[")  // no ANSI escapes in markdown
}
