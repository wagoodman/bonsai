package bonsai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

	t.Run("malformed targets are dropped", func(t *testing.T) {
		// no goarch part, no goos part, or no separator → not a buildable cell.
		b := glBuild{Targets: []string{"linux", "linux_", "_amd64", "linux_amd64"}}
		assert.Equal(t, []string{"linux/amd64"}, labelsOf(cellsForBuild(b)))
	})

	t.Run("no goos/goarch/targets falls back to the default set", func(t *testing.T) {
		got := labelsOf(cellsForBuild(glBuild{}))
		assert.Equal(t, []string{
			"linux/amd64", "linux/arm64",
			"darwin/amd64", "darwin/arm64",
			"windows/amd64", "windows/arm64",
		}, got)
	})

	t.Run("wildcard ignore drops a whole goos", func(t *testing.T) {
		// an ignore rule with an empty field matches any value for that field.
		b := glBuild{
			Goos:   []string{"linux", "windows"},
			Goarch: []string{"amd64", "arm64"},
			Ignore: []glIgnore{{Goos: "windows"}}, // empty goarch → all windows arches
		}
		assert.Equal(t, []string{"linux/amd64", "linux/arm64"}, labelsOf(cellsForBuild(b)))
	})

	t.Run("tags and env ride the cells", func(t *testing.T) {
		b := glBuild{Goos: []string{"linux"}, Goarch: []string{"amd64"}, Tags: glStringList{"netgo"}, Env: []string{"CGO_ENABLED=0"}}
		cells := cellsForBuild(b)
		require.Len(t, cells, 1)
		assert.Equal(t, []string{"netgo"}, cells[0].Tags)
		assert.Equal(t, map[string]string{"CGO_ENABLED": "0"}, cells[0].Env)
	})
}

func TestGlStringListUnmarshal(t *testing.T) {
	var b glBuild
	// the sequence form of flags/ldflags/tags (the block-scalar form is covered by TestFromGoreleaser).
	require.NoError(t, yaml.Unmarshal([]byte("flags: [-trimpath, -v]\nldflags: ['-s', '-w']\ntags: [netgo, osusergo]\n"), &b))
	assert.Equal(t, glStringList{"-trimpath", "-v"}, b.Flags)
	assert.Equal(t, glStringList{"-s", "-w"}, b.Ldflags)
	assert.Equal(t, glStringList{"netgo", "osusergo"}, b.Tags)

	// a mapping where a string/list is expected is a misconfiguration, not silently empty.
	err := yaml.Unmarshal([]byte("ldflags:\n  not: a-list\n"), &b)
	assert.Error(t, err)
}

func TestParseEnv(t *testing.T) {
	// "KEY=" keeps an empty value; "=VALUE" (no key) and "FOO" (no separator) are dropped.
	got := parseEnv([]string{"CGO_ENABLED=0", "EMPTY=", "=orphan", "NOSEP", " SPACED = x "})
	assert.Equal(t, map[string]string{"CGO_ENABLED": "0", "EMPTY": "", "SPACED": " x "}, got)
	assert.Nil(t, parseEnv(nil))
	assert.Nil(t, parseEnv([]string{"=only-orphans"}))
}

func TestDedummyTags(t *testing.T) {
	// a single entry may itself be space-separated, and templates are dummied.
	assert.Equal(t, []string{"netgo", "osusergo"}, dedummyTags([]string{"netgo osusergo"}))
	assert.Equal(t, []string{"v0"}, dedummyTags([]string{"v{{.Version}}"}))
	assert.Nil(t, dedummyTags(nil))
}

func TestGlTarget(t *testing.T) {
	assert.Equal(t, "", glTarget("", ""))
	assert.Equal(t, "./cmd/app", glTarget("./cmd/app", ""))                 // dir only
	assert.Equal(t, "./cmd/app/main.go", glTarget("", "./cmd/app/main.go")) // main only
	assert.Equal(t, "cmd/app/main.go", glTarget("cmd/app", "main.go"))      // dir + main joined
	assert.Equal(t, "./cmd/app/main.go", glTarget("./cmd/app", "main.go"))  // ./ prefix preserved
}

func TestHostBuild(t *testing.T) {
	// a config where each build (hence cell) carries distinct settings, so the host pick is visible.
	g := GoreleaserMatrix{
		Target: "./cmd/app",
		Cells: []Platform{
			{GOOS: "linux", GOARCH: "amd64", Tags: []string{"netgo"}, Env: map[string]string{"CGO_ENABLED": "0"}, Args: "-trimpath"},
			{GOOS: "darwin", GOARCH: "arm64", Tags: []string{"cgo"}, Env: map[string]string{"CGO_ENABLED": "1"}},
		},
	}

	t.Run("host match wins", func(t *testing.T) {
		b, target := g.HostBuild("darwin", "arm64")
		assert.Equal(t, "./cmd/app", target)
		assert.Equal(t, BuildSettings{Tags: []string{"cgo"}, Env: map[string]string{"CGO_ENABLED": "1"}}, b)
	})

	t.Run("no host match falls back to the first cell", func(t *testing.T) {
		b, _ := g.HostBuild("windows", "386")
		assert.Equal(t, []string{"netgo"}, b.Tags)
		assert.Equal(t, "-trimpath", b.Args)
	})
}

func TestPlatformKey(t *testing.T) {
	base := platformKey(Platform{GOOS: "linux", GOARCH: "amd64"})
	// equal cells collide (so the union dedups them); a differing env makes a distinct key.
	assert.Equal(t, base, platformKey(Platform{GOOS: "linux", GOARCH: "amd64"}))
	assert.NotEqual(t, base, platformKey(Platform{GOOS: "linux", GOARCH: "amd64", Env: map[string]string{"CGO_ENABLED": "1"}}))
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

func TestFromGoreleaserYmlExtension(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".goreleaser.yml"),
		[]byte("builds:\n  - goos: [linux]\n    goarch: [amd64]\n"), 0o644))
	imp, err := FromGoreleaser(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"linux/amd64"}, labelsOf(imp.Cells))
}

func TestFromGoreleaserErrors(t *testing.T) {
	write := func(t *testing.T, body string) string {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".goreleaser.yaml"), []byte(body), 0o644))
		return dir
	}

	t.Run("invalid yaml", func(t *testing.T) {
		_, err := FromGoreleaser(write(t, "builds: [: : :"))
		assert.Error(t, err)
	})
	t.Run("no builds", func(t *testing.T) {
		_, err := FromGoreleaser(write(t, "version: 2\n"))
		assert.ErrorContains(t, err, "no builds")
	})
	t.Run("builds present but no buildable cells", func(t *testing.T) {
		// only malformed targets → every cell dropped → no cells produced.
		_, err := FromGoreleaser(write(t, "builds:\n  - targets: [linux, _amd64]\n"))
		assert.ErrorContains(t, err, "no build cells")
	})
}

func labelsOf(cells []Platform) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.Label()
	}
	return out
}
