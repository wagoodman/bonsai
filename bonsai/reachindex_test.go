package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freedBytes (v(S)) must agree with a ground-truth reachable() sweep for arbitrary cut sets,
// since the greedy plan and Shapley pass both lean on it thousands of times.
func TestReachIndexFreedBytesMatchesReachable(t *testing.T) {
	spec := userScenario(true)
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)
	ri := g.newReachIndex(spec.size, base, c)

	// every subset of {gcr, oci}: compare index v(S) to a reachable() sweep with the same cut.
	subsets := [][]string{nil, {"gcr"}, {"oci"}, {"gcr", "oci"}}
	for _, s := range subsets {
		cut := make([]bool, len(ri.targets))
		sweepCut := map[string]bool{}
		for _, m := range s {
			cut[ri.targetID[m]] = true
			sweepCut[m] = true
		}
		after := g.reachable(sweepCut)
		var wantFreed uint64
		for ip := range base {
			if !after[ip] {
				wantFreed += spec.size[ip]
			}
		}
		assert.Equalf(t, wantFreed, ri.freedBytes(cut), "freed bytes for cut %v", s)
	}
}

// the greedy plan orders prunes by marginal saving and its cumulative column telescopes to
// the total prunable weight.
func TestGreedyPlanSharedScenario(t *testing.T) {
	spec := userScenario(true)
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)

	plan := g.greedyPlan(spec.size, base, c)
	require.Len(t, plan, 2)
	// gcr first: it alone frees 1500; oci frees nothing alone, so it comes second for +300.
	assert.Equal(t, "gcr", plan[0].Module)
	assert.Equal(t, uint64(1500), plan[0].Marginal)
	assert.Equal(t, uint64(1500), plan[0].Cumulative)
	assert.Equal(t, "oci", plan[1].Module)
	assert.Equal(t, uint64(300), plan[1].Marginal)
	assert.Equal(t, uint64(1800), plan[1].Cumulative, "cumulative telescopes to total prunable")
}

// each plan step's breakdown must account for its whole marginal: own code plus the deps it
// drags out. Pruning gcr first frees gcr's own 1000 (own) plus docker 500 (dragged out).
func TestGreedyPlanBreakdown(t *testing.T) {
	spec := userScenario(true)
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)

	plan := g.greedyPlan(spec.size, base, c)
	require.Len(t, plan, 2)

	gcr := plan[0]
	assert.Equal(t, "gcr", gcr.Module)
	assert.Equal(t, uint64(1000), gcr.OwnBytes, "gcr's own code")
	require.Len(t, gcr.Freed, 1)
	// docker is dragged out with gcr, and only gcr imports it (fan-in of 1).
	assert.Equal(t, FreedModule{Module: "docker", Bytes: 500, Importers: 1}, gcr.Freed[0])

	// invariant for every step: own + sum(dragged-out deps) == marginal.
	for _, s := range plan {
		var sum uint64
		for _, f := range s.Freed {
			sum += f.Bytes
		}
		assert.Equalf(t, s.Marginal, s.OwnBytes+sum, "step %s: own + deps must equal marginal", s.Module)
	}
}

// a module pulled in by two prune targets frees only once both are dropped. The greedy plan
// attributes it to the second prune, and that step's item is flagged "also prune <the first>"
// so the user sees freeing it is not exclusive to one candidate.
func TestPlanCoPrune(t *testing.T) {
	spec := graphSpec{
		main: "app",
		pkgMod: map[string]string{
			"app/main": "app", "a": "a", "b": "b", "shared": "shared",
		},
		imports: map[string][]string{
			"app/main": {"a", "b"}, // a and b are both 2nd-class targets
			"a":        {"shared"},
			"b":        {"shared"}, // shared is held by both a and b
		},
		roots: []string{"app/main"},
		size:  map[string]uint64{"app/main": 10, "a": 100, "b": 100, "shared": 500},
	}
	g := spec.build()
	c := classify(g, newPatternMatcher(nil), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)

	plan := g.greedyPlan(spec.size, base, c)
	require.Len(t, plan, 2)

	// step 1 frees a target's own code only; shared stays (the other target holds it).
	require.Len(t, plan[0].Freed, 0, "first prune frees no shared dep on its own")

	// step 2 frees shared — flagged that the first target must also be pruned.
	var sharedItem *FreedModule
	for i := range plan[1].Freed {
		if plan[1].Freed[i].Module == "shared" {
			sharedItem = &plan[1].Freed[i]
		}
	}
	require.NotNil(t, sharedItem)
	assert.Equal(t, []string{plan[0].Module}, sharedItem.CoPrune, "shared needs the first-pruned target too")
}

