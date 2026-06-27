package commands

import (
	"os"
	"path/filepath"
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

	t.Run("goreleaser is mutually exclusive with the matrix", func(t *testing.T) {
		opts := &matrixConfig{}
		opts.Goreleaser = true
		opts.Build.Matrix = configMatrix
		cfg := opts.Config()
		_, err := resolveCells(opts, &cfg)
		assert.ErrorContains(t, err, "mutually exclusive")
	})

	t.Run("goreleaser import fills the build target", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".goreleaser.yaml"),
			[]byte("builds:\n  - dir: ./cmd/app\n    goos: [linux]\n    goarch: [amd64]\n"), 0o644))
		opts := &matrixConfig{}
		opts.Goreleaser = true
		opts.Dir = dir
		cfg := opts.Config()
		cells, err := resolveCells(opts, &cfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"linux/amd64"}, labelsOfCells(cells))
		assert.Equal(t, "./cmd/app", cfg.Target, "the target comes from the goreleaser build")
		assert.Equal(t, bonsai.BuildSettings{}, cfg.Build, "global build settings don't apply to goreleaser cells")
	})
}

func labelsOfCells(cells []bonsai.Platform) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.Label()
	}
	return out
}
