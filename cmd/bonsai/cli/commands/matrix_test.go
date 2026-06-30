package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestParsePlatform(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		extraTags []string
		want      bonsai.Platform
		wantErr   bool
	}{
		{name: "os/arch", in: "linux/amd64", want: bonsai.Platform{GOOS: "linux", GOARCH: "amd64"}},
		{name: "os/arch+tags", in: "linux/amd64+netgo,cgo", want: bonsai.Platform{GOOS: "linux", GOARCH: "amd64", Tags: []string{"netgo", "cgo"}}},
		{name: "extra tags appended", in: "darwin/arm64", extraTags: []string{"netgo"}, want: bonsai.Platform{GOOS: "darwin", GOARCH: "arm64", Tags: []string{"netgo"}}},
		{name: "missing arch", in: "linux", wantErr: true},
		{name: "empty arch", in: "linux/", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePlatform(tt.in, tt.extraTags)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveCells(t *testing.T) {
	configMatrix := []bonsai.Platform{{GOOS: "linux", GOARCH: "ppc64le"}}

	t.Run("--platform wins over the config matrix", func(t *testing.T) {
		opts := &matrixConfig{}
		opts.Platforms = []string{"darwin/arm64"}
		opts.Build.Matrix = configMatrix
		cfg := opts.Config()
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, []bonsai.Platform{{GOOS: "darwin", GOARCH: "arm64"}}, cells)
	})

	t.Run("config matrix used when no --platform", func(t *testing.T) {
		opts := &matrixConfig{}
		opts.Build.Matrix = configMatrix
		cfg := opts.Config()
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, configMatrix, cells)
	})

	t.Run("falls back to the default matrix", func(t *testing.T) {
		opts := &matrixConfig{}
		cfg := opts.Config()
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, defaultMatrix(), cells)
	})

	// goreleaser is resolved at config-load time (Build.PostLoad). When it's the only source,
	// resolveCells turns its cells into the matrix and clears the global build settings.
	t.Run("goreleaser import becomes the cells and clears build settings", func(t *testing.T) {
		opts := &matrixConfig{}
		opts.GoreleaserImport = &bonsai.GoreleaserMatrix{
			File:  ".goreleaser.yaml",
			Cells: []bonsai.Platform{{GOOS: "linux", GOARCH: "amd64"}},
		}
		cfg := opts.Config()
		cfg.Build = bonsai.BuildSettings{Tags: []string{"host"}} // PostLoad would have set this
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"linux/amd64"}, labelsOfCells(cells))
		assert.Equal(t, bonsai.BuildSettings{}, cfg.Build, "per-cell flags ride the cells, so global settings are cleared")
	})

	// goreleaser is on by default, so an explicit --platform must win over it, not error.
	t.Run("--platform wins over an auto-detected goreleaser matrix", func(t *testing.T) {
		opts := &matrixConfig{}
		opts.GoreleaserImport = &bonsai.GoreleaserMatrix{Cells: []bonsai.Platform{{GOOS: "linux", GOARCH: "amd64"}}}
		opts.Platforms = []string{"darwin/arm64"}
		cfg := opts.Config()
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, []bonsai.Platform{{GOOS: "darwin", GOARCH: "arm64"}}, cells)
	})

	// a zero-cell import (fangs allocates the nil pointer while walking config) must not be treated
	// as a real goreleaser matrix; fall through to the default set instead.
	t.Run("empty goreleaser import falls through to default", func(t *testing.T) {
		opts := &matrixConfig{}
		opts.GoreleaserImport = &bonsai.GoreleaserMatrix{} // non-nil but zero cells
		cfg := opts.Config()
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, defaultMatrix(), cells)
	})
}

func labelsOfCells(cells []bonsai.Platform) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.Label()
	}
	return out
}
