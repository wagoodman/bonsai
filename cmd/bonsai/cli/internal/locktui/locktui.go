// Package locktui is an interactive fuzzy multi-select for editing bonsai's lock list.
// Type to filter, space to toggle, enter to save, esc to cancel.
package locktui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/tui"
)

// Item is one selectable dependency module.
type Item struct {
	Module string
	Direct bool
}

// Run launches the editor over items, with preselected modules already checked. It returns
// the chosen module paths and whether the user confirmed (enter) rather than cancelled.
func Run(items []Item, preselected map[string]bool) ([]string, bool, error) {
	res, err := tea.NewProgram(newModel(items, preselected), tea.WithAltScreen()).Run()
	if err != nil {
		return nil, false, err
	}
	m := res.(model)
	if !m.confirmed {
		return nil, false, nil
	}
	out := make([]string, 0, len(m.selected))
	for _, it := range items {
		if m.selected[it.Module] {
			out = append(out, it.Module)
		}
	}
	return out, true, nil
}

type model struct {
	items    []Item
	names    []string // item modules, parallel to items, for fuzzy matching
	selected map[string]bool

	filter  textinput.Model
	matches []int // indices into items, filtered + ranked
	cursor  int   // index into matches
	offset  int   // first visible match (scroll)
	height  int   // visible list rows
	width   int   // terminal width, for the full-row cursor bar

	confirmed bool
}

func newModel(items []Item, preselected map[string]bool) model {
	ti := tui.NewFilter("filter modules…")
	ti.Focus()

	sel := map[string]bool{}
	for mod := range preselected {
		sel[mod] = true
	}
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Module
	}

	m := model{
		items:    items,
		names:    names,
		selected: sel,
		filter:   ti,
		height:   15,
	}
	m.recompute()
	return m
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// reserve rows for title, filter, blank line, and help.
		m.height = max(3, msg.Height-5)
		m.width = msg.Width
		m.fixOffset()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "up", "ctrl+p":
			m.move(-1)
			return m, nil
		case "down", "ctrl+n":
			m.move(1)
			return m, nil
		case "pgup":
			m.move(-m.height)
			return m, nil
		case "pgdown":
			m.move(m.height)
			return m, nil
		case " ", "tab":
			// toggle the highlighted item; never typed into the filter (module paths have
			// no spaces, so the filter doesn't need a space key).
			if m.cursor < len(m.matches) {
				mod := m.items[m.matches[m.cursor]].Module
				if m.selected[mod] {
					delete(m.selected, mod)
				} else {
					m.selected[mod] = true
				}
			}
			return m, nil
		}

		var cmd tea.Cmd
		prev := m.filter.Value()
		m.filter, cmd = m.filter.Update(msg)
		if m.filter.Value() != prev {
			tui.StyleFilter(&m.filter)
			m.cursor = 0
			m.offset = 0
			m.recompute()
		}
		return m, cmd
	}

	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	return m, cmd
}

func (m *model) move(delta int) {
	if len(m.matches) == 0 {
		return
	}
	m.cursor = tui.Clamp(m.cursor+delta, 0, len(m.matches)-1)
	m.fixOffset()
}

func (m *model) fixOffset() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *model) recompute() {
	q := strings.TrimSpace(m.filter.Value())
	if q == "" {
		m.matches = m.matches[:0]
		for i := range m.items {
			m.matches = append(m.matches, i)
		}
	} else {
		found := fuzzy.Find(q, m.names)
		m.matches = make([]int, len(found))
		for i, f := range found {
			m.matches[i] = f.Index
		}
	}
	if m.cursor >= len(m.matches) {
		m.cursor = max(0, len(m.matches)-1)
	}
	m.fixOffset()
}

func (m model) View() string {
	var b strings.Builder
	fmt.Fprintln(&b, tui.Title.Render("Select modules to lock")+tui.Dim.Render("  (never suggested for pruning)"))
	fmt.Fprintln(&b, m.filter.View())
	fmt.Fprintln(&b)

	if len(m.matches) == 0 {
		fmt.Fprintln(&b, tui.Dim.Render("  no modules match"))
	}
	end := min(m.offset+m.height, len(m.matches))
	for i := m.offset; i < end; i++ {
		it := m.items[m.matches[i]]
		selected := m.selected[it.Module]
		// the cursor row is a full-width purple bar with plain text (matches the prune explorer),
		// not a ▸ arrow.
		if i == m.cursor {
			glyph := tui.GlyphOff
			if selected {
				glyph = tui.GlyphOn
			}
			plain := fmt.Sprintf("%s %s%s", glyph, it.Module, suffixPlain(it))
			fmt.Fprintln(&b, tui.RowCursor.Render(tui.Fit(plain, max(m.width, 1))))
			continue
		}
		// selection rides the greyscale axis: unchecked rows recede (dim glyph + name), checked
		// rows step forward (normal weight). No hue — green is reserved for freed weight elsewhere.
		glyph := tui.Dim.Render(tui.GlyphOff)
		modName := tui.Dim.Render(it.Module)
		if selected {
			glyph = tui.GlyphOn
			modName = it.Module
		}
		fmt.Fprintf(&b, "%s %s\n", glyph, modName+directSuffix(it))
	}

	fmt.Fprintln(&b)
	// no trailing newline: the chrome (title/filter/blanks/help) plus a full list fills the
	// terminal exactly, and a trailing newline would tip it one line over, scrolling the title off.
	fmt.Fprint(&b, tui.Help.Render(fmt.Sprintf(
		"space toggle · ↑/↓ move · enter save · esc cancel — %d selected, %d shown",
		len(m.selected), len(m.matches))))
	return b.String()
}

func directSuffix(it Item) string {
	if it.Direct {
		return " (direct)" // normal weight: direct deps lead, indirect recede
	}
	return tui.Dim.Render(" (indirect)")
}

// suffixPlain is directSuffix without styling, for the cursor row (which carries its own bar
// style and would otherwise embed conflicting color resets).
func suffixPlain(it Item) string {
	if it.Direct {
		return " (direct)"
	}
	return " (indirect)"
}
