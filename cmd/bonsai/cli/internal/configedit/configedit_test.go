package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadIgnore(t *testing.T) {
	tests := []struct {
		name    string
		content string // empty means "no file"
		want    []string
	}{
		{name: "missing file", content: "", want: nil},
		{
			name:    "present list",
			content: "analysis:\n  ignore:\n    - github.com/a/b\n    - golang.org/x/...\n",
			want:    []string{"github.com/a/b", "golang.org/x/..."},
		},
		{name: "no analysis section", content: "output: json\n", want: nil},
		{name: "analysis but no ignore", content: "analysis:\n  top: 10\n", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cfg.yaml")
			if tt.content != "" {
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))
			}
			got, err := ReadIgnore(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWriteIgnoreCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, WriteIgnore(path, []string{"github.com/a/b", "github.com/c/d"}))

	got, err := ReadIgnore(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"github.com/a/b", "github.com/c/d"}, got)
}

func TestWriteIgnorePreservesComments(t *testing.T) {
	const original = `# bonsai config
output: json # keep this inline comment

analysis:
  # how many rows to show
  top: 25
  ignore:
    - github.com/old/dep # replace me
`
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	require.NoError(t, WriteIgnore(path, []string{"github.com/new/dep"}))

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(out)

	// comments and unrelated keys survive.
	assert.Contains(t, s, "# bonsai config")
	assert.Contains(t, s, "keep this inline comment")
	assert.Contains(t, s, "# how many rows to show")
	assert.Contains(t, s, "top: 25")
	// the ignore list is replaced.
	assert.Contains(t, s, "github.com/new/dep")
	assert.NotContains(t, s, "github.com/old/dep")

	got, err := ReadIgnore(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"github.com/new/dep"}, got)
}

func TestWriteIgnoreEmptiesList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte("analysis:\n  ignore:\n    - a\n    - b\n"), 0o644))
	require.NoError(t, WriteIgnore(path, nil))

	got, err := ReadIgnore(path)
	require.NoError(t, err)
	assert.Empty(t, got)
}
