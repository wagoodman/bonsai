package commands

import (
	"fmt"
	"strings"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/report"
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
	b, err := report.ParseBudget(toBudgetSpec(opts.Check))
	if err != nil {
		return err // operational error (bad config) -> exit 1
	}

	// Config() carries the persisted analysis.build settings (and any goreleaser-derived ones,
	// resolved at config-load time); building the literal by hand here used to drop them.
	resolved, err := bonsai.Resolve(opts.Config())
	if err != nil {
		return err
	}
	defer resolved.Close()

	rep := report.EvaluateBudget(resolved.Size(), resolved.GoFloor(), b, opts.Binary != "")

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
		return &BudgetFailedError{N: report.CountFails(rep.Violations)}
	}
	return nil
}

// toBudgetSpec maps the CLI check options onto the report package's config-neutral budget spec.
func toBudgetSpec(c options.Check) report.BudgetSpec {
	return report.BudgetSpec{
		MaxBinarySize: c.MaxBinarySize,
		MaxGoVersion:  c.MaxGoVersion,
		Deny:          c.Deny,
		MaxModuleSize: c.MaxModuleSize,
		Action:        c.Action,
	}
}

// BudgetFailedError marks a gate failure (distinct from an operational error) so cli's exit-code
// mapper can return 2 instead of 1.
type BudgetFailedError struct{ N int }

func (e *BudgetFailedError) Error() string {
	return fmt.Sprintf("budget check failed: %d violation(s)", e.N)
}
