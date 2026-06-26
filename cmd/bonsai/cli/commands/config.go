package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/locktui"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/configedit"
)

// Config is the `bonsai config` command (from clio): the base command prints the resolved
// application configuration and `config locations` lists the config search paths.
func Config(app clio.Application) *cobra.Command {
	return clio.ConfigCommand(app, nil)
}

// Lock is the `bonsai lock` command: the build-free editor for the config's module-policy lists
// (locked / controlled / unlocked). It is a plain cobra command (its RunE isn't wrapped with the
// progress event-loop UI), so it can drive its own terminal UI without contending for stdin.
func Lock() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "lock [DIR]",
		Short: "interactively edit the module policy lists (locked / controlled / unlocked)",
		Long: "lock opens the target module's dependencies. Mark rows with space (a marks all shown), " +
			"then press l/c/u to lock, control, or unlock the marked set; i adds a free-form pattern row " +
			"(e.g. github.com/anchore/...); / filters; enter saves, esc cancels. Edits are written to the " +
			"analysis lists in the config file, preserving the rest of the file and its comments.",
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
	path := resolveConfigPath(cmd, dir)

	lock, controlled, unlock, err := configedit.ReadBuild(path)
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

	items := buildItems(mods, lock, controlled, unlock)
	final, ok, err := locktui.Run(items, locktui.Lists{Locked: lock, Controlled: controlled, Unlock: unlock})
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "cancelled; config unchanged")
		return nil
	}

	// only write when something changed versus what was read (same guard explore uses); the
	// lists are normalized so the on-disk diff stays minimal.
	base := bonsai.ClassInputs{Locked: sortedUnique(lock), Controlled: sortedUnique(controlled), Unlock: sortedUnique(unlock)}
	next := bonsai.ClassInputs{Locked: sortedUnique(final.Locked), Controlled: sortedUnique(final.Controlled), Unlock: sortedUnique(final.Unlock)}
	if sameInputs(base, next) {
		fmt.Fprintln(os.Stderr, "no changes; config unchanged")
		return nil
	}
	if err := configedit.WriteBuild(path, next.Locked, next.Controlled, next.Unlock); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote module policy lists to %s\n", path)
	return nil
}

// buildItems is the candidate row set: every concrete dependency module, plus a pattern row for
// any existing list entry that isn't a concrete module (globs, "path/..." patterns, or stale
// modules) so the editor shows them with their badges and never silently drops them.
func buildItems(mods []bonsai.ModuleRef, lists ...[]string) []locktui.Item {
	concrete := make(map[string]bool, len(mods))
	items := make([]locktui.Item, 0, len(mods))
	for _, m := range mods {
		concrete[m.Path] = true
		items = append(items, locktui.Item{Module: m.Path, Direct: m.Direct})
	}
	seen := map[string]bool{}
	for _, list := range lists {
		for _, e := range list {
			e = strings.TrimSpace(e)
			if e == "" || concrete[e] || seen[e] {
				continue
			}
			seen[e] = true
			items = append(items, locktui.Item{Module: e, Pattern: true})
		}
	}
	return items
}

// resolveConfigPath determines which file to edit: the first --config/-c value if given,
// else the first existing default config file in dir (the command's target directory, so a
// `[DIR]` argument picks up that directory's .bonsai.yaml, not the process cwd).
func resolveConfigPath(cmd *cobra.Command, dir string) string {
	if files, err := cmd.Flags().GetStringArray("config"); err == nil && len(files) > 0 {
		return files[0]
	}
	return configedit.FindConfig(dir)
}
