package bonsai

import "sort"

// The "why is this in my binary?" question is go-mod-why pointed at the class model: trace a
// module back through the modules that import it until you reach the 1st-class code you
// control. That terminal answers both halves of the question the user actually has — which
// 2nd-class dependency dragged this in, and through which of your own modules.

// ImportNode is a node in a module's import-why tree: the module, its class, and Via — the
// modules that directly import it, recursing toward 1st-class code. More records importers
// omitted at this node for brevity.
type ImportNode struct {
	Module string        `json:"module"`
	Class  string        `json:"class"`
	Via    []*ImportNode `json:"via,omitempty"`
	More   int           `json:"more,omitempty"`
}

const (
	whyBreadth = 3 // max importers shown per node before collapsing the rest into More
	whyBudget  = 8 // max total nodes in a why tree, so it stays readable
)

// moduleImporters builds the reverse module-import graph over base: module -> set of modules
// that directly import it. Standard-library targets and intra-module edges are skipped — the
// question is which of your dependencies pulled something in, not the stdlib's internals.
func (g *buildGraph) moduleImporters(base map[string]bool) map[string]map[string]bool {
	imp := map[string]map[string]bool{}
	for ip := range base {
		pkg := g.packages[ip]
		if pkg == nil {
			continue
		}
		src := g.moduleOfPkg[ip]
		if src == "" {
			continue
		}
		for _, dep := range pkg.Imports {
			if !base[dep] {
				continue
			}
			dst := g.moduleOfPkg[dep]
			if dst == "" || dst == src {
				continue
			}
			if imp[dst] == nil {
				imp[dst] = map[string]bool{}
			}
			imp[dst][src] = true
		}
	}
	return imp
}

// importWhy builds the why tree for module m: a reverse breadth-first walk over importers that
// terminates at 1st-class modules (the code you control — the satisfying answer) and is bounded
// in breadth and total nodes so it stays legible. Importers are ordered so the path toward your
// own code surfaces first. Returns nil when nothing imports m (it is itself an entrypoint).
func importWhy(m string, importers map[string]map[string]bool, c *classification, budget int) *ImportNode {
	root := &ImportNode{Module: m, Class: c.classOf(m).String()}
	visited := map[string]bool{m: true}
	nodes := 1
	queue := []*ImportNode{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// 1st-class modules are terminals: they are the code you own, the end of the "why".
		if cur != root && owned(c.classOf(cur.Module)) {
			continue
		}
		imps := make([]string, 0, len(importers[cur.Module]))
		for y := range importers[cur.Module] {
			if !visited[y] {
				imps = append(imps, y)
			}
		}
		// show importers closest to your own code first (owned, then 2nd-, then 3rd-class).
		sort.Slice(imps, func(i, j int) bool {
			if ri, rj := classRank(c.classOf(imps[i])), classRank(c.classOf(imps[j])); ri != rj {
				return ri < rj
			}
			return imps[i] < imps[j]
		})
		for i, y := range imps {
			if i >= whyBreadth || nodes >= budget {
				cur.More = len(imps) - i
				break
			}
			visited[y] = true
			child := &ImportNode{Module: y, Class: c.classOf(y).String()}
			cur.Via = append(cur.Via, child)
			nodes++
			queue = append(queue, child)
		}
	}
	if len(root.Via) == 0 {
		return nil
	}
	return root
}

// attachPlanWhy fills in the import-why tree for each pruned module and every non-stdlib
// module it drags out, so the plan explains who pulled each thing in without a second table.
func attachPlanWhy(plan []PrunePlanStep, importers map[string]map[string]bool, c *classification) {
	for i := range plan {
		plan[i].Why = importWhy(plan[i].Module, importers, c, whyBudget)
		for j := range plan[i].Freed {
			if f := &plan[i].Freed[j]; !f.Std {
				f.Why = importWhy(f.Module, importers, c, whyBudget)
			}
		}
	}
}

// owned reports whether a module is code the user controls — a terminal for the why trace.
func owned(cl moduleClass) bool { return cl == classMain || cl == classFirst }

// classRank orders modules from most-owned to least, so importers nearest your code sort first.
func classRank(cl moduleClass) int {
	switch cl {
	case classMain:
		return 0
	case classFirst:
		return 1
	case classSecond:
		return 2
	default:
		return 3
	}
}
