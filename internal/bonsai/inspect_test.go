package bonsai

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entryWeights must partition a target's exclusive savings: the retained bytes across its
// gateway children sum to exactly exclusiveBytes(target), with one entry per directly-imported
// package. This is the load-bearing invariant for the rewrite-scope map.
func TestEntryWeightsMultiEntry(t *testing.T) {
	// app/main imports two packages of module "lib" directly (lib/a, lib/b); each pulls in its
	// own exclusive internal package. The cut routes both through lib's gateway, so its exclusive
	// weight splits across the two entry packages.
	spec := graphSpec{
		main: "app",
		pkgMod: map[string]string{
			"app/main": "app",
			"lib/a":    "lib",
			"lib/b":    "lib",
			"lib/ai":   "lib",
			"lib/bi":   "lib",
		},
		imports: map[string][]string{
			"app/main": {"lib/a", "lib/b"},
			"lib/a":    {"lib/ai"},
			"lib/b":    {"lib/bi"},
		},
		roots: []string{"app/main"},
		size:  map[string]uint64{"app/main": 10, "lib/a": 100, "lib/b": 50, "lib/ai": 1000, "lib/bi": 20},
	}
	g := spec.build()
	c := classify(g, newPatternMatcher(nil), newPatternMatcher(nil), newPatternMatcher(nil))
	base := g.reachable(nil)
	dom := g.buildDomModel(spec.size, base, g.controlledGateway(c))

	got := dom.entryWeights("lib")
	want := []entryWeight{
		{pkg: "lib/a", retained: 1100}, // lib/a + lib/ai
		{pkg: "lib/b", retained: 70},   // lib/b + lib/bi
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(entryWeight{})); diff != "" {
		t.Errorf("entry weights mismatch (-want +got):\n%s", diff)
	}

	// partition: entry retained sizes sum to the target's exclusive savings.
	var sum uint64
	for _, ew := range got {
		sum += ew.retained
	}
	assert.Equal(t, dom.exclusiveBytes("lib"), sum, "entry weights must partition exclusive savings")
}

// across random graphs, the entry weights of every target must sum to its exclusive savings —
// the partition invariant holds regardless of graph shape or classification.
func TestEntryWeightsPartitionRandom(t *testing.T) {
	for seed := range int64(60) {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			g, sizes, controlled, locked := randomGraph(seed)
			c := classify(g, controlled, locked, newPatternMatcher(nil))
			base := g.reachable(nil)
			dom := g.buildDomModel(sizes, base, g.controlledGateway(c))

			for _, target := range c.targets() {
				var sum uint64
				for _, ew := range dom.entryWeights(target) {
					sum += ew.retained
				}
				require.Equalf(t, dom.exclusiveBytes(target), sum, "seed %d target %s", seed, target)
			}
		})
	}
}

// importSitesForModule returns the concrete edit locations (file:line, first-party package,
// imported path) for a module, skipping tests, vendor, and testdata, and maps each imported
// package back to the first-party packages that import it.
func TestImportSitesForModule(t *testing.T) {
	dir := t.TempDir()

	writeGoFile(t, dir, "main.go", `package main

import (
	"fmt"

	"github.com/dep/bravo"
	"github.com/dep/bravo/sub"
)

func main() { fmt.Println(bravo.B(), sub.S()) }
`)
	writeGoFile(t, dir, "svc/svc.go", `package svc

import "github.com/dep/bravo"

func Use() { bravo.B() }
`)
	// excluded: _test.go, vendor, testdata.
	writeGoFile(t, dir, "main_test.go", `package main

import "github.com/dep/bravo"

func extra() { bravo.B() }
`)
	writeGoFile(t, dir, "vendor/v/v.go", `package v

import "github.com/dep/bravo"
`)

	g := &buildGraph{
		mainModule: "example.com/app",
		mainModDir: dir,
		moduleOfPkg: map[string]string{
			"github.com/dep/bravo":     "github.com/dep/bravo",
			"github.com/dep/bravo/sub": "github.com/dep/bravo",
			"fmt":                      "",
		},
	}

	sites, importedBy := g.importSitesForModule("github.com/dep/bravo")

	want := []ImportSite{
		{File: "main.go", Line: 6, FromPackage: "example.com/app", ImportPath: "github.com/dep/bravo"},
		{File: "main.go", Line: 7, FromPackage: "example.com/app", ImportPath: "github.com/dep/bravo/sub"},
		{File: "svc/svc.go", Line: 3, FromPackage: "example.com/app/svc", ImportPath: "github.com/dep/bravo"},
	}
	if diff := cmp.Diff(want, sites); diff != "" {
		t.Errorf("sites mismatch (-want +got):\n%s", diff)
	}

	wantImportedBy := map[string][]string{
		"github.com/dep/bravo":     {"example.com/app", "example.com/app/svc"},
		"github.com/dep/bravo/sub": {"example.com/app"},
	}
	if diff := cmp.Diff(wantImportedBy, importedBy); diff != "" {
		t.Errorf("importedBy mismatch (-want +got):\n%s", diff)
	}
}
