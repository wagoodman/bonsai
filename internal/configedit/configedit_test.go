package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindConfig(t *testing.T) {
	t.Run("returns existing default", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, ".bonsai.yaml")
		require.NoError(t, os.WriteFile(want, []byte("analysis:\n"), 0o644))
		assert.Equal(t, want, FindConfig(dir))
	})

	t.Run("prefers .yaml over later defaults", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".bonsai.yml"), []byte("analysis:\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".bonsai.yaml"), []byte("analysis:\n"), 0o644))
		assert.Equal(t, filepath.Join(dir, ".bonsai.yaml"), FindConfig(dir))
	})

	t.Run("falls back to primary default when none exist", func(t *testing.T) {
		dir := t.TempDir()
		assert.Equal(t, filepath.Join(dir, ".bonsai.yaml"), FindConfig(dir))
	})

	t.Run("empty dir means current directory", func(t *testing.T) {
		assert.Equal(t, ".bonsai.yaml", FindConfig(""))
	})
}

func TestReadLock(t *testing.T) {
	tests := []struct {
		name    string
		content string // empty means "no file"
		want    []string
	}{
		{name: "missing file", content: "", want: nil},
		{
			name:    "present list",
			content: "analysis:\n  lock:\n    - github.com/a/b\n    - golang.org/x/...\n",
			want:    []string{"github.com/a/b", "golang.org/x/..."},
		},
		{name: "no analysis section", content: "output: json\n", want: nil},
		{name: "analysis but no lock", content: "analysis:\n  top: 10\n", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cfg.yaml")
			if tt.content != "" {
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))
			}
			got, err := ReadLock(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadBuild(t *testing.T) {
	type want struct {
		lock       []string
		controlled []string
		unlock     []string
	}
	tests := []struct {
		name    string
		content string // empty means "no file"
		want    want
		wantErr require.ErrorAssertionFunc
	}{
		{name: "missing file", content: "", want: want{}},
		{name: "no analysis section", content: "output: json\n", want: want{}},
		{name: "analysis but no build lists", content: "analysis:\n  top: 10\n", want: want{}},
		{
			name: "all three populated",
			content: "analysis:\n" +
				"  lock:\n    - github.com/a/b\n" +
				"  controlled:\n    - github.com/me/...\n" +
				"  unlock:\n    - github.com/c/d\n",
			want: want{
				lock:       []string{"github.com/a/b"},
				controlled: []string{"github.com/me/..."},
				unlock:     []string{"github.com/c/d"},
			},
		},
		{
			name:    "only lock present",
			content: "analysis:\n  lock:\n    - github.com/a/b\n    - golang.org/x/...\n",
			want:    want{lock: []string{"github.com/a/b", "golang.org/x/..."}},
		},
		{
			name:    "malformed yaml",
			content: "analysis: [unterminated\n",
			wantErr: require.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr == nil {
				tt.wantErr = require.NoError
			}
			path := filepath.Join(t.TempDir(), "cfg.yaml")
			if tt.content != "" {
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))
			}

			lock, controlled, unlock, err := ReadBuild(path)
			tt.wantErr(t, err)
			if err != nil {
				return
			}
			assert.Equal(t, tt.want.lock, lock)
			assert.Equal(t, tt.want.controlled, controlled)
			assert.Equal(t, tt.want.unlock, unlock)
		})
	}
}

func TestWriteBuildRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, WriteBuild(path,
		[]string{"github.com/a/b"},
		[]string{"github.com/me/..."},
		[]string{"github.com/c/d"},
	))

	lock, controlled, unlock, err := ReadBuild(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"github.com/a/b"}, lock)
	assert.Equal(t, []string{"github.com/me/..."}, controlled)
	assert.Equal(t, []string{"github.com/c/d"}, unlock)
}

func TestWriteBuildSkipsEmptyKeys(t *testing.T) {
	// an empty list whose key is absent should not introduce an empty key, keeping configs
	// that only use `lock` free of noise.
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, WriteBuild(path, []string{"github.com/a/b"}, nil, nil))

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "lock:")
	assert.NotContains(t, s, "controlled:")
	assert.NotContains(t, s, "unlock:")
}

func TestWriteBuildPreservesComments(t *testing.T) {
	const original = `# bonsai config
output: json # keep this inline comment

analysis:
  # how many rows to show
  top: 25
  lock:
    - github.com/old/dep # replace me
`
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	require.NoError(t, WriteBuild(path,
		[]string{"github.com/new/dep"},
		[]string{"github.com/me/..."},
		nil,
	))

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "# bonsai config")
	assert.Contains(t, s, "keep this inline comment")
	assert.Contains(t, s, "# how many rows to show")
	assert.Contains(t, s, "top: 25")
	assert.Contains(t, s, "github.com/new/dep")
	assert.NotContains(t, s, "github.com/old/dep")
	assert.Contains(t, s, "controlled:")
}

func TestWriteLockCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, WriteLock(path, []string{"github.com/a/b", "github.com/c/d"}))

	got, err := ReadLock(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"github.com/a/b", "github.com/c/d"}, got)
}

func TestWriteLockPreservesComments(t *testing.T) {
	const original = `# bonsai config
output: json # keep this inline comment

analysis:
  # how many rows to show
  top: 25
  lock:
    - github.com/old/dep # replace me
`
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	require.NoError(t, WriteLock(path, []string{"github.com/new/dep"}))

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(out)

	// comments and unrelated keys survive.
	assert.Contains(t, s, "# bonsai config")
	assert.Contains(t, s, "keep this inline comment")
	assert.Contains(t, s, "# how many rows to show")
	assert.Contains(t, s, "top: 25")
	// the lock list is replaced.
	assert.Contains(t, s, "github.com/new/dep")
	assert.NotContains(t, s, "github.com/old/dep")

	got, err := ReadLock(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"github.com/new/dep"}, got)
}

func TestWriteLockEmptiesList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte("analysis:\n  lock:\n    - a\n    - b\n"), 0o644))
	require.NoError(t, WriteLock(path, nil))

	got, err := ReadLock(path)
	require.NoError(t, err)
	assert.Empty(t, got)
}
