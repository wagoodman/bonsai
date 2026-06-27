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

// defaultFormat returns a Format preconfigured with the three shared output formats.
func defaultFormat() options.Format {
	return options.Format{
		Output:           formatTable,
		AllowableFormats: []string{formatTable, formatJSON, formatMarkdown},
	}
}

type anatomyConfig struct {
	options.Format  `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Anatomy `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
}

// Root is the bonsai entrypoint command: running `bonsai` with no subcommand opens the
// interactive prune explorer (the TUI). The static reports (anatomy, prune, go-version) live
// under their own subcommands.
//
// SetupRootCommand registers clio's global flags and records the root, but it only wraps a
// command's RunE in the progress event-loop when RunE is non-nil at that point. We leave RunE
// nil here and attach the TUI action afterward so the full-screen explorer owns stdin without
// the event-loop contending for it (the same reason the other interactive commands stay plain
// cobra). PreRunE still runs clio's config setup; it constructs the UI but never starts it.
func Root(app clio.Application, id clio.Identification) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bonsai [DIR]",
		Short: "interactively explore what happens to your binary size when you prune dependencies",
		Long: "bonsai helps you make smaller dependency trees for your Go projects. Run it with no subcommand to " +
			"open the interactive prune explorer: every dependency candidate starts selected for removal, and you " +
			"deselect what you need to see the projected binary size. Use `bonsai anatomy` for a static size " +
			"breakdown, `bonsai prune` to rank which dependencies to cut, and `bonsai go-version` for the lowest go " +
			"directive you can declare. Pass --binary to analyze a prebuilt binary instead.",
		Args: cobra.MaximumNArgs(1),
	}
	app.SetupRootCommand(cmd)
	wireExplore(cmd, id)
	return cmd
}

// Anatomy is the `bonsai anatomy` command: the binary's anatomy — how big it is and what occupies
// the space, attributed by content (code / data / pclntab) and by owner (module).
func Anatomy(app clio.Application) *cobra.Command {
	opts := &anatomyConfig{
		Format:  defaultFormat(),
		Anatomy: options.DefaultAnatomy(),
	}

	return app.SetupCommand(&cobra.Command{
		Use:   "anatomy [DIR]",
		Short: "break down what occupies your binary's size, by content and by owner",
		Long: "anatomy builds a Go module's entrypoint and attributes the resulting binary's size to its module " +
			"dependencies, broken down by content and by owner. Use `bonsai prune` to see which dependencies, if " +
			"removed, would free the most bytes, and `bonsai go-version` for the lowest go directive you can declare. " +
			"Pass --binary to analyze a prebuilt binary instead.",
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
			return runAnatomy(opts)
		},
	}, opts)
}

func runAnatomy(opts *anatomyConfig) error {
	resolved, err := bonsai.Resolve(bonsai.Config{
		Dir:        opts.Dir,
		Target:     opts.Target,
		Binary:     opts.Binary,
		Controlled: opts.Controlled,
		Locked:     opts.Lock,
		Unlock:     opts.Unlock,
		Why:        opts.Why,
		HideLocked: opts.HideLocked,
	})
	if err != nil {
		return err
	}
	defer resolved.Close()

	rep := resolved.Size()
	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteSizeTable(buf, &rep, opts.Top, opts.Sections, colorEnabled())
	case formatMarkdown:
		err = report.WriteSizeMarkdown(buf, &rep, opts.Top, opts.Sections)
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
