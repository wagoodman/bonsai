package prunetui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/bonsai"
)

func TestHumize(t *testing.T) {
	tests := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1500, "1.5 kB"},
		{16_200_000, "16.2 MB"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, humize(tt.in))
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "short", truncate("short", 20))
	assert.Equal(t, "…/long/path", truncate("github.com/very/long/path", 11))
}

func TestPruneAloneLine(t *testing.T) {
	// shared deps held back: say both — frees the net, of the gross, with the held remainder.
	shared := pruneAloneLine(bonsai.Detail{Target: true, Exclusive: 1500, PullsIn: 1800})
	assert.Contains(t, shared, "frees")
	assert.Contains(t, shared, "pulled in")
	assert.Contains(t, shared, "held by others")

	// fully exclusive: no shared remainder to explain.
	excl := pruneAloneLine(bonsai.Detail{Target: true, Exclusive: 1800, PullsIn: 1800})
	assert.Contains(t, excl, "nothing shared")

	// non-candidate: no prune-alone figure to report.
	assert.Contains(t, pruneAloneLine(bonsai.Detail{Target: false}), "not a candidate")
}

func TestWhyTreeInversion(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI) // force styling so the target's purple marker is visible

	// reverse importer tree from the Session: target X, imported by a, imported by you (1st).
	root := &bonsai.ImportNode{Module: "X", Class: "3rd", Via: []*bonsai.ImportNode{
		{Module: "a", Class: "2nd", Via: []*bonsai.ImportNode{
			{Module: "you", Class: "1st"},
		}},
	}}

	var paths [][]wnode
	collectWhyPaths(root, nil, &paths)
	trie := &whyTrie{}
	for _, p := range paths {
		trie.insert(p)
	}
	lines := renderWhyTrie(trie, "", 80, "X")

	require.Len(t, lines, 3)
	// go-mod-why order: consumer at the top, target (the selected module) at the bottom.
	assert.Contains(t, lines[0], "you")
	assert.Contains(t, lines[1], "a")
	assert.Contains(t, lines[2], "X")
	// the target is highlighted purple, tying it back to the left-pane selection; the importers
	// above it are not.
	assert.Contains(t, lines[2], styPurple.Render("X"), "the target module is purple")
	assert.NotContains(t, lines[0], styPurple.Render("you"), "importers are not purple")
	// the consumer line uses a tree connector.
	assert.Contains(t, lines[0], "─")
}

func TestStyleByClass(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI) // force styling even without a TTY in tests

	// 1st-class renders gold even when locked; locked-but-not-yours renders grey; plain otherwise.
	gold := classStyle("1st", true, true, "m")
	grey := classStyle("3rd", false, true, "m")
	plain := classStyle("2nd", false, false, "m")
	assert.NotEqual(t, plain, gold, "1st-class is styled")
	assert.NotEqual(t, plain, grey, "locked is styled")
	assert.NotEqual(t, gold, grey, "gold and grey differ")
}

func TestSortByGoMin(t *testing.T) {
	m := model{
		all: []bonsai.Module{
			{Module: "low", Target: true, GoVersion: "1.13", Size: 100},
			{Module: "high", Target: true, GoVersion: "1.24.0", Size: 50},
			{Module: "oldest", Target: true, GoVersion: "1.9", Size: 200},
			{Module: "none", Target: true, GoVersion: "", Size: 999}, // no directive sinks to the bottom
		},
		sortMode: sortGo,
	}
	m.rebuildVisible()

	got := make([]string, len(m.visible))
	for i, mod := range m.visible {
		got[i] = mod.Module
	}
	// highest declared go minimum first (version-aware, so 1.13 outranks 1.9); no-directive last.
	assert.Equal(t, []string{"high", "low", "oldest", "none"}, got)
}

func TestGoVerCell(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)

	plain, styled := goVerCell(bonsai.Module{GoVersion: "1.24.0"}, true)
	assert.Contains(t, plain, glyphFloor+"1.24.0", "pinner is △-flagged")
	assert.NotEqual(t, plain, styled, "pinner cell is styled")

	plain, _ = goVerCell(bonsai.Module{GoVersion: "1.18"}, false)
	assert.Contains(t, plain, "1.18")
	assert.NotContains(t, plain, glyphFloor, "non-pinner has no flag")

	plain, _ = goVerCell(bonsai.Module{GoVersion: ""}, false)
	assert.Contains(t, plain, "—", "no directive renders an em-dash")
}

