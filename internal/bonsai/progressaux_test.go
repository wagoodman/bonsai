package bonsai

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatBuildCommand(t *testing.T) {
	// the args slice is the literal argv bonsai hands to `go` (see buildForAnalysis): user flags,
	// then bonsai's -o/-ldflags=-dumpdep, then tags and target.
	args := []string{"build", "-trimpath", `-ldflags=-w -s -X main.v=0`, "-o", "/tmp/bonsai-bin-1", "-ldflags=-dumpdep", "-tags=netgo,osusergo", "./cmd/app"}

	t.Run("cross-build prefixes effective platform + merged env, then the exact argv", func(t *testing.T) {
		p := Platform{GOOS: "linux", GOARCH: "arm64", Env: map[string]string{"CGO_ENABLED": "1"}}
		b := BuildSettings{Env: map[string]string{"CGO_ENABLED": "0", "FOO": "bar"}}
		got := formatBuildCommand(p, b, args)
		// cell env wins over global (CGO_ENABLED=1), keys sorted; argv shown verbatim, incl. -o and both -ldflags.
		assert.Equal(t, `GOOS=linux GOARCH=arm64 CGO_ENABLED=1 FOO=bar go build -trimpath -ldflags=-w -s -X main.v=0 -o /tmp/bonsai-bin-1 -ldflags=-dumpdep -tags=netgo,osusergo ./cmd/app`, got)
	})

	t.Run("host build fills GOOS/GOARCH from runtime", func(t *testing.T) {
		got := formatBuildCommand(Platform{}, BuildSettings{}, []string{"build", "-o", "/tmp/x", "-ldflags=-dumpdep", "./cmd/app"})
		assert.Equal(t, "GOOS="+runtime.GOOS+" GOARCH="+runtime.GOARCH+" go build -o /tmp/x -ldflags=-dumpdep ./cmd/app", got)
	})
}

func TestFloorDesc(t *testing.T) {
	assert.Equal(t, "no dep-imposed floor", floorDesc(GoFloor{}))
	assert.Equal(t, "go 1.21", floorDesc(GoFloor{Version: "1.21"}))
	assert.Equal(t, "go 1.24, 2 pinning", floorDesc(GoFloor{Version: "1.24", Critical: []string{"a", "b"}}))
}

func TestGraphCounts(t *testing.T) {
	g := &buildGraph{
		packages: map[string]*listPackage{
			"a": {ImportPath: "a", Imports: []string{"b", "c"}},
			"b": {ImportPath: "b", Imports: []string{"c"}},
			"c": {ImportPath: "c"},
		},
		allModules: map[string]*listModule{"m": {}, "dep": {}, "": {}}, // "" (std) excluded
	}
	pkgs, edges, mods := g.counts()
	assert.Equal(t, 3, pkgs)
	assert.Equal(t, 3, edges)
	assert.Equal(t, 2, mods)
}
