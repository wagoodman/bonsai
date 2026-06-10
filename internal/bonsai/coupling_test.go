package bonsai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPkgAlias(t *testing.T) {
	tests := []struct {
		name       string
		importPath string
		want       string
	}{
		{name: "simple last element", importPath: "github.com/spf13/cobra", want: "cobra"},
		{name: "single element", importPath: "fmt", want: "fmt"},
		{name: "deep path", importPath: "github.com/aws/aws-sdk-go-v2/service/s3", want: "s3"},
		{name: "major-version suffix uses the element before it", importPath: "github.com/go-redis/redis/v8", want: "redis"},
		{name: "v-prefixed but not a version", importPath: "github.com/spf13/viper", want: "viper"},
		{name: "bare v is not a version", importPath: "example.com/v", want: "v"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, defaultPkgAlias(tt.importPath))
		})
	}
}

// writeGoFile writes content to a (possibly nested) path under root, creating parent dirs.
func writeGoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestScanCoupling(t *testing.T) {
	dir := t.TempDir()

	// root package: imports bravo (real package name "bee", differs from its path) and charlie
	// (under an explicit alias), exercising both alias-resolution branches. fmt is stdlib (no
	// module) and must be ignored.
	writeGoFile(t, dir, "main.go", `package main

import (
	"fmt"

	"github.com/dep/bravo"
	cee "github.com/dep/charlie"
)

func main() {
	bee.B()
	bee.Two()
	cee.C()
	fmt.Println()
}
`)
	// a second first-party package importing bravo: bumps bravo's importing-package count.
	writeGoFile(t, dir, "sub/sub.go", `package sub

import "github.com/dep/bravo"

func Use() { bee.B() }
`)
	// these must all be skipped: _test.go files, vendored code, and testdata trees.
	writeGoFile(t, dir, "main_test.go", `package main

import "github.com/dep/charlie"

func extra() { charlie.Ignored() }
`)
	writeGoFile(t, dir, "vendor/v/v.go", `package v

import "github.com/dep/bravo"

func V() { bravo.Vendored() }
`)
	writeGoFile(t, dir, "testdata/t.go", `package td

import "github.com/dep/bravo"

func T() { bravo.TestData() }
`)

	g := &buildGraph{
		mainModule: "example.com/app",
		mainModDir: dir,
		moduleOfPkg: map[string]string{
			"github.com/dep/bravo":   "github.com/dep/bravo",
			"github.com/dep/charlie": "github.com/dep/charlie",
			"fmt":                    "",
		},
		packages: map[string]*listPackage{
			"github.com/dep/bravo":   {ImportPath: "github.com/dep/bravo", Name: "bee"},
			"github.com/dep/charlie": {ImportPath: "github.com/dep/charlie", Name: "charlie"},
			"fmt":                    {ImportPath: "fmt", Name: "fmt"},
		},
	}

	got, err := scanCoupling(g)
	require.NoError(t, err)

	want := map[string]*Coupling{
		// imported by main.go and sub/sub.go (2 sites, 2 packages); symbols bee.B, bee.Two.
		"github.com/dep/bravo": {ImportingPackages: 2, ImportSites: 2, DistinctSymbols: 2},
		// imported once under alias cee; the _test.go usage of charlie is excluded.
		"github.com/dep/charlie": {ImportingPackages: 1, ImportSites: 1, DistinctSymbols: 1},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("coupling mismatch (-want +got):\n%s", diff)
	}
}