func TestFloorBar(t *testing.T) {
	tests := []struct {
		name      string
		goFloor   bonsai.GoFloor
		baseFloor bonsai.GoFloor
		want      []string // substrings expected in the rendered bar
		absent    []string
	}{
		{
			name:    "no dependency floor",
			goFloor: bonsai.GoFloor{},
			want:    []string{"go floor: none"},
		},
		{
			name:      "steady floor names version and pin count",
			goFloor:   bonsai.GoFloor{Version: "1.24.0", Critical: []string{"a", "b"}},
			baseFloor: bonsai.GoFloor{Version: "1.24.0", Critical: []string{"a", "b"}},
			want:      []string{"go ≥ 1.24.0", glyphFloor + "2 pinning"},
			absent:    []string{"→"},
		},
		{
			name:      "pruning shows the floor dropping from the baseline",
			goFloor:   bonsai.GoFloor{Version: "1.21", Critical: []string{"c"}},
			baseFloor: bonsai.GoFloor{Version: "1.24.0", Critical: []string{"a"}},
			want:      []string{"1.24.0", "→", "1.21", glyphFloor + "1 pinning"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{goFloor: tt.goFloor, baseFloor: tt.baseFloor}
			got := stripStyle(m.floorBar())
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
			for _, a := range tt.absent {
				assert.NotContains(t, got, a)
			}
		})
	}
}

func TestGoDirectiveLine(t *testing.T) {
	tests := []struct {
		name    string
		detail  bonsai.Detail
		floor   bonsai.GoFloor
		pins    bool
		want    string
		notWant string
	}{
		{
			name:   "no directive declared",
			detail: bonsai.Detail{Module: "x", GoVersion: ""},
			want:   "none declared",
		},
		{
			name:   "owned module is the user's to set",
			detail: bonsai.Detail{Module: "x", Class: "1st", Controlled: true, GoVersion: "1.26.0"},
			floor:  bonsai.GoFloor{Version: "1.24.0"},
			want:   "yours",
		},
		{
			name:   "sole pinner names the version it would drop to",
			detail: bonsai.Detail{Module: "x", Class: "2nd", GoVersion: "1.24.0"},
			floor:  bonsai.GoFloor{Version: "1.24.0", Critical: []string{"x"}, NextVersion: "1.22"},
			pins:   true,
			want:   "pins go floor 1.24.0 (→ 1.22 if pruned)",
		},
		{
			name:   "one of several pinners",
			detail: bonsai.Detail{Module: "x", Class: "2nd", GoVersion: "1.24.0"},
			floor:  bonsai.GoFloor{Version: "1.24.0", Critical: []string{"x", "y"}},
			pins:   true,
			want:   "1 of 2 pinning go floor 1.24.0",
		},
		{
			name:   "below the floor is harmless",
			detail: bonsai.Detail{Module: "x", Class: "2nd", GoVersion: "1.18"},
			floor:  bonsai.GoFloor{Version: "1.24.0", Critical: []string{"y"}},
			want:   "below floor go ≥ 1.24.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{goFloor: tt.floor, floorPins: map[string]bool{}}
			if tt.pins {
				m.floorPins[tt.detail.Module] = true
			}
			got := stripStyle(m.goDirectiveLine(tt.detail))
			assert.Contains(t, got, tt.want)
			if tt.notWant != "" {
				assert.NotContains(t, got, tt.notWant)
			}
		})
	}
}

func TestHelpOverlay(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)

	// a terminal tall enough to show the whole legend at once.
	m := model{termW: 90, termH: 48}

	// ? opens the overlay; the view becomes the legend, not the explorer body.
	opened, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = opened.(model)
	require.True(t, m.showHelp, "? opens the help overlay")

	view := stripStyle(m.View())
	// the legend covers every concept the user needs to discover.
	for _, want := range []string{
		"help",
		"1st-class", "2nd-class", "3rd-class", "locked",
		"prune candidates",
		"prune-alone", "retained",
		"go mod why",
		"go floor",
		"controlled", "unlock",
	} {
		assert.Contains(t, view, want, "help overlay explains %q", want)
	}
	if os.Getenv("BONSAI_TUI_PREVIEW") != "" {
		os.Stdout.WriteString("\n" + m.View() + "\n")
	}

	// scrolling up at the top clamps to 0 and doesn't dismiss.
	up, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyUp})
	m = up.(model)
	assert.True(t, m.showHelp)
	assert.Zero(t, m.helpOffset, "scrolling up at the top clamps to 0")

	// esc closes it again rather than quitting the program.
	closed, cmd := m.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = closed.(model)
	assert.False(t, m.showHelp, "esc closes the overlay")
	assert.Nil(t, cmd, "esc out of help does not quit")
}

