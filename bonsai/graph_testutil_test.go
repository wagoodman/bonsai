package bonsai

import (
	"fmt"
	"sort"
)

// treeShake is the independent oracle for the dominator engine: a plain reachability sweep
// that severs a single target's controlled edges and sums what becomes unreachable. The
// dominator tree must reproduce its FreedBytes for every target (see dominator_test.go).
func (g *buildGraph) treeShake(module string, selfSize map[string]uint64, baseReachable map[string]bool) PruneResult {
	after := g.reachable(map[string]bool{module: true})

	res := PruneResult{Module: module}
	freedByModule := map[string]int{}
	totalByModule := map[string]int{}
	for ip := range g.packages {
		if m := g.moduleOfPkg[ip]; m != "" {
			totalByModule[m]++
		}
	}

	for ip := range baseReachable {
		if after[ip] {
			continue
		}
		res.FreedPackages++
		res.FreedBytes += selfSize[ip]
		if m := g.moduleOfPkg[ip]; m != "" {
			freedByModule[m]++
		}
	}

	for m, freed := range freedByModule {
		if freed == totalByModule[m] {
			res.FreedModules = append(res.FreedModules, m)
		}
	}
	sort.Strings(res.FreedModules)
	return res
}

// graphSpec is a compact description of a build graph for tests: which package belongs to
// which module, the import edges between packages, the entrypoint roots, and per-package
// sizes. build() turns it into a *buildGraph wired enough for classification, reachability,
// dominator, and what-if passes.
type graphSpec struct {
	main    string
	pkgMod  map[string]string   // package import path -> module path ("" for std)
	imports map[string][]string // package import path -> imported package paths
	roots   []string            // entrypoint package import paths
	size    map[string]uint64   // package import path -> attributed bytes
	goVer   map[string]string   // module path -> declared `go` directive (optional)
}

func (s graphSpec) build() *buildGraph {
	g := &buildGraph{
		packages:     map[string]*listPackage{},
		moduleOfPkg:  map[string]string{},
		directMods:   map[string]bool{},
		allModules:   map[string]*listModule{},
		mainModule:   s.main,
		rootPackages: append([]string(nil), s.roots...),
	}
	for pkg, mod := range s.pkgMod {
		g.packages[pkg] = &listPackage{ImportPath: pkg, Imports: append([]string(nil), s.imports[pkg]...)}
		g.moduleOfPkg[pkg] = mod
		if mod != "" {
			if _, ok := g.allModules[mod]; !ok {
				g.allModules[mod] = &listModule{Path: mod, GoVersion: s.goVer[mod]}
			}
		}
	}
	return g
}

// userScenario is the running example from the design: main controls stereo and syft (1st
// class); both pull in gcr (2nd class); gcr pulls in docker and oci (3rd class). With shared
// set, syft also imports oci directly, making oci a 2nd-class target shared with gcr.
func userScenario(shared bool) graphSpec {
	s := graphSpec{
		main: "app",
		pkgMod: map[string]string{
			"app/main": "app",
			"stereo":   "stereo",
			"syft":     "syft",
			"gcr":      "gcr",
			"docker":   "docker",
			"oci":      "oci",
		},
		imports: map[string][]string{
			"app/main": {"stereo", "syft"},
			"stereo":   {"gcr"},
			"syft":     {"gcr"},
			"gcr":      {"docker", "oci"},
		},
		roots: []string{"app/main"},
		size: map[string]uint64{
			"app/main": 10, "stereo": 100, "syft": 100, "gcr": 1000, "docker": 500, "oci": 300,
		},
	}
	if shared {
		s.imports["syft"] = []string{"gcr", "oci"}
	}
	return s
}

// wideSharedScenario builds a graph with n 2nd-class targets (all imported by the main
// module) that overlap on a few shared 3rd-class deps. With n large it pushes the Shapley
// pass onto its sampled path while keeping genuine shared weight to attribute.
func wideSharedScenario(n int) graphSpec {
	s := graphSpec{
		main:    "app",
		pkgMod:  map[string]string{"app/main": "app"},
		imports: map[string][]string{"app/main": {}},
		roots:   []string{"app/main"},
		size:    map[string]uint64{"app/main": 10},
	}
	for j := range 3 { // shared 3rd-class deps
		sh := fmt.Sprintf("sh%d", j)
		s.pkgMod[sh] = sh
		s.size[sh] = uint64(200 + 50*j)
	}
	for i := range n {
		ti := fmt.Sprintf("t%d", i)
		s.pkgMod[ti] = ti
		s.size[ti] = uint64(100 + i)
		s.imports["app/main"] = append(s.imports["app/main"], ti)
		s.imports[ti] = []string{fmt.Sprintf("sh%d", i%3)} // overlap on shared deps
	}
	return s
}
