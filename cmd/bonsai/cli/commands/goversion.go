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

type goVersionConfig struct {
	options.Format `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Build  `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
}

// GoVersion is the `bonsai go-version` command: the lowest `go` directive your own modules can
// declare, given the dependencies actually in the build, and which deps pin that floor — the
// modules to prune to push it lower.
func GoVersion(app clio.Application) *cobra.Command {
	opts := &goVersionConfig{Format: defaultFormat()}

	return app.SetupCommand(&cobra.Command{
		Use:   "go-version [DIR]",
		Short: "report the lowest go directive you can declare, and the deps pinning it",
		Long: "go-version reports the dep-imposed minimum Go version: the lowest `go` directive your owned modules " +
			"could declare given the modules in the build, the headroom you can reclaim right now, and the " +
			"dependencies pinning the floor. Pass --binary to analyze a prebuilt binary instead.",
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
			return runGoVersion(opts)
		},
	}, opts)
}

func runGoVersion(opts *goVersionConfig) error {
	resolved, err := bonsai.Resolve(opts.Config())
	if err != nil {
		return err
	}
	defer resolved.Close()

	floor := resolved.GoFloor()
	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteGoFloorTable(buf, floor, colorEnabled())
	case formatMarkdown:
		err = report.WriteGoFloorMarkdown(buf, floor)
	case formatJSON:
		err = report.WriteJSON(buf, floor)
	default:
		err = fmt.Errorf("unknown format: %s", opts.Output)
	}
	if err != nil {
		return err
	}

	bus.Report(buf.String())
	return nil
}
