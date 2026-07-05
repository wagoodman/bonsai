package commands

import (
	"fmt"
	"strings"

	"github.com/anchore/clio"
	"github.com/anchore/fangs"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/report"
)

const refArg = "REF"

type diffConfig struct {
	options.Format `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Build  `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
	Top            int    `yaml:"top" json:"top" mapstructure:"top"`
	ref            string // resolved from the positional REF argument
}

func (o *diffConfig) AddFlags(flags fangs.FlagSet) {
	flags.IntVarP(&o.Top, "top", "t", "number of rows to show in the added/removed/changed tables")
}

func (o *diffConfig) PostLoad() error {
	if o.Top <= 0 {
		o.Top = 40
	}
	return nil
}

// Diff is the `bonsai diff [DIR] <REF>` command: build and analyze both the working tree and a
// baseline source state (the merge-base with REF by default), then report the size and go-floor
// delta — what this branch did to your binary. JSON is the machine contract a CI bot renders.
func Diff(app clio.Application) *cobra.Command {
	opts := &diffConfig{Format: defaultFormat(), Top: 40}

	return app.SetupCommand(&cobra.Command{
		Use:   "diff [DIR] REF",
		Short: "report the size and go-floor delta this branch makes against a git ref or prebuilt binary",
		Long: "diff builds and analyzes both your working tree and a baseline — by default the " +
			"merge-base of HEAD and REF, i.e. where your branch diverged — and reports the net binary size change, " +
			"which modules were added or removed (direct vs transitive), and any movement in the go-version floor. " +
			"The baseline is checked out into a throwaway git worktree; your working tree, index, and branch are " +
			"untouched. Pass --binary instead of REF to compare your working-tree build against a prebuilt binary. " +
			"Use --output json for the full machine-readable delta a CI bot can render into a PR comment.",
		Example: options.FormatPositionalArgsHelp(
			map[string]string{
				pathArg: pathArgHelp,
				refArg:  "the git ref to compare against (branch, tag, or commit); the baseline is the merge-base with it. Optional when --binary names the baseline.",
			},
		),
		Args: func(cmd *cobra.Command, args []string) error {
			// --binary supplies the baseline, so REF is optional; otherwise REF is required.
			_min, _max := 1, 2
			if opts.Binary != "" {
				_min, _max = 0, 1
			}
			if err := cobra.RangeArgs(_min, _max)(cmd, args); err != nil {
				return err
			}
			opts.Dir, opts.ref = parseDiffArgs(args, opts.Binary != "")
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			defer bus.Exit()
			return runDiff(opts)
		},
	}, opts)
}

// parseDiffArgs splits the validated positional args. With --binary the baseline is the binary, so
// every arg is DIR (0 or 1). Otherwise REF is always the last, an optional earlier one is DIR.
func parseDiffArgs(args []string, binaryBaseline bool) (dir, ref string) {
	if binaryBaseline {
		if len(args) == 1 {
			dir = args[0]
		}
		return dir, ""
	}
	ref = args[len(args)-1]
	if len(args) == 2 {
		dir = args[0]
	}
	return dir, ref
}

func runDiff(opts *diffConfig) error {
	// Config() carries the persisted analysis.build settings (and any goreleaser-derived ones), so
	// both sides of the diff build the same way the other subjects do.
	rep, err := bonsai.Diff(opts.Config(), opts.ref)
	if err != nil {
		return err
	}

	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteDiffTable(buf, rep, opts.Top, colorEnabled())
	case formatMarkdown:
		err = report.WriteDiffMarkdown(buf, rep, opts.Top)
	case formatJSON:
		err = report.WriteJSON(buf, rep)
	default:
		err = fmt.Errorf("unknown format: %s", opts.Output)
	}
	if err != nil {
		return err
	}

	bus.Report(buf.String())
	return nil
}
