package locktui

import (
	"fmt"
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

func TestSeedsLists(t *testing.T) {
	m := newModel(testItems(), Lists{Locked: []string{"github.com/spf13/cobra"}, Controlled: []string{"golang.org/x/text"}})
	assert.True(t, m.locked["github.com/spf13/cobra"])
	assert.True(t, m.controlled["golang.org/x/text"])
	assert.Empty(t, m.marked)
	assert.Len(t, m.matches, 4)
}

func TestMarkThenApplyLockToSelection(t *testing.T) {
	m := newModel(testItems(), Lists{})
	// mark the first two rows.
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	require.Len(t, m.marked, 2)

	m = send(m, key("l")) // apply lock to the marked set
	assert.True(t, m.locked[m.items[0].Module])
	assert.True(t, m.locked[m.items[1].Module])
	assert.False(t, m.locked[m.items[2].Module])

	m = send(m, key("l")) // applying again toggles the whole set off
	assert.Empty(t, m.locked)
}

func TestApplyClassToCursorWhenNothingMarked(t *testing.T) {
	m := newModel(testItems(), Lists{})
	mod := m.items[m.matches[m.cursor]].Module
	m = send(m, key("c"))
	assert.True(t, m.controlled[mod])
}

func TestMarkAllShownThenControl(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("a")) // mark every shown row
	assert.Len(t, m.marked, 4)
	m = send(m, key("c"))
	assert.Len(t, m.controlled, 4)
	m = send(m, key("a")) // a again unmarks all shown
	assert.Empty(t, m.marked)
}

func TestFilterThenMarkAllOnlyMarksSubset(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("/")) // enter filter mode
	require.Equal(t, modeFilter, m.mode)
	m = send(m, key("cobra"))
	require.Less(t, len(m.matches), 4)
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter}) // leave filter mode, keep query
	require.Equal(t, modeNormal, m.mode)

	m = send(m, key("a")) // marks only the filtered rows
	assert.Len(t, m.marked, len(m.matches))
	assert.True(t, m.marked["github.com/spf13/cobra"])
}

func TestAddPatternRowAndAssign(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("i")) // enter add mode
	require.Equal(t, modeAdding, m.mode)
	m = send(m, key("github.com/anchore/..."))
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, modeNormal, m.mode)

	// the new pattern row exists, is marked, and is a pattern.
	require.Len(t, m.items, 5)
	last := m.items[4]
	assert.Equal(t, "github.com/anchore/...", last.Module)
	assert.True(t, last.Pattern)
	assert.True(t, m.marked[last.Module])

	m = send(m, key("u")) // assign unlock to the marked (new pattern) row
	assert.True(t, m.unlock["github.com/anchore/..."])

	out := m.lists()
	assert.Equal(t, []string{"github.com/anchore/..."}, out.Unlock)
}

func TestEnterConfirmsEscCancels(t *testing.T) {
	confirm := send(newModel(testItems(), Lists{}), tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, confirm.confirmed)

	cancel := send(newModel(testItems(), Lists{}), tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, cancel.confirmed)
}

func TestCoveredByPattern(t *testing.T) {
	items := append(testItems(), Item{Module: "github.com/anchore/...", Pattern: true})
	m := newModel(items, Lists{Locked: []string{"github.com/anchore/..."}})
	// a concrete anchore module would be covered; none in testItems, so use the pattern's reach
	// against a synthetic module via the covered helper.
	assert.True(t, covered(m.locked, "github.com/anchore/stereoscope"))
	assert.False(t, covered(m.locked, "github.com/spf13/cobra"))
	// the explicit pattern entry itself isn't "covered" (it's explicit).
	assert.False(t, covered(m.locked, "github.com/anchore/..."))
}

func TestNavigationClamps(t *testing.T) {
	m := newModel(testItems(), Lists{})
	for range 10 {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	assert.Equal(t, len(m.matches)-1, m.cursor)
	for range 10 {
		m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	assert.Equal(t, 0, m.cursor)
}

// manyItems builds n distinct module rows for scrolling tests.
func manyItems(n int) []Item {
	out := make([]Item, n)
	for i := range n {
		out[i] = Item{Module: fmt.Sprintf("example.com/mod%02d", i), Direct: true}
	}
	return out
}

func TestFilterEscClearsQuery(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("/"))
	m = send(m, key("cobra"))
	require.Less(t, len(m.matches), 4)

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, modeNormal, m.mode)
	assert.Equal(t, "", m.filter.Value())
	assert.Len(t, m.matches, 4) // all rows restored
	assert.Equal(t, 0, m.cursor)
}

