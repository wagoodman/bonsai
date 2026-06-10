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

// whyAnalysis augments the sample with import-why trees, a go-floor with reclaimable headroom,
// orphaned-dep plan details, and blame — exercising the render paths the compact table skips.
func whyAnalysis() *bonsai.Analysis {
	an := sampleAnalysis()
	an.Modules[0].Why = &bonsai.ImportNode{
		Module: "github.com/example/dep", Class: "2nd",
		Via:  []*bonsai.ImportNode{{Module: "github.com/core/keep", Class: "1st"}},
		More: 1,
	}
	an.Plan[0].Marginal = 320
	an.Plan[0].OwnBytes = 220
	an.Plan[0].Freed = []bonsai.FreedModule{{
		Module: "github.com/x/orphan", Bytes: 100, Std: true,
		CoPrune: []string{"github.com/y/co"},
		Why:     &bonsai.ImportNode{Via: []*bonsai.ImportNode{{Module: "github.com/core/keep", Class: "1st"}}},
	}}
	an.GoFloor = bonsai.GoFloor{
		Version: "1.21", Critical: []string{"github.com/x/orphan"}, NextVersion: "1.20", OwnedMax: "1.24",
	}
	an.Blame = []bonsai.ModuleBlame{{Module: "github.com/example/dep", Blame: 320, Exact: true}}
	return an
}

func TestWriteJSON(t *testing.T) {
	an := sampleAnalysis()
	var b strings.Builder
	require.NoError(t, WriteJSON(&b, an))

	out := b.String()
	assert.Contains(t, out, "\n  ", "output should be indented")

	// the document round-trips back to an equivalent analysis (HideIgnored is presentation-only,
	// tagged json:"-", so it never serializes — the sample leaves it false anyway).
	var got bonsai.Analysis
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	if diff := cmp.Diff(*an, got); diff != "" {
		t.Errorf("JSON round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestWriteMarkdown(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteMarkdown(&b, whyAnalysis(), 40))
	out := b.String()

	assert.NotContains(t, out, "\x1b[", "markdown must never carry ANSI escapes")
	assert.Contains(t, out, "# binary size analysis", "top-level heading")
	assert.Contains(t, out, "## Largest modules by size", "section heading")
	assert.Contains(t, out, "| --- |", "markdown pipe-table separator")
	assert.Contains(t, out, "github.com/example/dep", "module data present")
	// the go-floor headroom note: owned modules declare more than deps require.
	assert.Contains(t, out, "you can drop to 1.21 now")
	// orphaned-dep label carries the std tag and co-prune note.
	assert.Contains(t, out, "github.com/x/orphan (std)")
	assert.Contains(t, out, "also prune github.com/y/co")
}

func TestWriteTableWhyTrace(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteTable(&b, whyAnalysis(), 40, false))
	out := b.String()

	// the import-why path renders the "← imported by (class)" trace and the "+N more" collapse.
	assert.Contains(t, out, "← github.com/core/keep (1st)")
	assert.Contains(t, out, "← +1 more")
	// blame and go-floor sections render.
	assert.Contains(t, out, "Fair-blame (Shapley)")
	assert.Contains(t, out, "Go version floor")
}

// TestWriteTableNoGoFloor covers the empty-floor branch: no dependency declares a directive.
func TestWriteTableNoGoFloor(t *testing.T) {
	an := sampleAnalysis() // sampleAnalysis leaves GoFloor zero
	var b strings.Builder
	require.NoError(t, WriteTable(&b, an, 40, false))
	assert.Contains(t, b.String(), "no dependency declares a `go` directive")
}
