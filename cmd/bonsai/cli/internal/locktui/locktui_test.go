package locktui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testItems() []Item {
	return []Item{
		{Module: "github.com/charmbracelet/lipgloss", Direct: true},
		{Module: "github.com/charmbracelet/bubbletea", Direct: true},
		{Module: "github.com/spf13/cobra", Direct: true},
		{Module: "golang.org/x/text", Direct: false},
	}
}

// send applies a key message to the model and returns the updated model.
func send(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestPreselection(t *testing.T) {
	m := newModel(testItems(), map[string]bool{"github.com/spf13/cobra": true})
	assert.True(t, m.selected["github.com/spf13/cobra"])
	assert.False(t, m.selected["golang.org/x/text"])
	assert.Len(t, m.matches, 4)
}

func TestSpaceToggles(t *testing.T) {
	m := newModel(testItems(), nil)
	// cursor starts at 0 → first item (sorted order preserved from input here).
	first := m.items[m.matches[m.cursor]].Module

	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	assert.True(t, m.selected[first], "space selects highlighted item")

	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	assert.False(t, m.selected[first], "space again deselects")
}

func TestFilterNarrowsMatches(t *testing.T) {
	m := newModel(testItems(), nil)
	require.Len(t, m.matches, 4)

	// fuzzy matching is subsequence-based, so the count may exceed one, but an exact
	// substring should rank first and the set should narrow.
	m = send(m, key("cobra"))
	require.NotEmpty(t, m.matches)
	assert.Less(t, len(m.matches), 4, "filter narrows the candidate set")
	assert.Equal(t, "github.com/spf13/cobra", m.items[m.matches[0]].Module, "best match ranks first")

	// toggling under a filter selects the highlighted (filtered) item.
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	assert.True(t, m.selected["github.com/spf13/cobra"])
}

func TestEnterConfirmsEscCancels(t *testing.T) {
	confirm := send(newModel(testItems(), nil), tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, confirm.confirmed)

	cancel := send(newModel(testItems(), nil), tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, cancel.confirmed)
}

func TestNavigationClamps(t *testing.T) {
	m := newModel(testItems(), nil)
	for range 10 { // hold down past the end
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	assert.Equal(t, len(m.matches)-1, m.cursor)
	for range 10 { // and back past the top
		m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	assert.Equal(t, 0, m.cursor)
}