// the bug this guards against (the user's go-getter/generic case): a CONTROLLED module
// "wrapper" reaches deep only through getter. The old blocker-set logic reported "also prune
// wrapper" everywhere, but pruning getter severs wrapper's import of getter too (wrapper is
// yours), so getter alone frees deep. wrapper must NOT show up as a co-prune.
func TestPlanCoPruneNoFalsePositiveThroughStep(t *testing.T) {
	spec := graphSpec{
		main: "app",
		pkgMod: map[string]string{
			"app/main": "app", "wrapper": "wrapper", "getter": "getter", "deep": "deep",
		},
		imports: map[string][]string{
			"app/main": {"getter", "wrapper"},
			"wrapper":  {"getter"}, // controlled wrapper reaches deep only via getter
			"getter":   {"deep"},
		},
		roots: []string{"app/main"},
		size:  map[string]uint64{"app/main": 10, "wrapper": 50, "getter": 100, "deep": 800},
	}
	g := spec.build()
	// wrapper is 1st-class (controlled) but unlocked, so it is a prune target like getter.
	c := classify(g, newPatternMatcher([]string{"wrapper"}), newPatternMatcher(nil), newPatternMatcher([]string{"wrapper"}))
	base := g.reachable(nil)

	plan := g.greedyPlan(spec.size, base, c)
	require.NotEmpty(t, plan)

	// pruning getter frees deep outright — pruning getter also stops wrapper (yours) importing
	// it — so there is NO "also prune wrapper".
	step := plan[0]
	assert.Equal(t, "getter", step.Module)
	var deep *FreedModule
	for i := range step.Freed {
		if step.Freed[i].Module == "deep" {
			deep = &step.Freed[i]
		}
	}
	require.NotNil(t, deep, "deep is freed by pruning getter alone")
	assert.Empty(t, deep.CoPrune, "wrapper reaches deep only through getter, so it is not a co-prune")
}

// standard-library weight a dependency drags in is broken out by package and tagged Std, so
// "x/tools frees 1.2 MB of stdlib" reads as the go/types toolchain rather than a mystery
// bucket. Here pruning dep orphans archive/tar, which only dep reached.
func TestGreedyPlanStdBreakdown(t *testing.T) {
	spec := graphSpec{
		main:    "app",
		pkgMod:  map[string]string{"app/main": "app", "dep": "dep", "archive/tar": ""},
		imports: map[string][]string{"app/main": {"dep"}, "dep": {"archive/tar"}},
		roots:   []string{"app/main"},
		size:    map[string]uint64{"app/main": 10, "dep": 100, "archive/tar": 200},
	}
	g := spec.build()
	c := classify(g, newPatternMatcher(nil), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)

	plan := g.greedyPlan(spec.size, base, c)
	require.Len(t, plan, 1)
	assert.Equal(t, "dep", plan[0].Module)
	assert.Equal(t, uint64(100), plan[0].OwnBytes, "dep's own code")
	require.Len(t, plan[0].Freed, 1)
	assert.Equal(t, FreedModule{Module: "archive/tar", Bytes: 200, Std: true, Importers: 1}, plan[0].Freed[0],
		"the stdlib dep is broken out by package, tagged std, and annotated with its fan-in")
}

// the fan-in annotation counts distinct modules that directly import a unit. Here gcr is
// imported by both stereo and syft, while oci (in the shared scenario) is imported by gcr and
// syft — so both should report 2 importers.
func TestReachIndexImporterFanIn(t *testing.T) {
	spec := userScenario(true) // syft also imports oci directly
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)
	ri := g.newReachIndex(spec.size, base, c)

	assert.Equal(t, 2, ri.importers[freedKey{name: "gcr"}], "gcr imported by stereo and syft")
	assert.Equal(t, 2, ri.importers[freedKey{name: "oci"}], "oci imported by gcr and syft")
	assert.Equal(t, 1, ri.importers[freedKey{name: "docker"}], "docker imported by gcr only")
}
