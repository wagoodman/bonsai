package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the import-why tree traces a module back through its importers and stops at the 1st-class
// code that pulled it in. docker (3rd) is reached only via gcr (2nd), which stereo and syft
// (1st, controlled) import — so the trace terminates there.
func TestImportWhy(t *testing.T) {
	spec := userScenario(true)
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)
	importers := g.moduleImporters(base)

	why := importWhy("docker", importers, c, whyBudget)
	require.NotNil(t, why)
	assert.Equal(t, "docker", why.Module)

	require.Len(t, why.Via, 1)
	gcr := why.Via[0]
	assert.Equal(t, "gcr", gcr.Module)
	assert.Equal(t, "2nd", gcr.Class)

	// gcr's importers are the controlled 1st-class modules, which terminate the trace.
	require.Len(t, gcr.Via, 2)
	got := []string{gcr.Via[0].Module, gcr.Via[1].Module}
	assert.ElementsMatch(t, []string{"stereo", "syft"}, got)
	for _, n := range gcr.Via {
		assert.Equal(t, "1st", n.Class)
		assert.Empty(t, n.Via, "1st-class modules are terminals — the trace stops at your code")
	}
}

// an entrypoint module (nothing imports it) has no why tree.
func TestImportWhyEntrypointIsNil(t *testing.T) {
	spec := userScenario(false)
	g := spec.build()
	c := classify(g, newPatternMatcher(nil), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)
	importers := g.moduleImporters(base)

	assert.Nil(t, importWhy("app", importers, c, whyBudget), "the main module is imported by nothing")
}
