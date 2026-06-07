package bonsai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleAnalysis() *Analysis {
	return &Analysis{
		BinarySize:    2000,
		AccountedSize: 1000,
		CodeSize:      600,
		DataSize:      300,
		PclntabSize:   100,
		MainModule:    "github.com/example/app",
		MainSize:      120,
		Sections:      []SectionInfo{{Name: ".text", Size: 600}},
		Modules: []ModuleSize{
			{Module: "github.com/example/dep", Size: 400, Direct: true, Class: "2nd",
				Prune: &PruneResult{FreedBytes: 320, FreedModules: []string{"x"}, PotentialBytes: 400, SharedBytes: 80,
					SharedWith: []SharedHolder{{Module: "github.com/shared/lib", Bytes: 80, AlsoVia: []string{"github.com/example/other"}}}}},
			{Module: "github.com/core/keep", Size: 300, Direct: true, Class: "1st", Ignored: true},
		},
		Plan: []PrunePlanStep{
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

func TestPatternMatcher(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		module   string
		want     bool
	}{
		{name: "exact match", patterns: []string{"github.com/a/b"}, module: "github.com/a/b", want: true},
		{name: "exact non-match", patterns: []string{"github.com/a/b"}, module: "github.com/a/c", want: false},
		{name: "subtree match", patterns: []string{"github.com/anchore/..."}, module: "github.com/anchore/syft", want: true},
		{name: "subtree matches root", patterns: []string{"github.com/anchore/..."}, module: "github.com/anchore", want: true},
		{name: "subtree non-match sibling", patterns: []string{"github.com/anchore/..."}, module: "github.com/anchorex/y", want: false},
		{name: "glob single segment", patterns: []string{"golang.org/x/*"}, module: "golang.org/x/text", want: true},
		{name: "glob does not cross slash", patterns: []string{"golang.org/x/*"}, module: "golang.org/x/text/encoding", want: false},
		{name: "empty patterns", patterns: nil, module: "github.com/a/b", want: false},
		{name: "blank pattern ignored", patterns: []string{"  "}, module: "github.com/a/b", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, newPatternMatcher(tt.patterns).match(tt.module))
		})
	}
}
