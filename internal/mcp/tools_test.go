package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestMatrixCellsPrecedence(t *testing.T) {
	gl := &bonsai.GoreleaserMatrix{Cells: []bonsai.Platform{{GOOS: "linux", GOARCH: "arm64"}}}

	t.Run("explicit platforms win over everything", func(t *testing.T) {
		b := options.Build{Matrix: []bonsai.Platform{{GOOS: "darwin", GOARCH: "amd64"}}, GoreleaserImport: gl}
		cells, fromGl, err := matrixCells(b, []string{"windows/amd64+netgo"}, []string{"foo"})
		require.NoError(t, err)
		assert.False(t, fromGl)
		require.Len(t, cells, 1)
		assert.Equal(t, "windows", cells[0].GOOS)
		assert.ElementsMatch(t, []string{"netgo", "foo"}, cells[0].Tags)
	})

	t.Run("configured matrix beats goreleaser", func(t *testing.T) {
		b := options.Build{Matrix: []bonsai.Platform{{GOOS: "darwin", GOARCH: "amd64"}}, GoreleaserImport: gl}
		cells, fromGl, err := matrixCells(b, nil, nil)
		require.NoError(t, err)
		assert.False(t, fromGl)
		require.Len(t, cells, 1)
		assert.Equal(t, "darwin", cells[0].GOOS)
	})

	t.Run("goreleaser import when no matrix", func(t *testing.T) {
		b := options.Build{GoreleaserImport: gl}
		cells, fromGl, err := matrixCells(b, nil, nil)
		require.NoError(t, err)
		assert.True(t, fromGl) // signals the caller to drop the global build settings
		assert.Equal(t, "arm64", cells[0].GOARCH)
	})

	t.Run("default set when nothing declared", func(t *testing.T) {
		cells, fromGl, err := matrixCells(options.Build{}, nil, nil)
		require.NoError(t, err)
		assert.False(t, fromGl)
		assert.Len(t, cells, 3) // linux/amd64, darwin/arm64, windows/amd64
	})

	t.Run("invalid platform errors", func(t *testing.T) {
		_, _, err := matrixCells(options.Build{}, []string{"notaplatform"}, nil)
		assert.Error(t, err)
	})
}
