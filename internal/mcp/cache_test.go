package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// the source fingerprint must change when a tracked file changes — the property that makes the
// agent's edit loop invalidate the cached build automatically.
func TestSourceFingerprintChangesOnEdit(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(main, []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644))

	cfg := bonsai.Config{Dir: dir}
	before := sourceFingerprint(cfg)

	// rewrite a tracked file with new content and a later mtime.
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(main, []byte("package main\n\nfunc main() {}\n"), 0o644))
	after := sourceFingerprint(cfg)
	assert.NotEqual(t, before, after, "fingerprint must change after a source edit")

	// an untracked file (not .go/.mod/.sum) does not affect the fingerprint.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644))
	assert.Equal(t, after, sourceFingerprint(cfg), "untracked file must not change the fingerprint")
}

// configKey distinguishes targets that should build separately and matches those that shouldn't.
func TestConfigKey(t *testing.T) {
	base := bonsai.Config{Dir: "a", Controlled: []string{"x"}}
	assert.Equal(t, configKey(base), configKey(bonsai.Config{Dir: "a", Controlled: []string{"x"}}), "same config => same key")
	assert.NotEqual(t, configKey(base), configKey(bonsai.Config{Dir: "b", Controlled: []string{"x"}}), "different dir => different key")
	assert.NotEqual(t, configKey(base), configKey(bonsai.Config{Dir: "a", Unlock: []string{"y"}}), "different unlock => different key")
}
