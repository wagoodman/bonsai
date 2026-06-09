package report

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func sampleAnalysis() *bonsai.Analysis {
	return &bonsai.Analysis{
		BinarySize:    2000,
		AccountedSize: 1000,
		CodeSize:      600,
		DataSize:      300,
		PclntabSize:   100,
		MainModule:    "github.com/example/app",
		MainSize:      120,
		Sections:      []bonsai.SectionInfo{{Name: ".text", Size: 600}},
		Modules: []bonsai.ModuleSize{
			{Module: "github.com/example/dep", Size: 400, Direct: true, Class: "2nd",
				Prune: &bonsai.PruneResult{FreedBytes: 320, FreedModules: []string{"x"}, PotentialBytes: 400, SharedBytes: 80,
					SharedWith: []bonsai.SharedHolder{{Module: "github.com/shared/lib", Bytes: 80, AlsoVia: []string{"github.com/example/other"}}}}},
			{Module: "github.com/core/keep", Size: 300, Direct: true, Class: "1st", Ignored: true},
		},
		Plan: []bonsai.PrunePlanStep{
			{Module: "github.com/example/dep", Marginal: 320, Cumulative: 320,
				OwnBytes: 320, Importers: 1},
		},
	}
}

func TestWriteTableColor(t *testing.T) {
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
			require.NoError(t, WriteTable(&b, sampleAnalysis(), 40, tt.color))
			out := b.String()
			assert.Equal(t, tt.wantANSI, strings.Contains(out, "\x1b["), "ANSI presence")
			// the data is always present regardless of color.
			assert.Contains(t, stripANSI(out), "github.com/example/dep")
			assert.Contains(t, stripANSI(out), "Prune plan")
		})
	}
}

func TestWriteTableHideIgnored(t *testing.T) {
	an := sampleAnalysis()
	an.HideIgnored = true
	var b strings.Builder
	require.NoError(t, WriteTable(&b, an, 40, false))
	assert.NotContains(t, b.String(), "github.com/core/keep", "ignored module should be hidden")

	an.HideIgnored = false
	var b2 strings.Builder
	require.NoError(t, WriteTable(&b2, an, 40, false))
	assert.Contains(t, b2.String(), "github.com/core/keep", "locked module should be shown when not hidden")
	assert.Contains(t, b2.String(), "locked", "locked kind label")
}
