package bonsai

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// applyReferenceEdges rewrites the build graph's per-package import edges using the
// linker's `-dumpdep` symbol-dependency dump, so reachability reflects what actually
// linked into the binary (post dead-code elimination) rather than source-level imports.
//
// The dump is a stream of `from -> to` symbol edges. We collapse each edge to the packages
// owning its endpoints and keep only edges between packages already in the build graph.
// The result is a strictly tighter reference graph: imports the linker dropped disappear,
// so prune/tree-shake estimates stop over-counting. It returns the number of package edges
// applied; zero means the dump was empty or unparseable and the caller should keep the
// `go list` import edges as a fallback.
func applyReferenceEdges(g *buildGraph, dumpdepPath string) (int, error) {
	f, err := os.Open(dumpdepPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return readReferenceEdges(g, f)
}

// readReferenceEdges parses a -dumpdep stream and rewrites g's per-package import edges
// from it; see applyReferenceEdges.
func readReferenceEdges(g *buildGraph, r io.Reader) (int, error) {
	// the main package's symbols are named `main.X`, not by import path; map them back to
	// the real entrypoint import path so they connect to the rest of the graph.
	mainImport := ""
	if len(g.rootPackages) > 0 {
		mainImport = g.rootPackages[0]
	}

	edges := map[string]map[string]bool{}
	add := func(from, to string) {
		if edges[from] == nil {
			edges[from] = map[string]bool{}
		}
		edges[from][to] = true
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		lhs, rhs, ok := strings.Cut(line, " -> ")
		if !ok {
			continue
		}
		from := symbolPackage(lhs, mainImport)
		to := symbolPackage(rhs, mainImport)
		if from == "" || to == "" || from == to {
			continue
		}
		// only edges between packages the build graph knows about; everything else is
		// runtime/compiler scaffolding we can't attribute to a module.
		fromPkg, toPkg := g.packages[from], g.packages[to]
		if fromPkg == nil || toPkg == nil {
			continue
		}
		// a standard-library package cannot import a third-party module in real source; such
		// an edge is a symbol-attribution artifact (e.g. a generic instantiated with an
		// external type whose symbol is named for the stdlib package that defines the generic).
		// Keeping it would falsely pin the external module as always-reachable, so drop it.
		if fromPkg.Standard && !toPkg.Standard {
			continue
		}
		add(from, to)
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("reading dumpdep: %w", err)
	}

	n := 0
	for _, p := range g.packages {
		refs := edges[p.ImportPath]
		p.Imports = make([]string, 0, len(refs))
		for to := range refs {
			p.Imports = append(p.Imports, to)
		}
		n += len(refs)
	}
	return n, nil
}

// symbolPackage maps a linker symbol name to the import path of its owning package, or ""
// for symbols that don't belong to a real package (compiler-generated, type metadata) or
// are auxiliary symbols whose cross-package reference edges are linker bookkeeping rather
// than genuine calls. The main package's `main.` symbols are remapped to mainImport.
func symbolPackage(sym, mainImport string) string {
	sym = strings.TrimSpace(sym)
	if isAuxSymbol(sym) {
		return ""
	}
	pkg := packageOfSymbol(sym)
	if pkg == pkgGenerated {
		return ""
	}
	if pkg == pkgMain {
		return mainImport
	}
	return pkg
}

// auxSymbolMarkers are the trailing name segments of compiler-emitted auxiliary symbols
// (argument metadata, stack maps, deferred-call records). The linker records edges into
// these from unrelated functions; collapsing such edges to packages invents spurious
// cross-package references, so we drop any edge touching one.
var auxSymbolMarkers = []string{
	".argliveinfo",
	".args_stackmap",
	".stkobj",
	".opendefer",
}

func isAuxSymbol(sym string) bool {
	for _, m := range auxSymbolMarkers {
		if strings.HasSuffix(sym, m) {
			return true
		}
	}
	// arginfo symbols are suffixed with a number (arginfo0, arginfo1, ...).
	if i := strings.LastIndex(sym, ".arginfo"); i >= 0 {
		rest := sym[i+len(".arginfo"):]
		if rest != "" && isAllDigits(rest) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
