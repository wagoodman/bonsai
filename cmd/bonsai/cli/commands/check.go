package commands

import (
	"fmt"
	"strings"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/humanize"
	"github.com/wagoodman/bonsai/internal/report"
)

const (
	actFail = "fail"
	actWarn = "warn"
)

type checkConfig struct {
	options.Format `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Build  `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
	options.Check  `yaml:"check" json:"check" mapstructure:"check"`
}

// Check is the `bonsai check` command: a non-interactive CI gate that builds (or loads --binary)
// the target, evaluates the committed budget in the config's `check:` block, and exits non-zero
// when the budget is violated. It reuses the same build/resolve and exported results the report
// commands use; the evaluation itself is field comparisons (see evaluateBudget).
func Check(app clio.Application) *cobra.Command {
	opts := &checkConfig{Format: defaultFormat()}

	return app.SetupCommand(&cobra.Command{
		Use:   "check [DIR]",
		Short: "enforce a committed size / go-version / deny-list budget, for CI",
		Long: "check evaluates the budget in the config's `check:` block against the built (or --binary) target: " +
			"max binary size, max go-version floor, a deny list of modules that must never reappear, and optional " +
			"per-module size caps. It exits 0 on pass, 2 on a failed gate, and 1 on an operational error, so CI can " +
			"tell \"gate failed\" from \"tool broke\". An absent `check:` block exits 0 with a note.",
		Example: options.FormatPositionalArgsHelp(
			map[string]string{
				pathArg: pathArgHelp,
			},
		),
		Args: chainArgs(
			cobra.MaximumNArgs(1),
			func(_ *cobra.Command, args []string) error {
				if len(args) == 1 {
					opts.Dir = args[0]
				}
				return nil
			},
		),
		RunE: func(_ *cobra.Command, _ []string) error {
			defer bus.Exit()
			return runCheck(opts)
		},
	}, opts)
}

func runCheck(opts *checkConfig) error {
	b, err := toBudget(opts.Check)
	if err != nil {
		return err // operational error (bad config) -> exit 1
	}

	resolved, err := bonsai.Resolve(bonsai.Config{
		Dir:        opts.Dir,
		Target:     opts.Target,
		Binary:     opts.Binary,
		Controlled: opts.Controlled,
		Locked:     opts.Lock,
		Unlock:     opts.Unlock,
	})
	if err != nil {
		return err
	}
	defer resolved.Close()

	rep := evaluateBudget(resolved.Size(), resolved.GoFloor(), b, opts.Binary != "")

	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteCheckTable(buf, &rep, colorEnabled())
	case formatMarkdown:
		err = report.WriteCheckMarkdown(buf, &rep)
	case formatJSON:
		err = report.WriteJSON(buf, &rep)
	default:
		err = fmt.Errorf("unknown format: %s", opts.Output)
	}
	if err != nil {
		return err
	}

	bus.Report(buf.String())

	if !rep.Pass {
		return &BudgetFailedError{N: countFails(rep.Violations)}
	}
	return nil
}

// moduleCap is a parsed per-module size cap: the module pattern and its byte limit (with the
// original human string kept for messages).
type moduleCap struct {
	pattern  string
	limit    uint64
	limitStr string
}

// budget is the parsed, normalized form of options.Check: sizes are turned into bytes once here so
// the evaluator deals only in numbers and never re-parses. An empty field means that rule is off.
type budget struct {
	maxBinarySize    uint64
	maxBinarySizeStr string // "" = rule off
	maxGoVersion     string // "" = rule off
	deny             []string
	moduleCaps       []moduleCap
	action           string // what a violation does: fail | warn
}

func (b budget) configured() bool {
	return b.maxBinarySizeStr != "" || b.maxGoVersion != "" || len(b.deny) > 0 || len(b.moduleCaps) > 0
}

