package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/locktui"
	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestBuildItems(t *testing.T) {
	mods := []bonsai.ModuleRef{
		{Path: "github.com/x/a", Direct: true},
		{Path: "github.com/x/b", Direct: false},
	}
	// lists carry a concrete module (already a row), a pattern, a stale module, a blank, and a
	// duplicate pattern across two lists.
	lock := []string{"github.com/x/a", "github.com/x/...", "  "}
	controlled := []string{"github.com/x/...", "github.com/stale/mod"}

	got := buildItems(mods, lock, controlled)

	want := []locktui.Item{
		{Module: "github.com/x/a", Direct: true},
		{Module: "github.com/x/b", Direct: false},
		{Module: "github.com/x/...", Pattern: true},
		{Module: "github.com/stale/mod", Pattern: true},
	}
	assert.Equal(t, want, got)
}
