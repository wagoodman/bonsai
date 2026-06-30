package options

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func ptr[T any](v T) *T { return &v }

func TestBuildPostLoad(t *testing.T) {
	writeGoreleaser := func(t *testing.T, body string) string {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".goreleaser.yaml"), []byte(body), 0o644))
		return dir
	}

	t.Run("no goreleaser file degrades silently (default-on)", func(t *testing.T) {
		// Goreleaser nil = on by default; an absent .goreleaser.yaml must not error.
		o := &Build{Dir: t.TempDir(), BuildSettings: bonsai.BuildSettings{Tags: []string{"kept"}}}
		require.NoError(t, o.PostLoad())
		assert.Nil(t, o.GoreleaserImport)
		assert.Equal(t, []string{"kept"}, o.BuildSettings.Tags, "analysis.build untouched when no goreleaser")
	})

	t.Run("default-on resolves a present file", func(t *testing.T) {
		dir := writeGoreleaser(t, "builds:\n  - dir: ./cmd/app\n    goos: [linux]\n    goarch: [amd64]\n")
		o := &Build{Dir: dir} // Goreleaser nil → on
		require.NoError(t, o.PostLoad())
		require.NotNil(t, o.GoreleaserImport)
		assert.Equal(t, "./cmd/app", o.Target)
	})

	t.Run("explicitly disabled ignores a present file", func(t *testing.T) {
		dir := writeGoreleaser(t, "builds:\n  - dir: ./cmd/app\n    goos: [linux]\n    goarch: [amd64]\n")
		o := &Build{Goreleaser: ptr(false), Dir: dir, BuildSettings: bonsai.BuildSettings{Tags: []string{"kept"}}}
		require.NoError(t, o.PostLoad())
		assert.Nil(t, o.GoreleaserImport)
		assert.Equal(t, []string{"kept"}, o.BuildSettings.Tags)
	})

	t.Run("folds the host build into BuildSettings and target", func(t *testing.T) {
		// a single build covering every host: same tags/env/flags across its cells.
		dir := writeGoreleaser(t, "builds:\n  - dir: ./cmd/app\n    env: [CGO_ENABLED=0]\n    flags: [-trimpath]\n    tags: [netgo]\n")
		o := &Build{Goreleaser: ptr(true), Dir: dir, BuildSettings: bonsai.BuildSettings{Tags: []string{"replaced"}}}
		require.NoError(t, o.PostLoad())
		require.NotNil(t, o.GoreleaserImport, "the resolved import is stashed for the matrix subject")
		assert.Equal(t, []string{"netgo"}, o.BuildSettings.Tags, "goreleaser wins over analysis.build")
		assert.Equal(t, map[string]string{"CGO_ENABLED": "0"}, o.BuildSettings.Env)
		assert.Equal(t, "-trimpath", o.BuildSettings.Args)
		assert.Equal(t, "./cmd/app", o.Target)
	})

	t.Run("explicit target wins over the goreleaser one", func(t *testing.T) {
		dir := writeGoreleaser(t, "builds:\n  - dir: ./cmd/app\n    goos: [linux]\n    goarch: [amd64]\n")
		o := &Build{Goreleaser: ptr(true), Dir: dir, Target: "./cmd/other"}
		require.NoError(t, o.PostLoad())
		assert.Equal(t, "./cmd/other", o.Target)
	})

	t.Run("an explicit analysis.matrix skips goreleaser (manual mode)", func(t *testing.T) {
		dir := writeGoreleaser(t, "builds:\n  - dir: ./cmd/app\n    goos: [linux]\n    goarch: [amd64]\n")
		o := &Build{Dir: dir, Matrix: []bonsai.Platform{{GOOS: "linux", GOARCH: "amd64"}}}
		require.NoError(t, o.PostLoad(), "no error: the explicit matrix just wins")
		assert.Nil(t, o.GoreleaserImport, "goreleaser not pulled when a matrix is declared")
	})

	t.Run("a present-but-broken goreleaser file is a real error", func(t *testing.T) {
		dir := writeGoreleaser(t, "builds: [: : :")
		o := &Build{Goreleaser: ptr(true), Dir: dir}
		assert.Error(t, o.PostLoad())
	})

	t.Run("PostLoad resets a stray import pointer", func(t *testing.T) {
		// mimics fangs allocating the nil field during config traversal: PostLoad must clear it.
		o := &Build{Goreleaser: ptr(false), Dir: t.TempDir(), GoreleaserImport: &bonsai.GoreleaserMatrix{}}
		require.NoError(t, o.PostLoad())
		assert.Nil(t, o.GoreleaserImport)
	})
}
