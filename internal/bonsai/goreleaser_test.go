package bonsai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDedummy(t *testing.T) {
	assert.Equal(t, "-X main.version=0", dedummy("-X main.version={{.Version}}"))
	assert.Equal(t, "-X a=0 -X b=0", dedummy("-X a={{ .Commit }} -X b={{.Date}}"))
	assert.Equal(t, "-trimpath", dedummy("-trimpath"))
}

func TestBuildArgs(t *testing.T) {
	// flags pass through; ldflags flatten (block scalars carry newlines) into one quoted token,
	// templates dummied, so splitArgs keeps the -ldflags value together.
	args := buildArgs(glStringList{"-trimpath"}, glStringList{"-s\n-w\n-X main.version={{.Version}}"})
	assert.Equal(t, `-trimpath -ldflags="-s -w -X main.version=0"`, args)
	assert.Equal(t, []string{"-trimpath", "-ldflags=-s -w -X main.version=0"}, splitArgs(args))
}

func TestCellsForBuild(t *testing.T) {
	t.Run("goos x goarch minus ignore", func(t *testing.T) {
		b := glBuild{
			Goos:   []string{"linux", "darwin"},
			Goarch: []string{"amd64", "arm64"},
			Ignore: []glIgnore{{Goos: "darwin", Goarch: "amd64"}},
		}
		got := labelsOf(cellsForBuild(b))
		assert.Equal(t, []string{"linux/amd64", "linux/arm64", "darwin/arm64"}, got)
	})

	t.Run("explicit targets win", func(t *testing.T) {
		b := glBuild{Targets: []string{"linux_amd64", "windows_arm64"}}
		assert.Equal(t, []string{"linux/amd64", "windows/arm64"}, labelsOf(cellsForBuild(b)))
	})

	t.Run("tags and env ride the cells", func(t *testing.T) {
		b := glBuild{Goos: []string{"linux"}, Goarch: []string{"amd64"}, Tags: glStringList{"netgo"}, Env: []string{"CGO_ENABLED=0"}}
		cells := cellsForBuild(b)
		require.Len(t, cells, 1)
		assert.Equal(t, []string{"netgo"}, cells[0].Tags)
		assert.Equal(t, map[string]string{"CGO_ENABLED": "0"}, cells[0].Env)
	})
}

// TestFromGoreleaser exercises the end-to-end import: three builds for the same binary union into
// one matrix, ldflags-as-block-scalar parse, env rides each cell, and the target is derived.
func TestFromGoreleaser(t *testing.T) {
	dir := t.TempDir()
	yaml := `
builds:
  - id: nix
    dir: ./cmd/app
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags: |
      -s
      -w
      -X main.version={{.Version}}
  - id: win
    dir: ./cmd/app
    env: [CGO_ENABLED=0]
    goos: [windows]
    goarch: [amd64]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".goreleaser.yaml"), []byte(yaml), 0o644))

	imp, err := FromGoreleaser(dir)
	require.NoError(t, err)

	assert.Equal(t, 2, imp.Builds)
	assert.Equal(t, "./cmd/app", imp.Target)
	assert.Equal(t, []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"}, labelsOf(imp.Cells))
	for _, c := range imp.Cells {
		assert.Equal(t, map[string]string{"CGO_ENABLED": "0"}, c.Env, c.Label())
	}
	// the linux cell carries the dummied ldflags; windows had none.
	assert.Contains(t, imp.Cells[0].Args, `-ldflags="-s -w -X main.version=0"`)
}

func TestFromGoreleaserMissing(t *testing.T) {
	_, err := FromGoreleaser(t.TempDir())
	assert.Error(t, err, "no goreleaser file is an error")
}

func labelsOf(cells []Platform) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.Label()
	}
	return out
}
