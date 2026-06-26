package report

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// sampleSize is the anatomy fixture: sizes, sections, and modules (size + class only, no prune).
func sampleSize() *bonsai.SizeReport {
	return &bonsai.SizeReport{
		BinarySize:    2000,
		AccountedSize: 1000,
		CodeSize:      600,
		DataSize:      300,
		PclntabSize:   100,
		MainModule:    "github.com/example/app",
		MainSize:      120,
		Sections:      []bonsai.SectionInfo{{Name: ".text", Size: 600}},
		Modules: []bonsai.ModuleSize{
			{Module: "github.com/example/dep", Size: 400, Direct: true, Class: "2nd"},
			{Module: "github.com/core/keep", Size: 300, Direct: true, Class: "1st", Locked: true},
		},
	}
}

// samplePrune is the prune fixture: candidates with prune estimates and a one-step greedy plan.
func samplePrune() *bonsai.PruneReport {
	return &bonsai.PruneReport{
		AccountedSize: 1000,
		MainModule:    "github.com/example/app",
		Modules: []bonsai.ModuleSize{
			{Module: "github.com/example/dep", Size: 400, Direct: true, Class: "2nd",
				Prune: &bonsai.PruneResult{FreedBytes: 320, FreedModules: []string{"x"}, PotentialBytes: 400, SharedBytes: 80,
					SharedWith: []bonsai.SharedHolder{{Module: "github.com/shared/lib", Bytes: 80, AlsoVia: []string{"github.com/example/other"}}}}},
			{Module: "github.com/core/keep", Size: 300, Direct: true, Class: "1st", Locked: true},
		},
		Plan: []bonsai.PrunePlanStep{
			{Module: "github.com/example/dep", Marginal: 320, Cumulative: 320, OwnBytes: 320, Importers: 1},
		},
	}
}

func TestWriteSizeTableColor(t *testing.T) {
	tests := []struct {
		name     string
		color    bool
		wantANSI bool
	}{
		{name: "color on emits ANSI", color: true, wantANSI: true},
		{name: "color off is plain", color: false, wantANSI: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			require.NoError(t, WriteSizeTable(&b, sampleSize(), 40, false, tt.color))
			out := b.String()
			assert.Equal(t, tt.wantANSI, strings.Contains(out, "\x1b["), "ANSI presence")
			// the data is always present regardless of color.
			assert.Contains(t, stripANSI(out), "github.com/example/dep")
			assert.Contains(t, stripANSI(out), "by content")
		})
	}
}

func TestWriteSizeSectionsGated(t *testing.T) {
	var off strings.Builder
	require.NoError(t, WriteSizeTable(&off, sampleSize(), 40, false, false))
	assert.NotContains(t, off.String(), "Sections (file-backed)", "section layout hidden by default")

	var on strings.Builder
	require.NoError(t, WriteSizeTable(&on, sampleSize(), 40, true, false))
	assert.Contains(t, on.String(), "Sections (file-backed)", "section layout shown with --sections")
}

func TestWriteSizeHideLocked(t *testing.T) {
	rep := sampleSize()
	rep.HideLocked = true
	var b strings.Builder
	require.NoError(t, WriteSizeTable(&b, rep, 40, false, false))
	assert.NotContains(t, b.String(), "github.com/core/keep", "locked module should be hidden")

	rep.HideLocked = false
	var b2 strings.Builder
	require.NoError(t, WriteSizeTable(&b2, rep, 40, false, false))
	assert.Contains(t, b2.String(), "github.com/core/keep", "locked module should be shown when not hidden")
	assert.Contains(t, b2.String(), "locked", "locked kind label")
}

func TestWritePruneTable(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WritePruneTable(&b, samplePrune(), 40, false))
	out := b.String()
	assert.Contains(t, out, "Prune candidates")
	assert.Contains(t, out, "Prune plan")
	assert.Contains(t, out, "github.com/example/dep")
}