func TestAddEscCancels(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("i"))
	m = send(m, key("github.com/anchore/..."))
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})

	assert.Equal(t, modeNormal, m.mode)
	assert.Len(t, m.items, 4) // nothing added
	assert.Equal(t, "", m.adder.Value())
}

func TestAddEmptyInputAddsNothing(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("i"))
	m = send(m, key("   ")) // whitespace only
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	assert.Equal(t, modeNormal, m.mode)
	assert.Len(t, m.items, 4)
}

func TestAddPatternDedupsExistingRow(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("i"))
	m = send(m, key("github.com/spf13/cobra")) // already a row
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	assert.Len(t, m.items, 4) // no duplicate row
	assert.True(t, m.marked["github.com/spf13/cobra"])
}

func TestApplyPromotesMixedMembershipToAll(t *testing.T) {
	m := newModel(testItems(), Lists{})
	// lock only the first row directly (cursor on row 0, nothing marked).
	m = send(m, key("l"))
	require.True(t, m.locked[m.items[0].Module])

	// now mark rows 0 and 1, then apply lock: partial membership should promote all, not toggle off.
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	require.Len(t, m.marked, 2)

	m = send(m, key("l"))
	assert.True(t, m.locked[m.items[0].Module])
	assert.True(t, m.locked[m.items[1].Module])
}

func TestTargetsEmptyIsNoOp(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, key("/"))
	m = send(m, key("zzznomatch"))
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	require.Empty(t, m.matches)

	// nothing marked, no cursor row: l/c/u must not panic or add anything.
	m = send(m, key("l"))
	m = send(m, key("c"))
	m = send(m, key("u"))
	assert.Empty(t, m.locked)
	assert.Empty(t, m.controlled)
	assert.Empty(t, m.unlock)
}

func TestWindowSizeSetsHeightAndClamps(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	assert.Equal(t, 15, m.height) // 20 - 5 reserved rows
	assert.Equal(t, 100, m.width)

	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 4})
	assert.Equal(t, 3, m.height) // clamped to a 3-row floor
}

func TestScrollOffsetTracksCursor(t *testing.T) {
	m := newModel(manyItems(30), Lists{})
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 13}) // height 8
	require.Equal(t, 8, m.height)
	require.Equal(t, 0, m.offset)

	// page down past the window; cursor must stay visible within [offset, offset+height).
	m = send(m, tea.KeyMsg{Type: tea.KeyPgDown})
	assert.GreaterOrEqual(t, m.cursor, m.offset)
	assert.Less(t, m.cursor, m.offset+m.height)

	// drive to the bottom; offset should reach the last full window.
	for range 30 {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	assert.Equal(t, len(m.matches)-1, m.cursor)
	assert.Equal(t, len(m.matches)-m.height, m.offset)

	// page back to the top; offset returns to 0.
	for range 30 {
		m = send(m, tea.KeyMsg{Type: tea.KeyPgUp})
	}
	assert.Equal(t, 0, m.cursor)
	assert.Equal(t, 0, m.offset)
}

func TestRecomputeClampsCursorOnShrink(t *testing.T) {
	m := newModel(testItems(), Lists{})
	m.cursor = 3 // last row

	// shrink the match set out from under the cursor; recompute must clamp it back in range.
	m.filter.SetValue("cobra")
	m.recompute()
	require.Less(t, len(m.matches), 4)
	assert.Less(t, m.cursor, len(m.matches))
	assert.Equal(t, len(m.matches)-1, m.cursor)
}

func TestBadgesPlainExplicitVsCovered(t *testing.T) {
	items := append(testItems(), Item{Module: "github.com/anchore/...", Pattern: true})
	m := newModel(items, Lists{Locked: []string{"github.com/anchore/..."}})

	// the pattern entry itself is explicit: [L].
	assert.Equal(t, " [L]", m.badgesPlain("github.com/anchore/..."))
	// a concrete module under the pattern is only covered: ·L.
	assert.Equal(t, " ·L", m.badgesPlain("github.com/anchore/stereoscope"))
	// an unrelated module gets nothing.
	assert.Equal(t, "", m.badgesPlain("github.com/spf13/cobra"))
}
