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

type pruneConfig struct {
	options.Format `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Prune  `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
}

// Prune is the `bonsai prune` command: which dependencies, if removed, free the most bytes. It
// ranks prune candidates by dominator-based retained size, annotates each with coupling (the
// removal effort), and lays out a greedy prune plan. Shapley fair-blame is opt-in via --blame.
func Prune(app clio.Application) *cobra.Command {
	opts := &pruneConfig{
		Format: defaultFormat(),
		Prune:  options.DefaultPrune(),
	}

	return app.SetupCommand(&cobra.Command{
		Use:   "prune [DIR]",
		Short: "rank which dependencies to cut for the best size savings, and in what order",
		Long: "prune joins size, tree-shake, and coupling signals to estimate the cost/benefit of pruning each " +
			"direct dependency: how many bytes removing it frees now, how much is shared with other deps, and a " +
			"greedy plan ordering the cuts by marginal savings. Pass --binary to analyze a prebuilt binary instead.",
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
			return runPrune(opts)
		},
	}, opts)
}

func runPrune(opts *pruneConfig) error {
	cfg := opts.Config()
	cfg.Blame = opts.Blame
	cfg.Why = opts.Why
	cfg.HideLocked = opts.HideLocked
	resolved, err := bonsai.Resolve(cfg)
	if err != nil {
		return err
	}
	defer resolved.Close()

	rep := resolved.Prune()
	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WritePruneTable(buf, &rep, opts.Top, colorEnabled())
	case formatMarkdown:
		err = report.WritePruneMarkdown(buf, &rep, opts.Top)
	case formatJSON:
		err = report.WriteJSON(buf, &rep)
	default:
		err = fmt.Errorf("unknown format: %s", opts.Output)
	}
	if err != nil {
		return err
	}

	bus.Report(buf.String())
	return nil
}
