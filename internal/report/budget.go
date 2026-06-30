package report

import (
	"fmt"
	"strings"

	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/humanize"
)

// what a violation does.
const (
	ActionFail = "fail"
	ActionWarn = "warn"
)

// BudgetSpec is the raw check budget as written in config: human size strings, a deny list, and an
// action. It mirrors the fields of the CLI's check options so callers map straight across, while
// keeping this package free of any config/options dependency.
type BudgetSpec struct {
	MaxBinarySize string
	MaxGoVersion  string
	Deny          []string
	MaxModuleSize map[string]string
	Action        string
}

// Budget is the parsed, normalized form of a BudgetSpec: sizes are turned into bytes once here so
// the evaluator deals only in numbers and never re-parses. An empty field means that rule is off.
type Budget struct {
	maxBinarySize    uint64
	maxBinarySizeStr string // "" = rule off
	maxGoVersion     string // "" = rule off
	deny             []string
	moduleCaps       []moduleCap
	action           string // what a violation does: fail | warn
}

// moduleCap is a parsed per-module size cap: the module pattern and its byte limit (with the
// original human string kept for messages).
type moduleCap struct {
	pattern  string
	limit    uint64
	limitStr string
}

func (b Budget) configured() bool {
	return b.maxBinarySizeStr != "" || b.maxGoVersion != "" || len(b.deny) > 0 || len(b.moduleCaps) > 0
}

// ParseBudget parses and validates a budget spec, surfacing a clear error on a bad size string or
// unknown action (which the caller turns into an operational error). Action defaults to "fail".
func ParseBudget(spec BudgetSpec) (Budget, error) {
	b := Budget{
		maxBinarySizeStr: strings.TrimSpace(spec.MaxBinarySize),
		maxGoVersion:     strings.TrimSpace(spec.MaxGoVersion),
		deny:             spec.Deny,
		action:           strings.ToLower(strings.TrimSpace(spec.Action)),
	}

	switch b.action {
	case "":
		b.action = ActionFail
	case ActionFail, ActionWarn:
	default:
		return Budget{}, fmt.Errorf("invalid check.action %q (want %q or %q)", spec.Action, ActionFail, ActionWarn)
	}

	if b.maxBinarySizeStr != "" {
		n, err := humanize.ParseBytes(b.maxBinarySizeStr)
		if err != nil {
			return Budget{}, fmt.Errorf("check.max-binary-size: %w", err)
		}
		b.maxBinarySize = n
	}

	for pat, lim := range spec.MaxModuleSize {
		n, err := humanize.ParseBytes(lim)
		if err != nil {
			return Budget{}, fmt.Errorf("check.max-module-size[%s]: %w", pat, err)
		}
		b.moduleCaps = append(b.moduleCaps, moduleCap{pattern: pat, limit: n, limitStr: strings.TrimSpace(lim)})
	}
	return b, nil
}

// EvaluateBudget is the pure, build-free core: it compares the already-exported size and go-floor
// results against the parsed budget and returns the violations. binaryArtifact selects which size
// metric the max-binary-size rule gates: the accounted (~ stripped) size by default, or the literal
// on-disk size when --binary points at the exact artifact being shipped.
func EvaluateBudget(size bonsai.SizeReport, floor bonsai.GoFloor, b Budget, binaryArtifact bool) CheckReport {
	gated := size.AccountedSize
	sizeLabel := "stripped binary" // accounted (~ release) size, independent of build flags
	if binaryArtifact {
		gated = size.BinarySize
		sizeLabel = "on-disk binary" // --binary points at the exact artifact, so gate its literal size
	}

	rep := CheckReport{
		BinarySize:      gated,
		BinarySizeLabel: sizeLabel,
		GoFloor:         floor.Version,
		Configured:      b.configured(),
	}
	add := func(v Violation) {
		v.Action = b.action
		rep.Violations = append(rep.Violations, v)
	}

	if b.maxBinarySizeStr != "" && gated > b.maxBinarySize {
		add(Violation{
			Rule:    "max-binary-size",
			Limit:   b.maxBinarySizeStr,
			Actual:  humanize.Bytes(gated),
			Message: fmt.Sprintf("%s is %s, over the %s budget", sizeLabel, humanize.Bytes(gated), b.maxBinarySizeStr),
		})
	}

	// an empty floor (no dependency forces a directive) never violates.
	if b.maxGoVersion != "" && floor.Version != "" && bonsai.CompareGoVersions(floor.Version, b.maxGoVersion) > 0 {
		add(Violation{
			Rule:    "max-go-version",
			Limit:   b.maxGoVersion,
			Actual:  floor.Version,
			Message: fmt.Sprintf("go floor is %s, over the %s budget", floor.Version, b.maxGoVersion),
		})
	}

	for _, m := range size.Modules {
		if !m.InBuild {
			continue
		}
		if bonsai.Matches(b.deny, m.Module) {
			add(Violation{
				Rule:    "deny",
				Module:  m.Module,
				Limit:   firstMatch(b.deny, m.Module),
				Actual:  "present",
				Message: fmt.Sprintf("denied module %s is in the build (matched %q)", m.Module, firstMatch(b.deny, m.Module)),
			})
		}
		for _, mc := range b.moduleCaps {
			if bonsai.Matches([]string{mc.pattern}, m.Module) && m.Size > mc.limit {
				add(Violation{
					Rule:    "max-module-size",
					Module:  m.Module,
					Limit:   mc.limitStr,
					Actual:  humanize.Bytes(m.Size),
					Message: fmt.Sprintf("%s is %s, over its %s cap", m.Module, humanize.Bytes(m.Size), mc.limitStr),
				})
			}
		}
	}

	rep.Pass = CountFails(rep.Violations) == 0
	return rep
}

// firstMatch returns the first pattern in patterns that matches module, for naming the offending
// pattern in a deny violation. Returns "" if none match (shouldn't happen at the call site).
func firstMatch(patterns []string, module string) string {
	for _, p := range patterns {
		if bonsai.Matches([]string{p}, module) {
			return p
		}
	}
	return ""
}

// CountFails counts the violations whose action is "fail" (a "warn" violation prints but doesn't
// fail the gate).
func CountFails(vs []Violation) int {
	n := 0
	for _, v := range vs {
		if v.Action == ActionFail {
			n++
		}
	}
	return n
}