// toBudget parses and validates the config budget, surfacing a clear error on a bad size string
// or unknown action (which the caller turns into exit 1). Action defaults to "fail".
func toBudget(c options.Check) (budget, error) {
	b := budget{
		maxBinarySizeStr: strings.TrimSpace(c.MaxBinarySize),
		maxGoVersion:     strings.TrimSpace(c.MaxGoVersion),
		deny:             c.Deny,
		action:           strings.ToLower(strings.TrimSpace(c.Action)),
	}

	switch b.action {
	case "":
		b.action = actFail
	case actFail, actWarn:
	default:
		return budget{}, fmt.Errorf("invalid check.action %q (want %q or %q)", c.Action, actFail, actWarn)
	}

	if b.maxBinarySizeStr != "" {
		n, err := humanize.ParseBytes(b.maxBinarySizeStr)
		if err != nil {
			return budget{}, fmt.Errorf("check.max-binary-size: %w", err)
		}
		b.maxBinarySize = n
	}

	for pat, lim := range c.MaxModuleSize {
		n, err := humanize.ParseBytes(lim)
		if err != nil {
			return budget{}, fmt.Errorf("check.max-module-size[%s]: %w", pat, err)
		}
		b.moduleCaps = append(b.moduleCaps, moduleCap{pattern: pat, limit: n, limitStr: strings.TrimSpace(lim)})
	}
	return b, nil
}

// evaluateBudget is the pure, build-free core: it compares the already-exported size and go-floor
// results against the parsed budget and returns the violations. binaryArtifact selects which size
// metric the max-binary-size rule gates: the accounted (~ stripped) size by default, or the
// literal on-disk size when --binary points at the exact artifact being shipped.
func evaluateBudget(size bonsai.SizeReport, floor bonsai.GoFloor, b budget, binaryArtifact bool) report.CheckReport {
	gated := size.AccountedSize
	sizeLabel := "stripped binary" // accounted (~ release) size, independent of build flags
	if binaryArtifact {
		gated = size.BinarySize
		sizeLabel = "on-disk binary" // --binary points at the exact artifact, so gate its literal size
	}

	rep := report.CheckReport{
		BinarySize:      gated,
		BinarySizeLabel: sizeLabel,
		GoFloor:         floor.Version,
		Configured:      b.configured(),
	}
	add := func(v report.Violation) {
		v.Action = b.action
		rep.Violations = append(rep.Violations, v)
	}

	if b.maxBinarySizeStr != "" && gated > b.maxBinarySize {
		add(report.Violation{
			Rule:    "max-binary-size",
			Limit:   b.maxBinarySizeStr,
			Actual:  humanize.Bytes(gated),
			Message: fmt.Sprintf("%s is %s, over the %s budget", sizeLabel, humanize.Bytes(gated), b.maxBinarySizeStr),
		})
	}

	// an empty floor (no dependency forces a directive) never violates.
	if b.maxGoVersion != "" && floor.Version != "" && bonsai.CompareGoVersions(floor.Version, b.maxGoVersion) > 0 {
		add(report.Violation{
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
			add(report.Violation{
				Rule:    "deny",
				Module:  m.Module,
				Limit:   firstMatch(b.deny, m.Module),
				Actual:  "present",
				Message: fmt.Sprintf("denied module %s is in the build (matched %q)", m.Module, firstMatch(b.deny, m.Module)),
			})
		}
		for _, mc := range b.moduleCaps {
			if bonsai.Matches([]string{mc.pattern}, m.Module) && m.Size > mc.limit {
				add(report.Violation{
					Rule:    "max-module-size",
					Module:  m.Module,
					Limit:   mc.limitStr,
					Actual:  humanize.Bytes(m.Size),
					Message: fmt.Sprintf("%s is %s, over its %s cap", m.Module, humanize.Bytes(m.Size), mc.limitStr),
				})
			}
		}
	}

	rep.Pass = countFails(rep.Violations) == 0
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

func countFails(vs []report.Violation) int {
	n := 0
	for _, v := range vs {
		if v.Action == actFail {
			n++
		}
	}
	return n
}

// BudgetFailedError marks a gate failure (distinct from an operational error) so cli's exit-code
// mapper can return 2 instead of 1.
type BudgetFailedError struct{ N int }

func (e *BudgetFailedError) Error() string {
	return fmt.Sprintf("budget check failed: %d violation(s)", e.N)
}
