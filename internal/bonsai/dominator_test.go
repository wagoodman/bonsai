package bonsai

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the dominator tree's exclusive savings must equal the ground-truth tree-shake (a full
// reachability sweep severing the target's controlled edges) for every target. This is the
// load-bearing invariant: it lets one dominator pass replace the per-module sweeps.
func TestDominatorExclusiveMatchesTreeShake(t *testing.T) {
	configs := []struct {
		name       string
		shared     bool
		controlled []string
		unlock     []string
	}{
		{name: "main only", controlled: nil},
		{name: "controlled org", controlled: []string{"stereo", "syft"}},
		{name: "controlled org, shared", shared: true, controlled: []string{"stereo", "syft"}},
		{name: "unlocked controlled", controlled: []string{"stereo", "syft"}, unlock: []string{"stereo"}},
	}
	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			spec := userScenario(cfg.shared)
			g := spec.build()
			c := classify(g, newPatternMatcher(cfg.controlled), newPatternMatcher(nil), newPatternMatcher(cfg.unlock))
			base := g.reachable(nil)
			dom := g.buildDomModel(spec.size, base, g.controlledGateway(c))

			for _, target := range c.targets() {
				want := g.treeShake(target, spec.size, base)
				assert.Equalf(t, want.FreedBytes, dom.exclusiveBytes(target),
					"exclusive bytes for target %s", target)
			}
		})
	}
}

// the shared scenario is the design's headline: pruning gcr frees its exclusive subtree but
// NOT oci, which syft also imports directly — 1500 of a 1800 potential, 300 shared.
func TestPruneResultsSharedScenario(t *testing.T) {
	spec := userScenario(true)
	g := spec.build()
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)
	dom := g.buildDomModel(spec.size, base, g.controlledGateway(c))
	blockers := g.blockerSets(c)
	prunes := g.pruneResults(spec.size, base, c, dom, blockers)

	gcr := prunes["gcr"]
	require.NotNil(t, gcr)
	assert.Equal(t, uint64(1500), gcr.FreedBytes, "exclusive: gcr + docker (oci held by syft)")
	assert.Equal(t, uint64(1800), gcr.PotentialBytes, "potential: gcr + docker + oci")
	assert.Equal(t, uint64(300), gcr.SharedBytes, "shared: oci")
	require.Len(t, gcr.SharedWith, 1)
	if diff := cmp.Diff(SharedHolder{Module: "oci", Bytes: 300, AlsoVia: []string{"oci"}}, gcr.SharedWith[0]); diff != "" {
		t.Errorf("shared holder mismatch (-want +got):\n%s", diff)
	}

	// pruning oci alone frees nothing — gcr still imports it — but its PRIZE is the full 300
	// bytes at stake: the all-inbound gateway credits the weight a controlled cut zeroes.
	oci := prunes["oci"]
	require.NotNil(t, oci)
	assert.Equal(t, uint64(0), oci.FreedBytes)
	assert.Equal(t, uint64(300), oci.PrizeBytes, "prize surfaces oci's weight even though EXCL is 0")
	assert.Greater(t, oci.PrizeBytes, oci.FreedBytes, "prize exceeds exclusive when a co-holder pins the module")
}

// randomized layered DAGs with random controlled/locked sets: the dominator exclusive must
// match the tree-shake sweep on every target, every time.
func TestDominatorExclusiveMatchesTreeShakeRandom(t *testing.T) {
	for seed := range int64(60) {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			g, sizes, controlled, locked := randomGraph(seed)
			c := classify(g, controlled, locked, newPatternMatcher(nil))
			base := g.reachable(nil)
			dom := g.buildDomModel(sizes, base, g.controlledGateway(c))

			for _, target := range c.targets() {
				want := g.treeShake(target, sizes, base).FreedBytes
				got := dom.exclusiveBytes(target)
				require.Equalf(t, want, got, "seed %d target %s", seed, target)
			}
		})
	}
}

// removing a module frees at least what cutting your own imports of it frees, so a target's
// prize (module-only gateway, full-graph retained) must never fall below its exclusive savings,
// on any random graph. This is the invariant the naive all-targets-at-once prize pass violated.
func TestPrizeAtLeastExclusive(t *testing.T) {
	for seed := range int64(60) {
		g, sizes, controlled, locked := randomGraph(seed)
		c := classify(g, controlled, locked, newPatternMatcher(nil))
		base := g.reachable(nil)
		dom := g.buildDomModel(sizes, base, g.controlledGateway(c))
		for _, target := range c.targets() {
			prizeDom := g.buildDomModel(sizes, base, g.moduleGateway(target))
			require.GreaterOrEqualf(t, prizeDom.exclusiveBytes(target), dom.exclusiveBytes(target),
				"seed %d target %s: prize below exclusive", seed, target)
		}
	}
}

// randomGraph builds a random layered DAG (every node reachable from the root) with random
// sizes and a random controlled/locked partition, exercising shared deps and deep chains.
func randomGraph(seed int64) (g *buildGraph, sizes map[string]uint64, controlled, locked patternMatcher) {
	rng := rand.New(rand.NewSource(seed))
	n := 6 + rng.Intn(14)

	spec := graphSpec{
		main:    "m0",
		pkgMod:  map[string]string{},
		imports: map[string][]string{},
		roots:   []string{"p0"},
		size:    map[string]uint64{},
	}
	var ctrl, lock []string
	for i := range n {
		pkg := fmt.Sprintf("p%d", i)
		mod := fmt.Sprintf("m%d", i)
		spec.pkgMod[pkg] = mod
		spec.size[pkg] = uint64(1 + rng.Intn(1000))
		if i > 0 {
			// 1-3 predecessors among earlier nodes guarantees acyclicity and reachability.
			preds := 1 + rng.Intn(3)
			for range preds {
				src := fmt.Sprintf("p%d", rng.Intn(i))
				spec.imports[src] = append(spec.imports[src], pkg)
			}
			if rng.Intn(2) == 0 {
				ctrl = append(ctrl, mod) // randomly controlled
			}
			if rng.Intn(4) == 0 {
				lock = append(lock, mod) // randomly locked
			}
		}
	}
	return spec.build(), spec.size, newPatternMatcher(ctrl), newPatternMatcher(lock)
}
