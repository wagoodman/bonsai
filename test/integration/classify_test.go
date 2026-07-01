package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEndToEndClassification(t *testing.T) {
	s := newSession(t)
	require.Equal(t, app, s.MainModule())

	mods := modulesByPath(s)
	require.Contains(t, mods, liba)
	require.Contains(t, mods, libs)
	require.Contains(t, mods, libb)
	require.Contains(t, mods, libc)

	// direct imports of the (only) controlled module are 2nd-class prune candidates;
	// transitively-reached deps are 3rd-class and never directly cuttable.
	assert.Equal(t, "2nd", mods[liba].Class)
	assert.Equal(t, "2nd", mods[libs].Class)
	assert.Equal(t, "3rd", mods[libb].Class)
	assert.Equal(t, "3rd", mods[libc].Class)

	assert.True(t, mods[liba].Target, "liba is a prune candidate")
	assert.True(t, mods[libs].Target, "libs is a prune candidate")
	assert.False(t, mods[libb].Target, "libb is 3rd-class, not directly cuttable")
	assert.False(t, mods[libc].Target, "libc is 3rd-class, not directly cuttable")

	// libc has two importers (liba and libs); libb only one (liba).
	assert.Equal(t, 2, mods[libc].Importers, "libc imported by liba and libs")
	assert.Equal(t, 1, mods[libb].Importers, "libb imported only by liba")
}

// TestEndToEndControlledWidening exercises bonsai's headline knob: marking liba as controlled
// promotes its direct dependency libb from 3rd-class to a 2nd-class prune candidate.
func TestEndToEndControlledWidening(t *testing.T) {
	s := newSession(t, liba)
	mods := modulesByPath(s)

	assert.Equal(t, "1st", mods[liba].Class, "liba is now controlled (yours)")
	assert.Equal(t, "2nd", mods[libb].Class, "libb is now a candidate via controlled liba")
	assert.True(t, mods[libb].Target, "libb became directly cuttable")

	// libc is imported by liba (controlled) and libs (not), so it's a candidate too.
	assert.Equal(t, "2nd", mods[libc].Class, "libc reachable directly from controlled code")
}
