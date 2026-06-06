package bonsai

import (
	"path"
	"strings"
)

// ignoreMatcher decides whether a module path is on the user's ignore list — modules we
// never suggest dropping (core dependencies the project will always carry). Patterns
// support exact paths, a trailing "/..." for a whole subtree, and path.Match globs (where
// "*" does not cross a "/"), e.g. "github.com/anchore/...", "golang.org/x/*".
type ignoreMatcher struct {
	patterns []string
}

func newIgnoreMatcher(patterns []string) ignoreMatcher {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return ignoreMatcher{patterns: cleaned}
}

func (m ignoreMatcher) empty() bool { return len(m.patterns) == 0 }

func (m ignoreMatcher) match(module string) bool {
	for _, p := range m.patterns {
		if sub, ok := strings.CutSuffix(p, "/..."); ok {
			if module == sub || strings.HasPrefix(module, sub+"/") {
				return true
			}
			continue
		}
		if module == p {
			return true
		}
		if ok, _ := path.Match(p, module); ok {
			return true
		}
	}
	return false
}
