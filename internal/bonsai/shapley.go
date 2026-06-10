package bonsai

import "math/rand"

// Exclusive savings (the dominator number) credit shared weight to nobody: if A and Y both
// pull in a 3 MB dep, neither is "charged" for it, and the sum of exclusive savings under-
// counts the prunable total. The principled fix is the Shapley value from cooperative game
// theory: model pruning as a cooperative game where the players are the prune targets and
// the characteristic function v(S) = bytes freed by pruning the target set S (a monotone,
// submodular set function evaluated by reachindex.freedBytes). The Shapley value is the
// *unique* allocation satisfying efficiency (the shares sum to v(all) — the true total
// prunable), symmetry, null-player, and additivity, so it splits each shared dependency's
// cost fairly among the targets that hold it alive. Blame answers "what is this dependency
// really costing me, counting its fair share of everything it drags in?"
//
// Exact Shapley is #P-hard in general (a sum over all 2^n coalitions), but two facts make it
// cheap here: dependency graphs have small per-dep consumer degree, so for few targets we
// enumerate coalitions exactly; and for larger sets the Monte-Carlo permutation estimator
// (the marginals along a random ordering telescope to v(all), so estimates stay efficient)
// converges fast. References: Shapley (1953); Castro et al. on polynomial Monte-Carlo
// estimation; the heap/cost-sharing attribution literature surveyed for bonsai.

// ModuleBlame is a prune target's Shapley-attributed share of the total prunable weight. The
// sum of Blame across targets equals the bytes freed by pruning everything droppable. Exact
// is true when computed by exhaustive coalition enumeration (few targets) and false when
// Monte-Carlo sampled.
type ModuleBlame struct {
	Module string `json:"module"`
	Blame  uint64 `json:"blame"`
	Exact  bool   `json:"exact"`
}

const (
	// exactShapleyMax is the largest target count for which we enumerate all 2^n coalitions
	// exactly; above it we switch to permutation sampling. 2^12 = 4096 v(S) evaluations.
	exactShapleyMax = 12
	// sampleBudget caps total graph sweeps for the sampled path (≈ permutations × targets).
	sampleBudget = 24000
	minSamples   = 64
	maxSamples   = 512
	shapleySeed  = 1 // fixed seed: reproducible blame across runs
)

// shapleyBlame computes each prune target's fair share of the total prunable weight via the
// Shapley value of the prune game v(S) = bytes freed by pruning the target set S. It is exact
// for small target sets and Monte-Carlo sampled otherwise.
func (g *buildGraph) shapleyBlame(selfSize map[string]uint64, base map[string]bool, c *classification) []ModuleBlame {
	ri := g.newReachIndex(selfSize, base, c)
	n := len(ri.targets)
	if n == 0 {
		return nil
	}
	if n <= exactShapleyMax {
		return ri.sortedBlame(ri.exactShapley(n), true)
	}
	return ri.sortedBlame(ri.sampledShapley(n), false)
}

// exactShapley enumerates every coalition exactly. φ_i = Σ_{S⊆N\{i}} w(|S|)·(v(S∪i)−v(S))
// with w(s) = s!(n−s−1)!/n!. v(S) is evaluated once per subset and memoized.
func (ri *reachIndex) exactShapley(n int) []uint64 {
	fact := make([]float64, n+1)
	fact[0] = 1
	for i := 1; i <= n; i++ {
		fact[i] = fact[i-1] * float64(i)
	}
	weight := make([]float64, n) // weight[|S|]
	for s := range n {
		weight[s] = fact[s] * fact[n-s-1] / fact[n]
	}

	vOf := make([]uint64, 1<<n)
	cut := make([]bool, n)
	for mask := 0; mask < (1 << n); mask++ {
		for t := range n {
			cut[t] = mask&(1<<t) != 0
		}
		vOf[mask] = ri.freedBytes(cut)
	}

	phi := make([]float64, n)
	for mask := 0; mask < (1 << n); mask++ {
		size := popcount(mask)
		for i := range n {
			if mask&(1<<i) != 0 {
				continue // i already in S; marginal is defined for S without i
			}
			marginal := float64(vOf[mask|(1<<i)] - vOf[mask])
			phi[i] += weight[size] * marginal
		}
	}
	return roundBlame(phi)
}

// sampledShapley estimates the Shapley value by averaging marginal contributions over random
// target orderings — each permutation's marginals telescope to v(N), so the running averages
// converge to the exact values while always summing to the true total.
func (ri *reachIndex) sampledShapley(n int) []uint64 {
	samples := sampleBudget / n
	samples = max(samples, minSamples)
	samples = min(samples, maxSamples)

	rng := rand.New(rand.NewSource(shapleySeed)) //nolint:gosec // deterministic Monte-Carlo sampling; reproducibility matters, cryptographic strength does not
	perm := ri.allTargetIDs()
	cut := make([]bool, n)
	phi := make([]float64, n)

	for s := 0; s < samples; s++ {
		rng.Shuffle(n, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		for t := range cut {
			cut[t] = false
		}
		var prev uint64
		for _, t := range perm {
			cut[t] = true
			f := ri.freedBytes(cut)
			phi[t] += float64(f - prev)
			prev = f
		}
	}
	for i := range phi {
		phi[i] /= float64(samples)
	}
	return roundBlame(phi)
}

func roundBlame(phi []float64) []uint64 {
	out := make([]uint64, len(phi))
	for i, v := range phi {
		if v < 0 {
			v = 0 // marginals are non-negative; guard against float drift
		}
		out[i] = uint64(v + 0.5)
	}
	return out
}

func popcount(x int) int {
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}
