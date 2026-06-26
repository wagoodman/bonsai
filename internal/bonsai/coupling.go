package bonsai

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
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

// ImportSite is one import statement in first-party code that references a dependency — the
// concrete edit location an agent needs to sever (or rewrite away) a dependency. File is
// relative to the main module directory. Unlike Coupling, which keeps only counts, these are
// the actual statements to change.
type ImportSite struct {
	File        string `json:"file"`        // source file, relative to the main module directory
	Line        int    `json:"line"`        // line of the import statement
	FromPackage string `json:"fromPackage"` // first-party package import path doing the importing
	ImportPath  string `json:"importPath"`  // the dependency package being imported
}

// importSitesForModule walks the main module source tree and returns every import statement
// that references a package of module, along with a map from each imported dependency package
// to the first-party packages that import it (for entry-package attribution). Scope matches
// scanCoupling: the main module tree only, skipping tests, vendor, and dotted dirs. Best-effort
// — unparseable files are skipped. Returns nil, nil when there is no source (e.g. prebuilt
// binary with no resolved module dir).
func (g *buildGraph) importSitesForModule(module string) ([]ImportSite, map[string][]string) {
	if g.mainModDir == "" {
		return nil, nil
	}
	var sites []ImportSite
	importedBy := map[string]map[string]bool{} // dep import path -> set of first-party packages

	fset := token.NewFileSet()
	_ = filepath.WalkDir(g.mainModDir, func(path string, d fs.DirEntry, err error) error {
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
		g.collectImportSites(fset, path, module, &sites, importedBy)
		return nil
	})

	sort.Slice(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Line < sites[j].Line
	})
	out := map[string][]string{}
	for ip, set := range importedBy {
		pkgs := make([]string, 0, len(set))
		for p := range set {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		out[ip] = pkgs
	}
	return sites, out
}

// collectImportSites parses one source file and appends every import of module to sites,
// recording the importing first-party package in importedBy. Best-effort: unparseable files
// are silently skipped.
func (g *buildGraph) collectImportSites(fset *token.FileSet, path, module string, sites *[]ImportSite, importedBy map[string]map[string]bool) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ImportsOnly)
	if err != nil {
		return // tolerate unparseable files; this is best-effort analysis
	}
	from := g.importPathOfDir(filepath.Dir(path))
	for _, imp := range file.Imports {
		ip, uerr := strconv.Unquote(imp.Path.Value)
		if uerr != nil || g.moduleOfPkg[ip] != module {
			continue
		}
		rel, rerr := filepath.Rel(g.mainModDir, path)
		if rerr != nil {
			rel = path
		}
		*sites = append(*sites, ImportSite{
			File:        filepath.ToSlash(rel),
			Line:        fset.Position(imp.Path.Pos()).Line,
			FromPackage: from,
			ImportPath:  ip,
		})
		if importedBy[ip] == nil {
			importedBy[ip] = map[string]bool{}
		}
		importedBy[ip][from] = true
	}
}

// importPathOfDir maps a directory under the main module tree to its package import path. It
// roots the path at the most specific module whose directory contains dir, so a package living
// in a nested module under the main tree gets that module's import path rather than one wrongly
// assembled from the main module root. Falls back to the main module when no module directory is
// known (e.g. the manually-built graphs in tests).
func (g *buildGraph) importPathOfDir(dir string) string {
	bestPath, bestDir := g.mainModule, g.mainModDir
	for path, m := range g.allModules {
		if m.Dir == "" || len(m.Dir) <= len(bestDir) || !dirContains(m.Dir, dir) {
			continue
		}
		bestPath, bestDir = path, m.Dir
	}
	rel, err := filepath.Rel(bestDir, dir)
	if err != nil || rel == "." {
		return bestPath
	}
	return bestPath + "/" + filepath.ToSlash(rel)
}

// dirContains reports whether child is parent or lives beneath it.
func dirContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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
