package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the exact Shapley value splits shared weight fairly: with v({gcr})=1500, v({oci})=0, and
// v({gcr,oci})=1800, gcr's fair share is 1650 and oci's is 150 — and they sum to the total
// prunable 1800 (efficiency).
func TestShapleyExactSharedScenario(t *testing.T) {
	spec := userScenario(true)
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)

	blame := g.shapleyBlame(spec.size, base, c)
	require.Len(t, blame, 2)
	got := map[string]ModuleBlame{}
	for _, b := range blame {
		got[b.Module] = b
	}
	assert.Equal(t, uint64(1650), got["gcr"].Blame)
	assert.Equal(t, uint64(150), got["oci"].Blame)
	assert.True(t, got["gcr"].Exact, "small coalition is computed exactly")

	var total uint64
	for _, b := range blame {
		total += b.Blame
	}
	assert.Equal(t, uint64(1800), total, "blame is efficient: sums to total prunable")
}

// blame must remain efficient (sum to total prunable) on larger graphs, where the sampled
// path runs. Use a graph with enough targets to exceed the exact threshold.
func TestShapleySampledIsEfficient(t *testing.T) {
	spec := wideSharedScenario(15) // 15 targets > exactShapleyMax forces sampling
	g := spec.build()
	c := classify(g, newPatternMatcher(nil), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)

	require.Greater(t, len(c.targets()), exactShapleyMax, "scenario must exceed the exact threshold")

	ri := g.newReachIndex(spec.size, base, c)
	cutAll := make([]bool, len(ri.targets))
	for i := range cutAll {
		cutAll[i] = true
	}
	totalPrunable := ri.freedBytes(cutAll)

	blame := g.shapleyBlame(spec.size, base, c)
	var total uint64
	for _, b := range blame {
		total += b.Blame
		assert.False(t, b.Exact, "large coalition is sampled")
	}
	// sampled marginals telescope to v(N) exactly per permutation, so the rounded sum is
	// within a byte-per-target of the true total.
	assert.InDelta(t, totalPrunable, total, float64(len(blame)), "sampled blame ≈ total prunable")
}
