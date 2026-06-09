package bonsai

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAuxSymbol(t *testing.T) {
	tests := []struct {
		name string
		sym  string
		want bool
	}{
		{name: "arginfo0", sym: "fmt.parseArgNumber.arginfo0", want: true},
		{name: "arginfo numbered", sym: "fmt.(*pp).Token.arginfo12", want: true},
		{name: "argliveinfo", sym: "fmt.isSpace.argliveinfo", want: true},
		{name: "args_stackmap", sym: "runtime.gcWriteBarrier.args_stackmap", want: true},
		{name: "stkobj", sym: "fmt.(*pp).doPrintln.stkobj", want: true},
		{name: "opendefer", sym: "sync.(*Pool).pinSlow.opendefer", want: true},
		{name: "real function", sym: "fmt.Fprintln", want: false},
		{name: "real method", sym: "fmt.(*pp).doPrintln", want: false},
		{name: "closure not aux", sym: "fmt.Fprintln.func1", want: false},
		{name: "arginfo without digits is not aux", sym: "pkg.Foo.arginfoX", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isAuxSymbol(tt.sym))
		})
	}
}

func TestSymbolPackage(t *testing.T) {
	const mainImport = "github.com/example/app/cmd/app"
	tests := []struct {
		name string
		sym  string
		want string
	}{
		{name: "main remapped to import path", sym: "main.main", want: mainImport},
		{name: "main method remapped", sym: "main.(*server).run", want: mainImport},
		{name: "stdlib package", sym: "fmt.Fprintln", want: "fmt"},
		{name: "nested stdlib", sym: "internal/poll.(*FD).Read", want: "internal/poll"},
		{name: "module package", sym: "github.com/example/dep.Do", want: "github.com/example/dep"},
		{name: "type metadata dropped", sym: "type:runtime.maptype", want: ""},
		{name: "go-prefixed dropped", sym: "go:info.fmt.Stringer", want: ""},
		{name: "aux symbol dropped", sym: "fmt.Fprintln.arginfo1", want: ""},
		{name: "empty", sym: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, symbolPackage(tt.sym, mainImport))
		})
	}
}

func TestReadReferenceEdges(t *testing.T) {
	// a graph with a main package, two deps, and stdlib fmt. reachability edges are
	// overwritten from the dump below.
	graph := func() *buildGraph {
		g := &buildGraph{
			packages:     map[string]*listPackage{},
			moduleOfPkg:  map[string]string{},
			rootPackages: []string{"app/cmd/app"},
		}
		for _, ip := range []string{"app/cmd/app", "fmt", "github.com/x/used", "github.com/x/dead"} {
			g.packages[ip] = &listPackage{ImportPath: ip, Imports: []string{"stale"}}
		}
		return g
	}

	tests := []struct {
		name      string
		dump      string
		wantEdges map[string][]string
		wantN     int
		wantErr   require.ErrorAssertionFunc
	}{
		{
			name: "real edges kept, aux and unknown dropped",
			dump: strings.Join([]string{
				"# app/cmd/app",
				"main.main -> fmt.Fprintln",
				"main.main -> github.com/x/used.Do",
				"fmt.Fprintln -> fmt.newPrinter",                 // intra-package, ignored
				"runtime.throw -> github.com/x/used.Do.arginfo1", // aux target, dropped
				"main.main -> runtime.morestack",                 // unknown target pkg, dropped
				"github.com/x/used.Do -> fmt.Sprintf",
			}, "\n"),
			// app/cmd/app -> {fmt, used}; used -> {fmt}; dead -> {} ; fmt -> {} (intra only)
			wantEdges: map[string][]string{
				"app/cmd/app":       {"fmt", "github.com/x/used"},
				"fmt":               {},
				"github.com/x/used": {"fmt"},
				"github.com/x/dead": {},
			},
			wantN: 3,
		},
		{
			name:  "empty dump yields no edges",
			dump:  "",
			wantN: 0,
			wantEdges: map[string][]string{
				"app/cmd/app": {}, "fmt": {}, "github.com/x/used": {}, "github.com/x/dead": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr == nil {
				tt.wantErr = require.NoError
			}
			g := graph()
			n, err := readReferenceEdges(g, strings.NewReader(tt.dump))
			tt.wantErr(t, err)
			if err != nil {
				return
			}

			require.Equal(t, tt.wantN, n)
			got := map[string][]string{}
			for ip, p := range g.packages {
				sort.Strings(p.Imports)
				got[ip] = p.Imports
			}
			for k, v := range tt.wantEdges {
				sort.Strings(v)
				tt.wantEdges[k] = v
			}
			if diff := cmp.Diff(tt.wantEdges, got); diff != "" {
				t.Errorf("edge mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// a standard-library package cannot import a third-party module in real source; such a
// reference edge is a symbol-attribution artifact and must be dropped so it does not pin the
// external module as always-reachable.
func TestReadReferenceEdgesDropsStdlibToExternal(t *testing.T) {
	g := &buildGraph{
		packages:     map[string]*listPackage{},
		moduleOfPkg:  map[string]string{},
		rootPackages: []string{"app/cmd/app"},
	}
	g.packages["app/cmd/app"] = &listPackage{ImportPath: "app/cmd/app"}
	g.packages["strings"] = &listPackage{ImportPath: "strings", Standard: true}
	g.packages["fmt"] = &listPackage{ImportPath: "fmt", Standard: true}
	g.packages["github.com/x/dep"] = &listPackage{ImportPath: "github.com/x/dep"}

	dump := strings.Join([]string{
		"main.main -> github.com/x/dep.Do",      // external -> external: kept
		"github.com/x/dep.Do -> fmt.Sprintf",    // external -> stdlib: kept
		"strings.Map -> github.com/x/dep.Token", // stdlib -> external: dropped (artifact)
		"strings.Map -> fmt.Sprintf",            // stdlib -> stdlib: kept
	}, "\n")

	_, err := readReferenceEdges(g, strings.NewReader(dump))
	require.NoError(t, err)

	got := map[string][]string{}
	for ip, p := range g.packages {
		sort.Strings(p.Imports)
		got[ip] = p.Imports
	}
	want := map[string][]string{
		"app/cmd/app":      {"github.com/x/dep"},
		"github.com/x/dep": {"fmt"},
		"strings":          {"fmt"}, // the strings -> github.com/x/dep artifact is gone
		"fmt":              {},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("edge mismatch (-want +got):\n%s", diff)
	}
}
