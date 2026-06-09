package bonsai

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleResolved() (*binaryInfo, *buildGraph) {
	spec := goVerScenario(true) // carries per-module `go` directives so the round-trip exercises them
	g := spec.build()
	bin := &binaryInfo{
		FileSize: 4000, SectionsSize: 2010, CodeSize: 1200, DataSize: 600, PclntabSize: 210,
		SelfSize: spec.size, GOOS: "linux", GOARCH: "amd64", MainModule: "app",
		Sections: []SectionInfo{{Name: ".text", Size: 1200}},
	}
	return bin, g
}

// the snapshot must survive a gob round-trip and rebuild into a graph that reaches identically
// — that equivalence is what makes a cached run safe to substitute for a fresh build.
func TestResolveSnapshotRoundTrip(t *testing.T) {
	bin, g := sampleResolved()

	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(snapshotOf(bin, g)))
	var snap buildSnapshot
	require.NoError(t, gob.NewDecoder(&buf).Decode(&snap))
	bin2, g2 := snap.rebuild()

	assert.Equal(t, bin.SelfSize, bin2.SelfSize)
	assert.Equal(t, bin.SectionsSize, bin2.SectionsSize)
	assert.Equal(t, g.mainModule, g2.mainModule)
	assert.Equal(t, g.moduleOfPkg, g2.moduleOfPkg)
	for ip, p := range g.packages {
		require.Contains(t, g2.packages, ip)
		assert.Equal(t, p.Imports, g2.packages[ip].Imports, "edges for %s", ip)
	}
	// the load-bearing invariant: same reachability, and same classification/targets.
	assert.Equal(t, g.reachable(nil), g2.reachable(nil))
	c := classify(g, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	c2 := classify(g2, newPatternMatcher([]string{"stereo", "syft"}), newPatternMatcher(nil), newPatternMatcher(nil))
	assert.Equal(t, c.targets(), c2.targets())

	// go directives must survive the round-trip, or a cached run reports a bogus "no floor".
	for path, m := range g.allModules {
		assert.Equalf(t, m.GoVersion, g2.allModules[path].GoVersion, "go directive for %s", path)
	}
	inBuild := map[string]bool{}
	for mod := range g.allModules {
		inBuild[mod] = true
	}
	assert.Equal(t, g.goFloor(inBuild, c), g2.goFloor(inBuild, c2), "go floor preserved across cache")
}

func TestResolveCacheStoreLoad(t *testing.T) {
	t.Setenv("BONSAI_CACHE_DIR", t.TempDir())
	bin, g := sampleResolved()

	storeResolveCache("abc123", bin, g)
	bin2, g2, err := loadResolveCache("abc123")
	require.NoError(t, err)
	assert.Equal(t, bin.SelfSize, bin2.SelfSize)
	assert.Equal(t, g.reachable(nil), g2.reachable(nil))

	_, _, err = loadResolveCache("missing")
	assert.Error(t, err, "unknown key is a miss")
}

func TestResolveCacheKeyGating(t *testing.T) {
	// a non-git directory can't be cached.
	_, ok := resolveCacheKey(t.TempDir(), "github.com/x/y")
	assert.False(t, ok, "non-git dir is not cacheable")

	// BONSAI_NO_CACHE disables caching even on a clean repo.
	t.Setenv("BONSAI_NO_CACHE", "1")
	_, ok = resolveCacheKey(".", "github.com/x/y")
	assert.False(t, ok, "BONSAI_NO_CACHE disables the cache")
}
