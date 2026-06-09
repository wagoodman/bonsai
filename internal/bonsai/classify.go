package bonsai

import "sort"

// This file holds the conceptual core of bonsai's prune model: the *controllable-edge*
// generalization. Prior dependency-debloating work (DepClean / Soto-Valero et al.) asks a
// binary question — is a dependency used at all? bonsai instead asks which edges the user
// could realistically cut. An edge is cuttable iff it leaves a module whose source the user
// controls (a 1st-class module) and lands on a non-locked module. Everything else — the
// 1st/2nd/3rd-class taxonomy, the set of prune targets, what the dominator/reachability
// passes are even allowed to remove — is derived from that one set. Crucially, when only the
// main module is controlled this reduces exactly to "drop a direct dependency", so the model
// is a strict superset of the original behavior. See the package doc for the GC framing.

// moduleClass is where a module sits relative to the code the user controls. The taxonomy
// is derived, never declared: the user only declares which modules are controlled (1st
// class) and which are locked; everything else falls out of the dependency structure.
type moduleClass int

const (
	classUnknown moduleClass = iota
	classMain                // the main module (the ultimate 1st-class module)
	classFirst               // 1st-class: source the user controls (can edit its imports)
	classSecond              // 2nd-class: not controlled, but directly imported by a controlled module
	classThird               // 3rd-class: reached only through uncontrolled edges (pure transitive)
)

func (c moduleClass) String() string {
	switch c {
	case classMain:
		return "main"
	case classFirst:
		return "1st"
	case classSecond:
		return "2nd"
	case classThird:
		return "3rd"
	default:
		return "?"
	}
}

// classification is the resolved verdict for every module in the build: its class, whether
// it is locked (never proposed for pruning), and whether it is a prune target (a module the
// user could realistically stop importing). It is the single source of truth that the
// reachability, dominator, and what-if passes consult.
type classification struct {
	class  map[string]moduleClass
	locked map[string]bool
	target map[string]bool // non-locked modules with at least one cuttable edge into them

	// directImported is the set of modules directly imported by some controlled module —
	// the 2nd-class frontier (plus unlocked 1st-class modules), i.e. every module reachable
	// by a single cuttable hop out of code we own.
	directImported map[string]bool
}

func (c *classification) isLocked(m string) bool       { return c.locked[m] }
func (c *classification) isTarget(m string) bool       { return c.target[m] }
func (c *classification) classOf(m string) moduleClass { return c.class[m] }

// targets returns the prune-target modules sorted for stable output.
func (c *classification) targets() []string {
	out := make([]string, 0, len(c.target))
	for m := range c.target {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// classify resolves every module's class, lock state, and prune-target status from the
// controlled/locked/unlock patterns and the build graph's import edges. It also populates
// g.controlled so that subsequent reachability calls sever the right edges.
//
// Rules:
//   - controlled (1st-class) = the main module ∪ anything matching the controlled patterns.
//   - a module is locked (never pruned) if it matches a locked pattern, OR it is controlled
//     and not explicitly unlocked. The main module is always locked.
//   - 2nd-class = not controlled but directly imported by a controlled module.
//   - 3rd-class = everything else (reached only via uncontrolled edges).
//   - a prune target is any non-locked module with a cuttable edge into it — i.e. directly
//     imported by a controlled module. That is exactly the 2nd-class frontier plus any
//     unlocked 1st-class module (which the user has opted into dropping wholesale).
func classify(g *buildGraph, controlled, locked, unlock patternMatcher) *classification {
	// 1st-class set first, since both directness and reachability depend on it.
	g.controlled = map[string]bool{}
	for m := range g.allModules {
		if m != g.mainModule && controlled.match(m) {
			g.controlled[m] = true
		}
	}

	// the prunable frontier: modules reached by a single cuttable hop out of controlled
	// code. A cuttable hop lands on either an uncontrolled module (the 2nd-class frontier) or
	// an unlocked controlled module (a 1st-class module the user opted into dropping wholesale).
	directImported := map[string]bool{}
	for ip, pkg := range g.packages {
		srcMod := g.moduleOfPkg[ip]
		if !g.isControlled(srcMod) {
			continue
		}
		for _, imp := range pkg.Imports {
			dst := g.moduleOfPkg[imp]
			if dst == "" || dst == srcMod {
				continue
			}
			if !g.isControlled(dst) || !lockedMod(g, dst, locked, unlock) {
				directImported[dst] = true
			}
		}
	}

	c := &classification{
		class:          map[string]moduleClass{},
		locked:         map[string]bool{},
		target:         map[string]bool{},
		directImported: directImported,
	}

	for m := range g.allModules {
		c.locked[m] = lockedMod(g, m, locked, unlock)
		switch {
		case m == g.mainModule:
			c.class[m] = classMain
		case g.isControlled(m):
			c.class[m] = classFirst
		case directImported[m]:
			c.class[m] = classSecond
		default:
			c.class[m] = classThird
		}
		// a prune target is anything droppable: a cuttable edge reaches it and it is not locked.
		if directImported[m] && !c.locked[m] {
			c.target[m] = true
		}
	}
	return c
}

// lockedMod applies the lock rules for a single module: an explicit unlock always wins; an
// explicit lock pattern locks it; otherwise a controlled module is locked by default.
func lockedMod(g *buildGraph, m string, locked, unlock patternMatcher) bool {
	if m == g.mainModule {
		return true
	}
	if unlock.match(m) {
		return false
	}
	if locked.match(m) {
		return true
	}
	return g.isControlled(m)
}
