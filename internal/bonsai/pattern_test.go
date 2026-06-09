package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
