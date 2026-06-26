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

type inspectConfig struct {
	options.Format `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Build  `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
}

// Inspect is the `bonsai inspect MODULE` command: the single-module drill-down. It reports the
// concrete import sites (the edit locations), the per-entry-package weight (the rewrite scope),
// what leaves vs survives if the module is pruned, and the go-version floor delta — the
// machine-readable detail an agent needs to act on one prune candidate.
func Inspect(app clio.Application) *cobra.Command {
	opts := &inspectConfig{Format: defaultFormat()}
	var module string

	return app.SetupCommand(&cobra.Command{
		Use:   "inspect MODULE [DIR]",
		Short: "drill into one module: where it's imported, what pruning it frees, and the go-version effect",
		Long: "inspect answers \"if I cut this dependency, what exactly do I edit and what happens?\" for a single " +
			"module: the import sites in your first-party code (file:line), the per-entry-package retained size " +
			"(how much each imported package is worth — the scope of a partial rewrite), what other modules leave " +
			"vs survive, and whether pruning it lowers your go-version floor. Pass --binary to analyze a prebuilt " +
			"binary instead.",
		Example: options.FormatPositionalArgsHelp(
			map[string]string{
				"MODULE": "the dependency module path to inspect (e.g. github.com/google/go-containerregistry)",
				pathArg:  pathArgHelp,
			},
		),
		Args: chainArgs(
			cobra.RangeArgs(1, 2),
			func(_ *cobra.Command, args []string) error {
				module = args[0]
				if len(args) == 2 {
					opts.Dir = args[1]
				}
				return nil
			},
		),
		RunE: func(_ *cobra.Command, _ []string) error {
			defer bus.Exit()
			return runInspect(opts, module)
		},
	}, opts)
}

func runInspect(opts *inspectConfig, module string) error {
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

	rep, err := resolved.Inspect(module)
	if err != nil {
		return err
	}

	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteInspectTable(buf, &rep, colorEnabled())
	case formatMarkdown:
		err = report.WriteInspectMarkdown(buf, &rep)
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
