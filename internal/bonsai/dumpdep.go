package bonsai

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// applyReferenceEdges narrows the build graph's per-package import edges to what actually
// survived dead-code elimination, using the linker's `-dumpdep` symbol-dependency dump.
//
// The dump is a stream of `from -> to` symbol edges, but it is NOT a complete reference graph:
// the linker records the edge into a symbol only the first time that symbol is marked reachable
// (the print lives inside the deadcode pass's "not yet reachable" branch), so a dependency
// reached through two importers shows only one of those edges. Using it directly as the import
// graph would collapse every shared dependency onto a single arbitrary importer and wreck the
// retained-size attribution that is the whole point of the tool.
//
// What the dump IS reliable for: which packages survived DCE. Every reachable symbol appears as
// an endpoint, so the set of packages owning a dump endpoint is exactly the live set. So we use
// the dump only to derive that live set, then keep the COMPLETE `go list` import edges
// restricted to live packages — edges into DCE-dropped packages disappear (target not live),
// while shared deps keep every importer edge. It returns the number of package edges kept; zero
// means the dump was empty or unparseable and the caller should keep the `go list` edges as-is.
func applyReferenceEdges(g *buildGraph, dumpdepPath string) (int, error) {
	f, err := os.Open(dumpdepPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return readReferenceEdges(g, f)
}

// readReferenceEdges parses a -dumpdep stream and narrows g's per-package import edges to the
// live (post-DCE) packages it witnesses; see applyReferenceEdges.
func readReferenceEdges(g *buildGraph, r io.Reader) (int, error) {
	// the main package's symbols are named `main.X`, not by import path; map them back to
	// the real entrypoint import path so they're attributed to the entrypoint package.
	mainImport := ""
	if len(g.rootPackages) > 0 {
		mainImport = g.rootPackages[0]
	}

	// live = packages with at least one symbol that survived DCE, witnessed as a dump endpoint.
	live := map[string]bool{}
	noteLive := func(sym string) {
		if pkg := symbolPackage(sym, mainImport); pkg != "" {
			if _, known := g.packages[pkg]; known {
				live[pkg] = true
			}
		}
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
		noteLive(lhs)
		noteLive(rhs)
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("reading dumpdep: %w", err)
	}

	// a parse miss (nothing witnessed) means the dump was empty/unusable: leave the go list edges
	// untouched so reachability still works, and signal the caller with a zero return. (this is
	// checked before adding roots below, so an empty dump never gets mistaken for a one-package
	// build and clobbers the source edges.)
	if len(live) == 0 {
		return 0, nil
	}

	// the entrypoint is never dead code, even if its symbols only ever appear on the `from` side.
	for _, root := range g.rootPackages {
		live[root] = true
	}

	// keep the complete go list edges restricted to live packages. dead packages lose their
	// imports (and, having no live importer, drop out of the reachable sweep); shared deps keep
	// every importer edge, so retained-size attribution sees the real DAG.
	n := 0
	for ip, p := range g.packages {
		if !live[ip] {
			p.Imports = nil
			continue
		}
		kept := p.Imports[:0] // in-place filter: we only ever shrink
		for _, imp := range p.Imports {
			if live[imp] {
				kept = append(kept, imp)
			}
		}
		p.Imports = kept
		n += len(kept)
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