func TestHelpOverlayScrolls(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)

	// a short terminal can't show the whole legend, so the last sections are below the fold...
	m := model{termW: 90, termH: 20, showHelp: true}
	top := stripStyle(m.View())
	assert.Contains(t, top, "/", "a partial view shows the [from–to/total] scroll marker")
	assert.NotContains(t, top, "unlock", "the Reclassify/Keys sections start below the fold")

	// ...until you page down to them.
	for range 10 {
		next, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyPgDown})
		m = next.(model)
	}
	assert.Contains(t, stripStyle(m.View()), "unlock", "paging down reveals the lower sections")
}

// integration: build a real session for the bonsai module and exercise the model's render and
// toggle path. Skipped in -short since it compiles the binary.
func TestModelRenderAndToggle(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the target binary")
	}
	root := repoRoot(t)
	s, err := bonsai.NewSession(bonsai.Config{Dir: root, Controlled: []string{"github.com/anchore/..."}})
	if err != nil {
		t.Skipf("could not build session (environment can't build): %v", err)
	}

	m := newModel(s, State{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(model)

	view := m.View()
	assert.Contains(t, view, "binary", "summary bar present")
	assert.Contains(t, view, "Candidates", "list header present")
	assert.Contains(t, stripStyle(view), "go ≥", "go-version floor shown in the bar")
	assert.NotEmpty(t, m.baseFloor.Version, "baseline go floor computed")

	// everything starts in the binary (checked), so nothing is pruned yet.
	assert.Zero(t, m.whatif.FreedBytes, "baseline prunes nothing")
	assert.Empty(t, m.whatif.PrunedModules)

	// move to a candidate that frees something.
	for m.cursor < len(m.visible) && !(m.visible[m.cursor].Target && m.visible[m.cursor].Exclusive > 0) {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	require.Less(t, m.cursor, len(m.visible), "found a candidate with savings")

	// with nothing pruned, every dep of the highlighted module is still in the binary.
	for _, st := range m.dragStatus {
		assert.False(t, st.Freed, "no dep is removed before anything is unchecked")
	}
	if os.Getenv("BONSAI_TUI_PREVIEW") != "" {
		os.Stdout.WriteString("\n" + m.View() + "\n")
	}

	// uncheck it (prune it).
	toggled, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = toggled.(model)
	assert.NotZero(t, m.whatif.FreedBytes, "unchecking a candidate prunes bytes")
	assert.NotEmpty(t, m.whatif.PrunedModules)

	// 'a' toggles to all-modules (more rows than candidates only).
	cands := len(m.visible)
	all, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = all.(model)
	assert.Greater(t, len(m.visible), cands, "all-modules view shows more than candidates")

	// live re-classification: locking the highlighted candidate drops it from the candidate set.
	beforeCount := m.candidateCount()
	locked, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = locked.(model)
	assert.Less(t, m.candidateCount(), beforeCount, "locking a module via the UI removes it as a candidate")

	// fuzzy filter narrows the list.
	before := len(m.visible)
	slash, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = slash.(model)
	require.True(t, m.filtering, "/ enters filter mode")
	for _, r := range "yaml" {
		k, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = k.(model)
	}
	assert.NotEmpty(t, m.visible)
	assert.Less(t, len(m.visible), before, "fuzzy filter narrows the list")
	esc, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = esc.(model)
	assert.False(t, m.filtering, "esc exits filter")

	// tab focuses the detail pane; space there expands a dep's importers.
	tab, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = tab.(model)
	assert.Equal(t, focusDetail, m.focus, "tab moves focus to the detail pane")
	if len(m.dragStatus) > 0 {
		// move to a shared dep (has importers) then expand it.
		for m.detailCursor < len(m.dragStatus)-1 && len(m.dragStatus[m.detailCursor].NeededBy) == 0 {
			dn, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m = dn.(model)
		}
		exp, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		m = exp.(model)
		assert.NotEmpty(t, m.expanded, "space in the detail pane expands importers")
	}
	if os.Getenv("BONSAI_TUI_PREVIEW") != "" {
		os.Stdout.WriteString("\n" + m.View() + "\n")
	}
}

// repoRoot walks up from the test working directory to the module root (the dir whose go.mod
// declares the bonsai module), which is a buildable target for the session.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/wagoodman/bonsai") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("could not locate repo root")
		}
		dir = parent
	}
}
