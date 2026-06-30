package bonsai

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSweepTempsIn(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		mt := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(p, mt, mt))
		return p
	}
	oldBin := write("bonsai-bin-123", 2*time.Hour)
	oldDump := write("bonsai-dumpdep-456", 2*time.Hour)
	freshBin := write("bonsai-bin-789", time.Minute) // in-flight build: must survive
	other := write("not-bonsai-999", 2*time.Hour)    // someone else's temp: untouched

	sweepTempsIn(dir, time.Now().Add(-time.Hour))

	assert.NoFileExists(t, oldBin, "stale bin reclaimed")
	assert.NoFileExists(t, oldDump, "stale dumpdep reclaimed")
	assert.FileExists(t, freshBin, "fresh temp kept (could be a concurrent build)")
	assert.FileExists(t, other, "non-bonsai temp left alone")
}
