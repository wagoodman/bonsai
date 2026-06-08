package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSession assembles a Session from a synthetic graph (no real build), mirroring what
// NewSession does after resolve().
func testSession(spec graphSpec, in ClassInputs) *Session {
	g := spec.build()
	var total uint64
	for _, b := range spec.size {
		total += b
	}
	s := &Session{
		bin:      &binaryInfo{SectionsSize: total, SelfSize: spec.size},
		g:        g,
		base:     g.reachable(nil),
		selfSize: spec.size,
		moduleSz: map[string]uint64{},
	}
	for ip := range s.base {
		if mod := g.moduleOfPkg[ip]; mod != "" {
			s.moduleSz[mod] += s.selfSize[ip]
		}
	}
	s.importers = g.moduleImporters(s.base)
	s.importees = g.moduleImportees(s.base)
	s.Reclassify(in)
	return s
}

func TestSessionWhatIf(t *testing.T) {
	s := testSession(userScenario(true), ClassInputs{Controlled: []string{"stereo", "syft"}})

	// pruning gcr alone frees gcr + docker (1500); oci stays (syft holds it directly).
	got := s.WhatIf(map[string]bool{"gcr": true})
	assert.Equal(t, uint64(2010), got.OriginalSize)
	assert.Equal(t, uint64(1500), got.FreedBytes)
	assert.Equal(t, uint64(510), got.ProjectedSize)
	assert.Equal(t, []string{"docker", "gcr"}, got.PrunedModules)

	// pruning gcr AND oci frees the whole cluster.
	both := s.WhatIf(map[string]bool{"gcr": true, "oci": true})
	assert.Equal(t, uint64(1800), both.FreedBytes)
	assert.Equal(t, []string{"docker", "gcr", "oci"}, both.PrunedModules)

	// locked/non-target selections are ignored.
	assert.Equal(t, uint64(0), s.WhatIf(map[string]bool{"stereo": true}).FreedBytes)
}

func TestSessionMarginal(t *testing.T) {
	s := testSession(userScenario(true), ClassInputs{Controlled: []string{"stereo", "syft"}})

	// oci frees nothing on its own (gcr holds it)...
	assert.Equal(t, uint64(0), s.Marginal(map[string]bool{}, "oci"))
	// ...but adds 300 once gcr is already selected.
	assert.Equal(t, uint64(300), s.Marginal(map[string]bool{"gcr": true}, "oci"))
}

func TestSessionReclassifyChangesCandidates(t *testing.T) {
	s := testSession(userScenario(true), ClassInputs{}) // main-only controlled

	target := func() bool {
		for _, m := range s.Modules() {
			if m.Module == "gcr" {
				return m.Target
			}
		}
		t.Fatal("gcr not in modules")
		return false
	}

	assert.False(t, target(), "with only main controlled, gcr is 3rd-class, not a candidate")

	s.Reclassify(ClassInputs{Controlled: []string{"stereo", "syft"}})
	assert.True(t, target(), "controlling stereo/syft makes gcr a 2nd-class candidate")
}

func TestSessionDetail(t *testing.T) {
	s := testSession(userScenario(true), ClassInputs{Controlled: []string{"stereo", "syft"}})

	d := s.Detail("gcr")
	assert.Equal(t, "2nd", d.Class)
	assert.True(t, d.Target)
	assert.Equal(t, uint64(1000), d.Own, "gcr's own code")
	assert.Equal(t, uint64(1500), d.Exclusive)
	// docker is dragged out exclusively; oci is shared, so not here.
	require.Len(t, d.DragOut, 1)
	assert.Equal(t, FreedModule{Module: "docker", Bytes: 500}, d.DragOut[0])
	// fan-in: gcr is imported by stereo and syft.
	assert.Equal(t, 2, d.Importers)

	// 1st-class modules are styled gold even though locked by default.
	st := s.Detail("stereo")
	assert.True(t, st.Controlled)
	assert.True(t, st.Locked)
}
