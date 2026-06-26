package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/configedit"
)

func TestSortedUnique(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "empty", in: nil, want: nil},
		{name: "only empties", in: []string{"", ""}, want: nil},
		{name: "sorts", in: []string{"c", "a", "b"}, want: []string{"a", "b", "c"}},
		{name: "dedupes and drops empty", in: []string{"b", "a", "b", "", "a"}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sortedUnique(tt.in))
		})
	}
}

func TestPersistInputs(t *testing.T) {
	t.Run("unchanged inputs are not written", func(t *testing.T) {
		// path points at a file that doesn't exist yet; if persistInputs writes nothing, it
		// stays absent.
		path := filepath.Join(t.TempDir(), ".bonsai.yaml")
		base := bonsai.ClassInputs{Locked: []string{"github.com/a/b"}}
		persistInputs(path, base, base)

		_, err := os.Stat(path)
		assert.True(t, os.IsNotExist(err), "no write expected when inputs are unchanged")
	})

	t.Run("changed inputs are written normalized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".bonsai.yaml")
		base := bonsai.ClassInputs{Locked: []string{"github.com/a/b"}}
		// final adds a lock and a controlled entry, with an unsorted dupe to prove normalization.
		final := bonsai.ClassInputs{
			Locked:     []string{"github.com/c/d", "github.com/a/b", "github.com/a/b"},
			Controlled: []string{"github.com/me/..."},
		}
		persistInputs(path, base, final)

		lock, controlled, unlock, err := configedit.ReadBuild(path)
		require.NoError(t, err)
		assert.Equal(t, []string{"github.com/a/b", "github.com/c/d"}, lock)
		assert.Equal(t, []string{"github.com/me/..."}, controlled)
		assert.Empty(t, unlock)
	})

	t.Run("empty path is a no-op", func(t *testing.T) {
		// should not panic or attempt a write with no file to target.
		persistInputs("", bonsai.ClassInputs{}, bonsai.ClassInputs{Locked: []string{"github.com/a/b"}})
	})
}

func TestSameInputs(t *testing.T) {
	tests := []struct {
		name string
		a    bonsai.ClassInputs
		b    bonsai.ClassInputs
		want bool
	}{
		{
			name: "both empty",
			want: true,
		},
		{
			name: "equal across all lists",
			a:    bonsai.ClassInputs{Controlled: []string{"a"}, Locked: []string{"b"}, Unlock: []string{"c"}},
			b:    bonsai.ClassInputs{Controlled: []string{"a"}, Locked: []string{"b"}, Unlock: []string{"c"}},
			want: true,
		},
		{
			name: "differ in locked",
			a:    bonsai.ClassInputs{Locked: []string{"a"}},
			b:    bonsai.ClassInputs{Locked: []string{"a", "b"}},
			want: false,
		},
		{
			name: "differ in controlled",
			a:    bonsai.ClassInputs{Controlled: []string{"a"}},
			b:    bonsai.ClassInputs{Controlled: []string{"z"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sameInputs(tt.a, tt.b))
		})
	}
}
