package bonsai

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestCmpGo(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{name: "minor less than", a: "1.21", b: "1.24.0", want: -1},
		{name: "language version below its .0 release", a: "1.24", b: "1.24.0", want: -1},
		{name: "patch ordering", a: "1.24.0", b: "1.24.5", want: -1},
		{name: "double-digit minor", a: "1.13", b: "1.9", want: 1},
		{name: "equal", a: "1.22", b: "1.22", want: 0},
		{name: "both empty", a: "", b: "", want: 0},
		{name: "empty sorts below any version", a: "", b: "1.13", want: -1},
		{name: "any version sorts above empty", a: "1.13", b: "", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cmpGo(tt.a, tt.b))
		})
	}
}

// goVerScenario is userScenario annotated with `go` directives so the floor model has something
// to chew on: gcr pins the highest non-owned directive, with docker just below it and oci lower.
func goVerScenario(shared bool) graphSpec {
	s := userScenario(shared)
	s.goVer = map[string]string{
		"app":    "1.21",
		"stereo": "1.21",
		"syft":   "1.21",
		"gcr":    "1.24.0",
		"docker": "1.23.0",
		"oci":    "1.22",
	}
	return s
}

func TestGoFloor(t *testing.T) {
	tests := []struct {
		name       string
		controlled []string
		omit       []string // modules treated as no-longer-in-build (e.g. pruned away)
		noVersions bool     // strip every `go` directive
		want       GoFloor
	}{
		{
			// only the main module is owned: every dep counts toward the floor, gcr pins it.
			name: "main-only owned, gcr pins the floor",
			want: GoFloor{Version: "1.24.0", Critical: []string{"gcr"}, NextVersion: "1.23.0", OwnedMax: "1.21"},
		},
		{
			// controlling stereo/syft only moves them into OwnedMax; they share the main's version
			// here so the floor is unchanged.
			name:       "controlling owned modules leaves the floor on deps",
			controlled: []string{"stereo", "syft"},
			want:       GoFloor{Version: "1.24.0", Critical: []string{"gcr"}, NextVersion: "1.23.0", OwnedMax: "1.21"},
		},
		{
			// dropping gcr from the build (as a prune would) lowers the floor to the next dep;
			// stereo/syft are owned here, so only oci remains to pin it.
			name:       "pruning the pinner lowers the floor",
			controlled: []string{"stereo", "syft"},
			omit:       []string{"gcr", "docker"}, // gcr drags docker out with it
			want:       GoFloor{Version: "1.22", Critical: []string{"oci"}, OwnedMax: "1.21"},
		},
		{
			// no dependency declares a directive: nothing constrains the floor.
			name:       "no directives means no floor",
			noVersions: true,
			want:       GoFloor{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := goVerScenario(false)
			if tt.noVersions {
				spec.goVer = nil
			}
			g := spec.build()
			c := classify(g, newPatternMatcher(tt.controlled), newPatternMatcher(nil), newPatternMatcher(nil))

			inBuild := map[string]bool{}
			for mod := range g.allModules {
				inBuild[mod] = true
			}
			for _, mod := range tt.omit {
				delete(inBuild, mod)
			}

			got := g.goFloor(inBuild, c)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("goFloor mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSessionGoFloor exercises the live floor through the reachability index: as targets are
// selected for pruning, the deps they orphan leave the build and the floor drops.
func TestSessionGoFloor(t *testing.T) {
	s := testSession(goVerScenario(true), ClassInputs{Controlled: []string{"stereo", "syft"}})

	// nothing pruned: gcr (1.24.0) pins the floor for the owned stereo/syft/app.
	base := s.GoFloor(map[string]bool{})
	assert.Equal(t, GoFloor{Version: "1.24.0", Critical: []string{"gcr"}, NextVersion: "1.23.0", OwnedMax: "1.21"}, base)

	// pruning gcr orphans docker too (oci is held directly by syft), so the floor falls past
	// docker's 1.23.0 straight to oci's 1.22.
	pruneGcr := s.GoFloor(map[string]bool{"gcr": true})
	assert.Equal(t, GoFloor{Version: "1.22", Critical: []string{"oci"}, OwnedMax: "1.21"}, pruneGcr)

	// pruning the whole cluster leaves no non-owned module: the floor is unconstrained by deps.
	pruneAll := s.GoFloor(map[string]bool{"gcr": true, "oci": true})
	assert.Equal(t, GoFloor{OwnedMax: "1.21"}, pruneAll)
}
