package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/bonsai"
	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/prunetui"
)

// Explore is the `bonsai explore` command: an interactive prune explorer. It is a plain cobra
// command (not wired through clio) so its full-screen TUI owns stdin without the progress
// event-loop UI contending for it. The build/analysis runs first with simple stderr status.
func Explore() *cobra.Command {
	var (
		target     string
		binary     string
		controlled []string
		locked     []string
		unlock     []string
	)
	cmd := &cobra.Command{
		Use:   "explore [DIR]",
		Short: "interactively explore which dependencies to prune (what-if TUI)",
		Long: "explore opens a what-if TUI: every dependency candidate starts selected for removal — " +
			"deselect the ones you need with space. The summary bar shows the projected binary size and how " +
			"many modules actually get pruned; the right panes show what the highlighted module drags out " +
			"(and what survives, held by others) and why it's in the build. Your selection and per-module " +
			"class/lock choices are remembered per scanned module across runs. Read-only — enter prints the " +
			"chosen prune set.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runExplore(bonsai.Config{
				Dir:        dir,
				Target:     target,
				Binary:     binary,
				Controlled: controlled,
				Locked:     locked,
				Unlock:     unlock,
			})
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&target, "target", "", "entrypoint package to build and analyze")
	flags.StringVarP(&binary, "binary", "b", "", "explore a prebuilt binary instead of building from source")
	flags.StringArrayVarP(&controlled, "controlled", "C", nil, "1st-class module patterns whose imports are cuttable")
	flags.StringArrayVarP(&locked, "ignore", "i", nil, "module patterns never offered for pruning (locked)")
	flags.StringArrayVar(&unlock, "unlock", nil, "locked modules to re-open as prune candidates")
	return cmd
}

func runExplore(cfg bonsai.Config) error {
	fmt.Fprintln(os.Stderr, "building and analyzing… (this can take a few seconds)")
	session, err := bonsai.NewSession(cfg)
	if err != nil {
		return err
	}

	// remembered selection/classification for this exact scan target (not shared across targets).
	key := session.MainModule()
	initial := loadExploreState(key)
	if isEmptyInputs(initial.Inputs) {
		initial.Inputs = session.Inputs() // first run: seed from flags
	}

	res, err := prunetui.Run(session, initial)
	if err != nil {
		return err
	}
	saveExploreState(key, res.State) // persist whatever the user ended on

	if !res.Confirmed {
		fmt.Fprintln(os.Stderr, "cancelled; nothing applied")
		return nil
	}
	if len(res.Pruned) == 0 {
		fmt.Fprintln(os.Stderr, "no modules selected for pruning")
		return nil
	}
	// the chosen prune set goes to stdout so it can be piped/saved; status stays on stderr.
	fmt.Fprintf(os.Stderr, "\n%d modules would be pruned:\n", len(res.Pruned))
	for _, m := range res.Pruned {
		fmt.Println(m)
	}
	return nil
}

func isEmptyInputs(in bonsai.ClassInputs) bool {
	return len(in.Controlled) == 0 && len(in.Locked) == 0 && len(in.Unlock) == 0
}

// explore state is a tiny JSON store keyed by scanned module path, so re-running on the same
// target restores the prior selection and class/lock choices but never leaks them to a
// different target.

func exploreStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bonsai", "explore.json")
}

func loadExploreState(key string) prunetui.State {
	path := exploreStatePath()
	if path == "" {
		return prunetui.State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return prunetui.State{}
	}
	var store map[string]prunetui.State
	if json.Unmarshal(data, &store) != nil {
		return prunetui.State{}
	}
	return store[key]
}

func saveExploreState(key string, state prunetui.State) {
	path := exploreStatePath()
	if path == "" {
		return
	}
	store := map[string]prunetui.State{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &store)
	}
	store[key] = state
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if data, err := json.MarshalIndent(store, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}
