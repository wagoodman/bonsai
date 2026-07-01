package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// TestEndToEndPrebuiltBinary exercises the --binary (prebuilt) resolution path, which is entirely
// separate from source mode: it loads an already-built artifact and resolves the graph from `go
// list` only, with no `-dumpdep` reachability. It's the control group for the dumpdep fix -- shared
// deps come straight from go list here -- and it has a telling contrast with source mode around
// dead code (see the libz assertions).
func TestEndToEndPrebuiltBinary(t *testing.T) {
	requireLong(t)
	bin := buildBinary(t, fixtureDir(t))

	s, err := bonsai.NewSession(bonsai.Config{Binary: bin, Dir: fixtureDir(t)})
	require.NoError(t, err)
	require.Equal(t, app, s.MainModule())

	mods := modulesByPath(s)
	// classification and shared-dep detection still hold: go list gives complete edges natively,
	// so libc keeps both importers even without the dumpdep pass.
	assert.Equal(t, "2nd", mods[liba].Class)
	assert.Equal(t, "3rd", mods[libc].Class)
	assert.Equal(t, 2, mods[libc].Importers, "shared dep keeps both importers in binary mode too")

	// the source-vs-binary contrast: binary mode has no DCE reachability, so libz (dead in source
	// mode) is present here -- but with zero attributed bytes, since it has no symbols in the binary.
	libzMod, ok := mods[libz]
	assert.True(t, ok, "no DCE in binary mode, so the source import libz stays in the graph")
	assert.Equal(t, uint64(0), libzMod.Size, "libz contributes no bytes; it was eliminated at build")

	// the fixture binary is unstripped, so the on-disk size exceeds the accounted (stripped) size.
	assert.Greater(t, s.BinarySize(), s.AccountedSize())
}
