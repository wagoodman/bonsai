package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// whySize augments the anatomy sample with an import-why tree, exercising the "← imported by"
// trace the compact largest-modules table skips.
func whySize() *bonsai.SizeReport {
	rep := sampleSize()
	rep.Modules[0].Why = &bonsai.ImportNode{
		Module: "github.com/example/dep", Class: "2nd",
		Via:  []*bonsai.ImportNode{{Module: "github.com/core/keep", Class: "1st"}},
		More: 1,
	}
	return rep
}

// whyPrune augments the prune sample with orphaned-dep plan details (std tag, co-prune note, why
// trace) and blame — the render paths the compact candidate table skips.
func whyPrune() *bonsai.PruneReport {
	rep := samplePrune()
	rep.Plan[0].Marginal = 320
	rep.Plan[0].OwnBytes = 220
	rep.Plan[0].Freed = []bonsai.FreedModule{{
		Module: "github.com/x/orphan", Bytes: 100, Std: true,
		CoPrune: []string{"github.com/y/co"},
		Why:     &bonsai.ImportNode{Via: []*bonsai.ImportNode{{Module: "github.com/core/keep", Class: "1st"}}},
	}}
	rep.Blame = []bonsai.ModuleBlame{{Module: "github.com/example/dep", Blame: 320, Exact: true}}
	return rep
}

// floorWithHeadroom is a go-floor where the owned modules declare more than deps require, so the
// reclaimable-now note renders.
func floorWithHeadroom() bonsai.GoFloor {
	return bonsai.GoFloor{
		Version: "1.21", Critical: []string{"github.com/x/orphan"}, NextVersion: "1.20", OwnedMax: "1.24",
	}
}

func TestWriteJSON(t *testing.T) {
	rep := samplePrune()
	var b strings.Builder
	require.NoError(t, WriteJSON(&b, rep))

	out := b.String()
	assert.Contains(t, out, "\n  ", "output should be indented")

	// the document round-trips back to an equivalent report (HideIgnored is presentation-only,
	// tagged json:"-", so it never serializes — the sample leaves it false anyway).
	var got bonsai.PruneReport
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	if diff := cmp.Diff(*rep, got); diff != "" {
		t.Errorf("JSON round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestWriteSizeMarkdown(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteSizeMarkdown(&b, whySize(), 40, true))
	out := b.String()

	assert.NotContains(t, out, "\x1b[", "markdown must never carry ANSI escapes")
	assert.Contains(t, out, "# binary size analysis", "top-level heading")
	assert.Contains(t, out, "## Largest modules by size", "section heading")
	assert.Contains(t, out, "## Sections (file-backed)", "section layout shown with sections=true")
	assert.Contains(t, out, "| --- |", "markdown pipe-table separator")
	assert.Contains(t, out, "github.com/example/dep", "module data present")
	// the import-why trace renders under the module.
	assert.Contains(t, out, "← github.com/core/keep (1st)")
}

func TestWritePruneMarkdown(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WritePruneMarkdown(&b, whyPrune(), 40))
	out := b.String()

	assert.NotContains(t, out, "\x1b[", "markdown must never carry ANSI escapes")
	assert.Contains(t, out, "## Prune candidates", "section heading")
	assert.Contains(t, out, "| --- |", "markdown pipe-table separator")
	// orphaned-dep label carries the std tag and co-prune note.
	assert.Contains(t, out, "github.com/x/orphan (std)")
	assert.Contains(t, out, "also prune github.com/y/co")
	// blame renders.
	assert.Contains(t, out, "Fair-blame (Shapley)")
}

func TestWritePruneTableWhyTrace(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WritePruneTable(&b, whyPrune(), 40, false))
	out := b.String()

	// the import-why path renders the "← imported by (class)" trace and the "+N more" collapse.
	assert.Contains(t, out, "← github.com/core/keep (1st)")
	// blame renders.
	assert.Contains(t, out, "Fair-blame (Shapley)")
}

func TestWriteSizeTableWhyTrace(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteSizeTable(&b, whySize(), 40, false, false))
	out := b.String()

	assert.Contains(t, out, "← github.com/core/keep (1st)")
	assert.Contains(t, out, "← +1 more")
}

func TestWriteGoFloor(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteGoFloorTable(&b, floorWithHeadroom(), false))
	out := b.String()

	assert.Contains(t, out, "Go version floor")
	// owned modules declare more than deps require → reclaimable-now note.
	assert.Contains(t, out, "you can drop to 1.21 now")
	assert.Contains(t, out, "github.com/x/orphan", "critical (pinning) module listed")
}

// TestWriteGoFloorEmpty covers the empty-floor branch: no dependency declares a directive.
func TestWriteGoFloorEmpty(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteGoFloorTable(&b, bonsai.GoFloor{}, false))
	assert.Contains(t, b.String(), "no dependency declares a `go` directive")
}
