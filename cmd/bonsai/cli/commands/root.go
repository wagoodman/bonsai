package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/report"
)

// colorEnabled reports whether the table report should emit ANSI color: only when stdout is
// a terminal and NO_COLOR is unset. Reports are printed to stdout after the UI tears down.
func colorEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

const (
	formatTable    = "table"
	formatJSON     = "json"
	formatMarkdown = "markdown"
)

type analyzeConfig struct {
	options.Format   `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Analysis `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
}

// Root is the bonsai entrypoint command. It builds the target in a Go module (or analyzes
// a prebuilt binary via --binary) and reports what each module contributes to its size and
// which direct dependencies, if pruned, would free the most bytes.
func Root(app clio.Application) *cobra.Command {
	opts := &analyzeConfig{
		Format: options.Format{
			Output:           formatTable,
			AllowableFormats: []string{formatTable, formatJSON, formatMarkdown},
		},
		Analysis: options.DefaultAnalysis(),
	}

	return app.SetupRootCommand(&cobra.Command{
		Use:   "bonsai [DIR]",
		Short: "understand what is in a Go binary and which dependencies, if pruned, would yield the best size savings",
		Long: "bonsai builds a Go module's entrypoint and attributes the resulting binary's size to its module " +
			"dependencies, estimating the cost/benefit of pruning each direct dependency by joining size, " +
			"tree-shake, and coupling signals. Pass --binary to analyze a prebuilt binary instead.",
		Example: options.FormatPositionalArgsHelp(
			map[string]string{
				pathArg: "the module directory to build and analyze (default: current directory)",
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
			return runAnalyze(opts)
		},
	}, opts)
}

func runAnalyze(opts *analyzeConfig) error {
	an, err := bonsai.Analyze(bonsai.Config{
		Dir:         opts.Dir,
		Target:      opts.Target,
		Binary:      opts.Binary,
		Controlled:  opts.Controlled,
		Ignore:      opts.Ignore,
		Unlock:      opts.Unlock,
		Blame:       opts.Blame,
		Why:         opts.Why,
		HideIgnored: opts.HideIgnored,
	})
	if err != nil {
		return err
	}

	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteTable(buf, an, opts.Top, colorEnabled())
	case formatMarkdown:
		err = report.WriteMarkdown(buf, an, opts.Top)
	case formatJSON:
		err = report.WriteJSON(buf, an)
	default:
		err = fmt.Errorf("unknown format: %s", opts.Output)
	}
	if err != nil {
		return err
	}

	bus.Report(buf.String())

	return nil
}
