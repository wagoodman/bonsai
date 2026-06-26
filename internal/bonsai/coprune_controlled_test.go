package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// regression for the "co-prune a controlled module" bug: pruning a 3rd-party target must
// never recommend co-pruning a module the user declared controlled (1st-class). The headline
// recommendation reads SharedWith.AlsoVia, so a controlled/locked module leaking into that
// list is exactly the user-visible defect.
//
// Scenario mirrors the syft repro: the app imports a getter (the big win) and a controlled
// org module; both reach a shared 3rd-party dep. Before the fix, controlling the org via the
// bare-ellipsis pattern "github.com/org..." silently matched nothing, so the org module became
// a 2nd-class target and surfaced as a co-prune of getter.
func TestRecommendationsExcludeControlledModules(t *testing.T) {
	// app/main -> getter, orgb ; getter -> excl, shared ; orgb -> shared.
	spec := graphSpec{
		main: "app",
		pkgMod: map[string]string{
			"app/main": "app",
			"getter":   "github.com/hashicorp/go-getter",
			"orgb":     "github.com/org/bubbly",
			"excl":     "github.com/some/excl",
			"shared":   "github.com/some/shared",
		},
		imports: map[string][]string{
			"app/main": {"getter", "orgb"},
			"getter":   {"excl", "shared"},
			"orgb":     {"shared"},
		},
		roots: []string{"app/main"},
		size: map[string]uint64{
			"app/main": 10, "getter": 1000, "orgb": 200, "excl": 500, "shared": 300,
		},
	}

	tests := []struct {
		name       string
		controlled []string
	}{
		{name: "bare ellipsis (no slash)", controlled: []string{"github.com/org..."}},
		{name: "slash ellipsis subtree", controlled: []string{"github.com/org/..."}},
		{name: "exact module", controlled: []string{"github.com/org/bubbly"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := spec.build()
			c := classify(g, newPatternMatcher(tt.controlled), newPatternMatcher(nil), newPatternMatcher(nil))

			// the controlled org module is locked by default and must never be a prune target.
			assert.NotContains(t, c.targets(), "github.com/org/bubbly", "controlled module is not a target")

			base := g.reachable(nil)
			dom := g.buildDomModel(spec.size, base, c)
			blockers := g.blockerSets(c)
			prunes := g.pruneResults(spec.size, base, c, dom, blockers)

			getter := prunes["github.com/hashicorp/go-getter"]
			require.NotNil(t, getter)

			// no recommendation (co-prune via shared holders) may name a controlled/locked module.
			for _, sh := range getter.SharedWith {
				for _, via := range sh.AlsoVia {
					assert.Falsef(t, c.isLocked(via) || g.isControlled(via),
						"co-prune recommendation names controlled/locked module %q", via)
					assert.NotEqual(t, "github.com/org/bubbly", via, "must not co-prune the controlled org module")
				}
			}
		})
	}
}

// proves the scenario above actually exercises the leak path: when the org module is NOT
// controlled, it legitimately shows up as a co-prune of getter. This guards against the
// regression test silently passing because the scenario stopped reaching the code under test.
func TestRecommendationsCoPruneWhenNotControlled(t *testing.T) {
	spec := graphSpec{
		main: "app",
		pkgMod: map[string]string{
			"app/main": "app",
			"getter":   "github.com/hashicorp/go-getter",
			"orgb":     "github.com/org/bubbly",
			"excl":     "github.com/some/excl",
			"shared":   "github.com/some/shared",
		},
		imports: map[string][]string{
			"app/main": {"getter", "orgb"},
			"getter":   {"excl", "shared"},
			"orgb":     {"shared"},
		},
		roots: []string{"app/main"},
		size: map[string]uint64{
			"app/main": 10, "getter": 1000, "orgb": 200, "excl": 500, "shared": 300,
		},
	}
	g := spec.build()
	c := classify(g, newPatternMatcher(nil), newPatternMatcher(nil), newPatternMatcher(nil))

	base := g.reachable(nil)
	dom := g.buildDomModel(spec.size, base, c)
	blockers := g.blockerSets(c)
	prunes := g.pruneResults(spec.size, base, c, dom, blockers)

	getter := prunes["github.com/hashicorp/go-getter"]
	require.NotNil(t, getter)

	var via []string
	for _, sh := range getter.SharedWith {
		via = append(via, sh.AlsoVia...)
	}
	assert.Contains(t, via, "github.com/org/bubbly",
		"uncontrolled org module is a legitimate co-prune — confirms the test reaches the leak path")
}
