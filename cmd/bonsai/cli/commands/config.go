package commands

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/bonsai"
	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/configedit"
	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/ignoretui"
	"github.com/wagoodman/bonsai/internal"
)

// Config is the `bonsai config` command group. Its subcommands edit the bonsai config file.
// These are plain cobra commands (not wired through clio's command setup) so they can drive
// their own interactive terminal UI without the progress event-loop UI contending for stdin.
func Config() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "view and edit bonsai configuration",
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(configIgnore())
	return cmd
}

func configIgnore() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "ignore [DIR]",
		Short: "interactively edit the ignore list (modules never suggested for pruning)",
		Long: "ignore opens a fuzzy multi-select of the target module's dependencies. Type to filter, " +
			"space to toggle, enter to save, esc to cancel. The selection is written to the ignore list in " +
			"the config file, preserving the rest of the file and its comments.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runConfigIgnore(c, dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "",
		"entrypoint package whose dependencies are offered (default: the module's sole main package)")
	return cmd
}

func runConfigIgnore(cmd *cobra.Command, dir, target string) error {
	path, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	current, err := configedit.ReadIgnore(path)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "resolving dependency modules…")
	mods, err := bonsai.Modules(bonsai.Config{Dir: dir, Target: target})
	if err != nil {
		return err
	}
	if len(mods) == 0 {
		return fmt.Errorf("no dependency modules found in %s", dir)
	}

	items := make([]ignoretui.Item, len(mods))
	for i, m := range mods {
		items[i] = ignoretui.Item{Module: m.Path, Direct: m.Direct}
	}

	// pre-select modules already covered by the current ignore list; carry forward any
	// entries that aren't concrete dependency modules (globs, patterns, stale modules) so
	// the editor never silently drops them.
	candidates := map[string]bool{}
	for _, it := range items {
		candidates[it.Module] = true
	}
	preselected, extras := splitIgnore(current, candidates)

	chosen, ok, err := ignoretui.Run(items, preselected)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "cancelled; config unchanged")
		return nil
	}

	final := append(extras, chosen...)
	sort.Strings(final)
	if err := configedit.WriteIgnore(path, final); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d ignore entr%s to %s\n", len(final), plural(len(final)), path)
	return nil
}

// splitIgnore divides the current ignore entries into modules to pre-check (those that are
// concrete dependency modules) and extras to preserve verbatim (globs, "path/..." patterns,
// or modules no longer in the graph).
func splitIgnore(current []string, candidates map[string]bool) (preselected map[string]bool, extras []string) {
	preselected = map[string]bool{}
	for _, e := range current {
		if candidates[e] {
			preselected[e] = true
		} else {
			extras = append(extras, e)
		}
	}
	return preselected, extras
}

// resolveConfigPath determines which file to edit: the first --config/-c value if given,
// else the first existing default config file, else ".<app>.yaml" in the current directory.
func resolveConfigPath(cmd *cobra.Command) (string, error) {
	if files, err := cmd.Flags().GetStringArray("config"); err == nil && len(files) > 0 {
		return files[0], nil
	}
	defaults := []string{
		"." + internal.ApplicationName + ".yaml",
		"." + internal.ApplicationName + ".yml",
		internal.ApplicationName + ".yaml",
		internal.ApplicationName + ".yml",
		"." + internal.ApplicationName + "/config.yaml",
	}
	for _, p := range defaults {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return defaults[0], nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
