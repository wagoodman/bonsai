package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/prunetui"
	"github.com/wagoodman/bonsai/cmd/bonsai/internal/ui"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/configedit"
)

// wireExplore attaches the interactive prune explorer to cmd: a what-if TUI where every
// dependency candidate starts selected for removal and you deselect what you need. It is the
// root command's action (running `bonsai` with no subcommand). The RunE is left unwrapped by
// clio's event-loop so the full-screen TUI owns stdin; the build/analysis runs first under the
// same task-progress UI the static reports show (via ui.RunWithProgress), which tears down
// before the TUI starts. See Root for why this isn't wired as a normal clio RunE.
func wireExplore(cmd *cobra.Command, id clio.Identification) {
	// the TUI shows "bonsai · <version>" in its status bar; hide the unset build-time default
	// ("[not provided]" from main.go) that ldflags overrides on real releases.
	version := id.Version
	if version == "[not provided]" {
		version = ""
	}
	var (
		target     string
		binary     string
		controlled []string
		locked     []string
		unlock     []string
	)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		return runExplore(c, bonsai.Config{
			Dir:        dir,
			Target:     target,
			Binary:     binary,
			Controlled: controlled,
			Locked:     locked,
			Unlock:     unlock,
		}, version)
	}
	flags := cmd.Flags()
	flags.StringVar(&target, "target", "", "entrypoint package to build and analyze")
	flags.StringVarP(&binary, "binary", "b", "", "explore a prebuilt binary instead of building from source")
	flags.StringArrayVarP(&controlled, "controlled", "C", nil, "1st-class module patterns whose imports are cuttable")
	flags.StringArrayVarP(&locked, "lock", "l", nil, "module patterns to lock (never offered for pruning)")
	flags.StringArrayVar(&unlock, "unlock", nil, "locked modules to re-open as prune candidates")
}

func runExplore(cmd *cobra.Command, cfg bonsai.Config, version string) error {
	// explore bypasses clio, so .bonsai.yaml isn't auto-loaded; fold in the analysis lock/
	// controlled/unlock lists (the single source of truth, written by `bonsai config lock` and
	// explore itself) unioned with any flags. baseline is the merged starting state we diff
	// against on exit to decide whether to write the user's lock/class edits back.
	path := resolveConfigPath(cmd)
	if lock, controlled, unlock, err := configedit.ReadBuild(path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", path, err)
	} else {
		cfg.Locked = append(cfg.Locked, lock...)
		cfg.Controlled = append(cfg.Controlled, controlled...)
		cfg.Unlock = append(cfg.Unlock, unlock...)
	}
	// normalize the merged flag+config lists so the session, the TUI's toggle lists, and the
	// writeback diff all share one clean, stable set.
	cfg.Controlled = sortedUnique(cfg.Controlled)
	cfg.Locked = sortedUnique(cfg.Locked)
	cfg.Unlock = sortedUnique(cfg.Unlock)
	baseline := bonsai.ClassInputs{
		Controlled: cfg.Controlled,
		Locked:     cfg.Locked,
		Unlock:     cfg.Unlock,
	}

	var session *bonsai.Session
	// run the build/analysis under the same ✔ task-progress UI the root command shows; it tears
	// down before the full-screen TUI below takes stdin.
	err := ui.RunWithProgress(false, func() error {
		var e error
		session, e = bonsai.NewSession(cfg)
		return e
	})
	if err != nil {
		return err
	}

	// remembered what-if selection for this exact scan target (not shared across targets); lock/
	// class state comes from config, not from this cache.
	key := session.MainModule()
	initial := loadExploreState(key)

	res, err := prunetui.Run(session, initial, version)
	if err != nil {
		return err
	}
	saveExploreState(key, res.State) // persist the selection the user ended on

	if !res.Confirmed {
		fmt.Fprintln(os.Stderr, "cancelled; nothing applied")
		return nil
	}

	// persist any lock/class edits made in the TUI back to the config (the source of truth),
	// but only when they actually changed and we have a file to write — esc/cancel never reaches
	// here, so this is a deliberate confirm.
	persistInputs(path, baseline, res.Inputs)

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

// persistInputs writes the explorer's final lock/class state back to the config file when it
// differs from the merged starting state. The lists are normalized so the on-disk diff stays
// minimal and matches what `bonsai config lock` writes.
func persistInputs(path string, baseline, final bonsai.ClassInputs) {
	final = bonsai.ClassInputs{
		Controlled: sortedUnique(final.Controlled),
		Locked:     sortedUnique(final.Locked),
		Unlock:     sortedUnique(final.Unlock),
	}
	if path == "" || sameInputs(baseline, final) {
		return
	}
	if err := configedit.WriteBuild(path, final.Locked, final.Controlled, final.Unlock); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save lock changes to %s: %v\n", path, err)
		return
	}
	fmt.Fprintf(os.Stderr, "saved lock/class changes to %s\n", path)
}

// sameInputs reports whether two normalized (sorted, de-duplicated) ClassInputs are equal.
func sameInputs(a, b bonsai.ClassInputs) bool {
	return equalSlice(a.Controlled, b.Controlled) &&
		equalSlice(a.Locked, b.Locked) &&
		equalSlice(a.Unlock, b.Unlock)
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sortedUnique returns the input sorted with duplicates and empties removed (nil for empty), so
// merged flag+config lists produce a clean, stable set for classification and config writeback.
func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
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
		_ = os.WriteFile(path, data, 0o644) //nolint:gosec // explorer UI state cache; intentionally world-readable, holds no secrets
	}
}
