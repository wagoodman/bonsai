package bonsai

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// Coupling measures how deeply first-party code is wired into a dependency module —
// a proxy for how hard it would be to remove. Lower numbers => easier to prune.
type Coupling struct {
	ImportingPackages int `json:"importingPackages"` // distinct first-party packages that import the module
	ImportSites       int `json:"importSites"`       // total import statements referencing the module
	DistinctSymbols   int `json:"distinctSymbols"`   // distinct selector symbols used (approximate, alias-based)
}

// scanCoupling walks the main module source tree (skipping tests, vendored, and
// generated-by-build dirs) and tallies, per dependency module, how first-party code
// references it. Symbol counting is a syntactic approximation: it matches
// `alias.Symbol` selector expressions against the file's imports without full type
// resolution, which is cheap (stdlib only) and good enough to rank coupling depth.
func scanCoupling(g *buildGraph) (map[string]*Coupling, error) { //nolint:funlen,gocognit // single graph sweep with interdependent accumulators; splitting obscures the flow
	out := map[string]*Coupling{}
	get := func(mod string) *Coupling {
		c := out[mod]
		if c == nil {
			c = &Coupling{}
			out[mod] = c
		}
		return c
	}
	// track distinct importing-package and symbol sets per module across files.
	importingPkgs := map[string]map[string]bool{}
	symbolSets := map[string]map[string]bool{}

	fset := token.NewFileSet()
	root := g.mainModDir
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "vendor" || base == "testdata" || (strings.HasPrefix(base, ".") && base != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil // tolerate unparseable files; this is best-effort analysis
		}
		// containing package import path, derived from the file's directory.
		pkgDir := filepath.Dir(path)

		// map local import alias -> dependency module for this file.
		aliasToModule := map[string]string{}
		for _, imp := range file.Imports {
			ip, _ := strconv.Unquote(imp.Path.Value)
			mod := g.moduleOfPkg[ip]
			if mod == "" || mod == g.mainModule {
				continue
			}
			c := get(mod)
			c.ImportSites++
			if importingPkgs[mod] == nil {
				importingPkgs[mod] = map[string]bool{}
			}
			importingPkgs[mod][pkgDir] = true

			// prefer the real package-clause name from `go list` (exact); fall back to
			// the path-derived guess only if the package isn't in the build graph.
			local := defaultPkgAlias(ip)
			if p := g.packages[ip]; p != nil && p.Name != "" {
				local = p.Name
			}
			if imp.Name != nil {
				local = imp.Name.Name
			}
			if local != "_" && local != "." {
				aliasToModule[local] = mod
			}
		}
		if len(aliasToModule) == 0 {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			mod, ok := aliasToModule[ident.Name]
			if !ok {
				return true
			}
			if symbolSets[mod] == nil {
				symbolSets[mod] = map[string]bool{}
			}
			symbolSets[mod][ident.Name+"."+sel.Sel.Name] = true
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	for mod, c := range out {
		c.ImportingPackages = len(importingPkgs[mod])
		c.DistinctSymbols = len(symbolSets[mod])
	}
	return out, nil
}

// defaultPkgAlias guesses the local name of an import path when no explicit alias is
// given. This is a heuristic (the real name comes from the package clause), but the
// last path element is correct for the overwhelming majority of packages.
func defaultPkgAlias(importPath string) string {
	base := importPath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	// strip major-version suffixes like v2, v3.
	if len(base) >= 2 && base[0] == 'v' {
		allDigits := true
		for _, r := range base[1:] {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			// use the element before the version component.
			if i := strings.LastIndex(importPath[:strings.LastIndex(importPath, "/")], "/"); i >= 0 {
				return importPath[i+1 : strings.LastIndex(importPath, "/")]
			}
		}
	}
	return base
}
