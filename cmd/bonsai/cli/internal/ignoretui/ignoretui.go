// Package ignoretui is an interactive fuzzy multi-select for editing bonsai's ignore list.
// Type to filter, space to toggle, enter to save, esc to cancel.
package ignoretui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// Item is one selectable dependency module.
type Item struct {
	Module string
	Direct bool
}

var (
	styTitle  = lipgloss.NewStyle().Bold(true)
	styHelp   = lipgloss.NewStyle().Faint(true)
	styDirect = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	styDim    = lipgloss.NewStyle().Faint(true)
	styCursor = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5")) // magenta
	styCheck  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
)

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

	confirmed bool
}

func newModel(items []Item, preselected map[string]bool) model {
	ti := textinput.New()
	ti.Placeholder = "filter modules…"
	ti.Prompt = "filter> "
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
	m.cursor = clamp(m.cursor+delta, 0, len(m.matches)-1)
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
	fmt.Fprintln(&b, styTitle.Render("Select modules to ignore")+styDim.Render("  (never suggested for pruning)"))
	fmt.Fprintln(&b, m.filter.View())
	fmt.Fprintln(&b)

	if len(m.matches) == 0 {
		fmt.Fprintln(&b, styDim.Render("  no modules match"))
	}
	end := min(m.offset+m.height, len(m.matches))
	for i := m.offset; i < end; i++ {
		it := m.items[m.matches[i]]
		cursor := "  "
		if i == m.cursor {
			cursor = styCursor.Render("▸ ")
		}
		check := "[ ]"
		if m.selected[it.Module] {
			check = styCheck.Render("[x]")
		}
		// de-emphasize rows that are neither highlighted nor selected.
		modName := it.Module
		if i != m.cursor && !m.selected[it.Module] {
			modName = styDim.Render(modName)
		}
		fmt.Fprintf(&b, "%s%s %s\n", cursor, check, modName+directSuffix(it))
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, styHelp.Render(fmt.Sprintf(
		"space toggle · ↑/↓ move · enter save · esc cancel — %d selected, %d shown",
		len(m.selected), len(m.matches))))
	return b.String()
}

func directSuffix(it Item) string {
	if it.Direct {
		return styDirect.Render(" (direct)")
	}
	return styDim.Render(" (indirect)")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
