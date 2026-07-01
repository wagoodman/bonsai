package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestEndToEndWhatIf(t *testing.T) {
	s := newSession(t)

	// pruning liba drops liba + its exclusive leaf libb; libc survives (libs still holds it).
	a := s.WhatIf(map[string]bool{liba: true})
	assert.ElementsMatch(t, []string{liba, libb}, a.PrunedModules)
	assert.Greater(t, a.FreedBytes, uint64(0))
	assert.NotContains(t, a.PrunedModules, libc)
	assert.NotContains(t, a.PrunedModules, libs)

	// pruning libs drops only libs; libc survives (liba still holds it).
	sx := s.WhatIf(map[string]bool{libs: true})
	assert.ElementsMatch(t, []string{libs}, sx.PrunedModules)

	// pruning both removes the whole dependency set, libc included.
	both := s.WhatIf(map[string]bool{liba: true, libs: true})
	assert.ElementsMatch(t, []string{liba, libs, libb, libc}, both.PrunedModules)
	assert.Greater(t, both.FreedBytes, a.FreedBytes, "cutting both frees more than liba alone")
}

// TestEndToEndEnginesAgree is the real-graph cross-check: Module.Exclusive comes from the
// dominator tree, WhatIf(prune X alone).FreedBytes comes from the reach-index engine. Two
// independent implementations must return the same exclusive retained size for every target.
func TestEndToEndEnginesAgree(t *testing.T) {
	s := newSession(t)
	for _, m := range s.Modules() {
		if !m.Target {
			continue
		}
		freed := s.WhatIf(map[string]bool{m.Module: true}).FreedBytes
		assert.Equal(t, m.Exclusive, freed,
			"dominator Exclusive and reach-index FreedBytes disagree for %s", m.Module)
	}
}

func TestEndToEndSizeReconciles(t *testing.T) {
	s := newSession(t)
	assert.Greater(t, s.AccountedSize(), uint64(0))

	var sum uint64
	for _, m := range s.Modules() {
		sum += m.Size
	}
	assert.Greater(t, sum, uint64(0), "modules carry attributed weight")
	assert.LessOrEqual(t, sum, s.AccountedSize(), "module weight cannot exceed the accounted binary")
}

// TestEndToEndDeadImportDropped proves the live-set filter's contract on a real build: libz is
// imported by app in source (main.go's never-called deadImport) but dead-code-eliminated, so it
// must be absent from the analysis entirely -- not a module, not a phantom prune target. This is
// the case the import graph must NOT over-count (the conservative direction of the dumpdep fix).
func TestEndToEndDeadImportDropped(t *testing.T) {
	s := newSession(t)
	mods := modulesByPath(s)

	_, present := mods[libz]
	assert.False(t, present, "DCE-eliminated source import must not appear as a module")
	assert.NotContains(t, s.WhatIf(map[string]bool{libz: true}).PrunedModules, libz,
		"a dead import can't be a prune target")
}

// TestEndToEndBlameSplitsSharedWeight checks the Shapley fair-blame attribution on the real
// graph. libc (680B) is shared by liba and libs, so its weight splits between them; libb is
// exclusive to liba. The invariant: blame sums to what pruning everything droppable frees, and
// liba (carrying libb outright + its share of libc) outweighs libs.
func TestEndToEndBlameSplitsSharedWeight(t *testing.T) {
	requireLong(t)
	r, err := bonsai.Resolve(bonsai.Config{Dir: fixtureDir(t), Blame: true})
	require.NoError(t, err)
	defer r.Close()

	blame := map[string]uint64{}
	for _, b := range r.Prune().Blame {
		assert.True(t, b.Exact, "few targets -> exact (deterministic) Shapley")
		blame[b.Module] = b.Blame
	}
	require.Contains(t, blame, liba)
	require.Contains(t, blame, libs)

	s := newSession(t)
	freedAll := s.WhatIf(map[string]bool{liba: true, libs: true}).FreedBytes
	// blame sums to freed-all within integer rounding (shares are rounded independently, so the
	// sum can drift by up to ~one byte per target).
	assert.InDelta(t, freedAll, blame[liba]+blame[libs], 2, "blame is a fair split of all prunable weight")
	assert.Greater(t, blame[liba], blame[libs], "liba holds libb outright plus its share of libc")
	assert.Greater(t, blame[libs], uint64(0), "libs still gets its share of shared libc")
}

// TestEndToEndDeepSharing stresses the shared-dependency handling past one level: a three-way
// shared dep (s) and a nested shared dep (t). Under the old dumpdep-as-edge-graph bug s would
// show a single importer and pruning one holder would wrongly free it; the live-set fix must
// keep every importer edge at depth.
func TestEndToEndDeepSharing(t *testing.T) {
	s := newSessionAt(t, deepDir(t))
	require.Equal(t, deepApp, s.MainModule())
	mods := modulesByPath(s)

	assert.Equal(t, "3rd", mods[deepS].Class)
	assert.Equal(t, "3rd", mods[deepT].Class)
	assert.Equal(t, 3, mods[deepS].Importers, "s is shared by a, b, c")
	assert.Equal(t, 2, mods[deepT].Importers, "t is held by s and c")

	// pruning any single importer frees only it: the shared subtree survives via the others.
	assert.ElementsMatch(t, []string{deepA}, s.WhatIf(map[string]bool{deepA: true}).PrunedModules)
	assert.ElementsMatch(t, []string{deepC}, s.WhatIf(map[string]bool{deepC: true}).PrunedModules)

	// pruning all three importers collapses the whole graph, s and t included.
	all := s.WhatIf(map[string]bool{deepA: true, deepB: true, deepC: true})
	assert.ElementsMatch(t, []string{deepA, deepB, deepC, deepS, deepT}, all.PrunedModules)

	// the two size engines must still agree target-by-target on the deeper graph.
	for _, m := range s.Modules() {
		if !m.Target {
			continue
		}
		assert.Equal(t, m.Exclusive, s.WhatIf(map[string]bool{m.Module: true}).FreedBytes,
			"engines disagree for %s", m.Module)
	}
}

// TestEndToEndDeepBlameSymmetry uses the deep fixture's symmetry as an exact oracle: a and b are
// byte-identical modules in identical graph positions, so their fair-blame must be equal; c also
// imports t directly, so it carries strictly more. Blame still sums to the freed-everything total.
func TestEndToEndDeepBlameSymmetry(t *testing.T) {
	requireLong(t)
	r, err := bonsai.Resolve(bonsai.Config{Dir: deepDir(t), Blame: true})
	require.NoError(t, err)
	defer r.Close()

	blame := map[string]uint64{}
	for _, b := range r.Prune().Blame {
		assert.True(t, b.Exact)
		blame[b.Module] = b.Blame
	}
	assert.Equal(t, blame[deepA], blame[deepB], "symmetric identical modules get equal blame")
	assert.Greater(t, blame[deepC], blame[deepA], "c also imports t directly, so it carries more")

	s := newSessionAt(t, deepDir(t))
	freedAll := s.WhatIf(map[string]bool{deepA: true, deepB: true, deepC: true}).FreedBytes
	// within integer rounding (see the blame-sum note above); 3 targets -> up to ~3 bytes drift.
	assert.InDelta(t, freedAll, blame[deepA]+blame[deepB]+blame[deepC], 3)
}
