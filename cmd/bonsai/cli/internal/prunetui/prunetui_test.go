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
