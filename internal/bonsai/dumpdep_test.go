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
	// the graph carries the COMPLETE go list import edges; the dump only decides which packages
	// survived DCE. shared is imported by both app and used (a diamond) -- the case the linker's
	// first-discovery dump cannot express but go list can.
	graph := func() *buildGraph {
		g := &buildGraph{
			packages:     map[string]*listPackage{},
			moduleOfPkg:  map[string]string{},
			rootPackages: []string{"app/cmd/app"},
		}
		imports := map[string][]string{
			"app/cmd/app":         {"fmt", "github.com/x/used", "github.com/x/shared"},
			"github.com/x/used":   {"github.com/x/shared"},
			"github.com/x/shared": {"fmt"},
			"github.com/x/dead":   {"fmt"},
			"fmt":                 nil,
		}
		for ip, imps := range imports {
			g.packages[ip] = &listPackage{ImportPath: ip, Imports: imps}
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
			name: "live edges kept (incl. shared diamond), dead pkg and edges into it dropped",
			dump: strings.Join([]string{
				"# app/cmd/app",
				"main.main -> fmt.Fprintln",
				"main.main -> github.com/x/used.Do",
				"main.main -> github.com/x/shared.S",             // shared first discovered via app...
				"github.com/x/used.Do -> github.com/x/shared.S",  // ...so the dump still records this edge as a from
				"runtime.throw -> github.com/x/used.Do.arginfo1", // aux symbol: doesn't mark a package live
				"main.main -> runtime.morestack",                 // unknown package: ignored
				"github.com/x/shared.S -> fmt.Sprintf",
			}, "\n"),
			// dead is never witnessed -> its imports cleared AND it's not a target of any edge.
			// shared keeps BOTH importers (app and used): the regression the live-set fix exists for.
			wantEdges: map[string][]string{
				"app/cmd/app":         {"fmt", "github.com/x/shared", "github.com/x/used"},
				"github.com/x/used":   {"github.com/x/shared"},
				"github.com/x/shared": {"fmt"},
				"github.com/x/dead":   nil,
				"fmt":                 nil, // live, but imports nothing
			},
			wantN: 5, // app:3 + used:1 + shared:1
		},
		{
			name:  "empty dump leaves go list edges untouched",
			dump:  "",
			wantN: 0,
			// fallback: nothing witnessed, so the source edges must survive verbatim.
			wantEdges: map[string][]string{
				"app/cmd/app":         {"fmt", "github.com/x/shared", "github.com/x/used"},
				"github.com/x/used":   {"github.com/x/shared"},
				"github.com/x/shared": {"fmt"},
				"github.com/x/dead":   {"fmt"},
				"fmt":                 nil,
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
