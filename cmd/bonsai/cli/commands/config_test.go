package commands

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitLock(t *testing.T) {
	candidates := map[string]bool{
		"github.com/x/a": true,
		"github.com/x/b": true,
	}
	tests := []struct {
		name            string
		current         []string
		wantPreselected []string // compared as a sorted set
		wantExtras      []string
	}{
		{
			name:            "concrete modules are pre-selected",
			current:         []string{"github.com/x/a", "github.com/x/b"},
			wantPreselected: []string{"github.com/x/a", "github.com/x/b"},
		},
		{
			// globs/patterns and stale modules aren't candidates: preserved verbatim as extras.
			name:            "non-candidates are preserved as extras",
			current:         []string{"github.com/x/a", "github.com/x/...", "github.com/stale/mod"},
			wantPreselected: []string{"github.com/x/a"},
			wantExtras:      []string{"github.com/x/...", "github.com/stale/mod"},
		},
		{
			name: "empty input",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preselected, extras := splitLock(tt.current, candidates)

			gotPre := make([]string, 0, len(preselected))
			for k := range preselected {
				gotPre = append(gotPre, k)
			}
			sort.Strings(gotPre)
			sort.Strings(tt.wantPreselected)
			assert.Equal(t, tt.wantPreselected, nonEmpty(gotPre))
			assert.Equal(t, tt.wantExtras, extras)
		})
	}
}

func TestPlural(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{name: "singular", n: 1, want: "y"},
		{name: "zero is plural", n: 0, want: "ies"},
		{name: "many", n: 5, want: "ies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, plural(tt.n))
		})
	}
}

// nonEmpty normalizes an empty slice to nil so comparisons against an unset want field hold.
func nonEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}
