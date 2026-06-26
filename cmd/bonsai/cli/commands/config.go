package commands

import (
	"fmt"
	"os"
	"sort"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/configedit"
	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/locktui"
	"github.com/wagoodman/bonsai/internal"
	"github.com/wagoodman/bonsai/internal/bonsai"
)

// Config is the `bonsai config` command (from clio): the base command prints the resolved
// application configuration and `config locations` lists the config search paths.
func Config(app clio.Application) *cobra.Command {
	return clio.ConfigCommand(app, nil)
}

// Lock is the `bonsai lock` command: an interactive editor for the config's lock list. It is a
// plain cobra command (its RunE isn't wrapped with the progress event-loop UI), so it can drive
// its own terminal UI without contending for stdin.
func Lock() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "lock [DIR]",
		Short: "interactively edit the lock list (modules never suggested for pruning)",
		Long: "lock opens a fuzzy multi-select of the target module's dependencies. Type to filter, " +
			"space to toggle, enter to save, esc to cancel. The selection is written to the lock list in " +
			"the config file, preserving the rest of the file and its comments.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runConfigLock(c, dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "",
		"entrypoint package whose dependencies are offered (default: the module's sole main package)")
	return cmd
}

func runConfigLock(cmd *cobra.Command, dir, target string) error {
	path := resolveConfigPath(cmd)

	current, err := configedit.ReadLock(path)
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

	items := make([]locktui.Item, len(mods))
	for i, m := range mods {
		items[i] = locktui.Item{Module: m.Path, Direct: m.Direct}
	}

	// pre-select modules already covered by the current lock list; carry forward any
	// entries that aren't concrete dependency modules (globs, patterns, stale modules) so
	// the editor never silently drops them.
	candidates := map[string]bool{}
	for _, it := range items {
		candidates[it.Module] = true
	}
	preselected, extras := splitLock(current, candidates)

	chosen, ok, err := locktui.Run(items, preselected)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "cancelled; config unchanged")
		return nil
	}

	final := make([]string, 0, len(extras)+len(chosen))
	final = append(final, extras...)
	final = append(final, chosen...)
	sort.Strings(final)
	if err := configedit.WriteLock(path, final); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d lock entr%s to %s\n", len(final), plural(len(final)), path)
	return nil
}

// splitLock divides the current lock entries into modules to pre-check (those that are
// concrete dependency modules) and extras to preserve verbatim (globs, "path/..." patterns,
// or modules no longer in the graph).
func splitLock(current []string, candidates map[string]bool) (preselected map[string]bool, extras []string) {
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
func resolveConfigPath(cmd *cobra.Command) string {
	if files, err := cmd.Flags().GetStringArray("config"); err == nil && len(files) > 0 {
		return files[0]
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
			return p
		}
	}
	return defaults[0]
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
