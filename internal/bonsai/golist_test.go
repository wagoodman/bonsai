package bonsai

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModuleForImportPath(t *testing.T) {
	// a graph that knows two third-party modules and the stdlib; modulePaths is sorted
	// longest-first so prefix matching attributes to the most specific module.
	g := &buildGraph{
		moduleOfPkg: map[string]string{
			"github.com/x/dep":      "github.com/x/dep",
			"github.com/x/dep/sub":  "github.com/x/dep",
			"github.com/x/deptools": "github.com/x/deptools",
			"fmt":                   "", // stdlib: known package, no module
		},
		modulePaths: []string{"github.com/x/deptools", "github.com/x/dep"},
	}
	sort.Slice(g.modulePaths, func(i, j int) bool { return len(g.modulePaths[i]) > len(g.modulePaths[j]) })

	tests := []struct {
		name       string
		importPath string
		wantMod    string
		wantOK     bool
	}{
		{name: "direct package hit", importPath: "github.com/x/dep", wantMod: "github.com/x/dep", wantOK: true},
		{name: "known subpackage hit", importPath: "github.com/x/dep/sub", wantMod: "github.com/x/dep", wantOK: true},
		{name: "stdlib package has no module", importPath: "fmt", wantMod: "", wantOK: false},
		{
			// not in moduleOfPkg, resolved by longest-prefix; must not match the shorter
			// "github.com/x/dep" prefix of "deptools".
			name: "unknown subpackage resolves by longest prefix", importPath: "github.com/x/deptools/inner",
			wantMod: "github.com/x/deptools", wantOK: true,
		},
		{name: "unknown path with no matching module", importPath: "github.com/other/thing", wantMod: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMod, gotOK := g.moduleForImportPath(tt.importPath)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantMod, gotMod)
		})
	}
}
