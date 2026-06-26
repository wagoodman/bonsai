package bonsai

import (
	"path"
	"strings"
)

// patternMatcher matches module paths against a set of user patterns. It backs every
// module-set input — the lock list, the controlled (1st-class) set, and the
// unlock overrides. Patterns support exact paths, a trailing "/..." for a whole subtree
// (slash-boundary aware), a bare trailing "..." Go-style prefix wildcard, and path.Match
// globs (where "*" does not cross a "/"), e.g. "github.com/anchore/...",
// "github.com/anchore...", "golang.org/x/*".
type patternMatcher struct {
	patterns []string
}

// Matches reports whether module is covered by any of the given patterns (exact path,
// "foo/...", bare "foo...", or path.Match glob). It exposes the lock-list matcher to the
// interactive editors so they can show which modules a pattern already covers, no build needed.
func Matches(patterns []string, module string) bool {
	return newPatternMatcher(patterns).match(module)
}

func newPatternMatcher(patterns []string) patternMatcher {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return patternMatcher{patterns: cleaned}
}

func (m patternMatcher) match(module string) bool {
	for _, p := range m.patterns {
		// "foo/..." matches foo and its whole subtree, respecting slash boundaries.
		if sub, ok := strings.CutSuffix(p, "/..."); ok {
			if module == sub || strings.HasPrefix(module, sub+"/") {
				return true
			}
			continue
		}
		// a bare trailing "..." is Go's wildcard: match any module sharing this prefix
		// (so "github.com/anchore..." controls the anchore subtree just like ".../...").
		if sub, ok := strings.CutSuffix(p, "..."); ok {
			if strings.HasPrefix(module, sub) {
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
