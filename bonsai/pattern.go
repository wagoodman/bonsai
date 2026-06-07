package bonsai

import (
	"path"
	"strings"
)

// patternMatcher matches module paths against a set of user patterns. It backs every
// module-set input — the locked/ignore list, the controlled (1st-class) set, and the
// unlock overrides. Patterns support exact paths, a trailing "/..." for a whole subtree,
// and path.Match globs (where "*" does not cross a "/"), e.g. "github.com/anchore/...",
// "golang.org/x/*".
type patternMatcher struct {
	patterns []string
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
