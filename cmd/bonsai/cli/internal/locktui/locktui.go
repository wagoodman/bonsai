// Package locktui is the build-free editor for bonsai's three module-policy lists: locked,
// controlled, and unlocked. You mark a set of rows (modules or typed patterns), then apply a
// class to the whole marked set. It mirrors explore's keys (l/c/u, /-to-filter) so the two
// editors feel like one tool, but needs only `go list`, no build.
package locktui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/tui"
	"github.com/wagoodman/bonsai/internal/bonsai"
)

// Item is one row: a concrete dependency module, or a user-typed pattern entry (Pattern true).
type Item struct {
	Module  string
	Direct  bool
	Pattern bool
}

// Lists is the editor's input and output: the three policy lists keyed by module path or pattern.
type Lists struct {
	Locked, Controlled, Unlock []string
}

// Run launches the editor over items, seeded with the initial three lists. It returns the final
// lists and whether the user confirmed (enter) rather than cancelled (esc/q).
func Run(items []Item, initial Lists) (Lists, bool, error) {
	res, err := tea.NewProgram(newModel(items, initial), tea.WithAltScreen()).Run()
	if err != nil {
		return Lists{}, false, err
	}
	m := res.(model)
	if !m.confirmed {
		return Lists{}, false, nil
	}
	return m.lists(), true, nil
}

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeAdding
)

// repeated key strings, pulled out so goconst stays quiet.
const (
	keyEsc   = "esc"
	keyEnter = "enter"
)

// class badge styles: explicit membership is a bright [X]; covered-by-a-broader-pattern is a
// faint ·X so the user sees why a module already reads as locked/controlled.
var (
	styLocked     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)   // red
	styControlled = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true) // gold
	styUnlock     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)   // cyan
)

type model struct {
	items []Item
	names []string // item modules, parallel to items, for fuzzy matching

	marked     map[string]bool // the mark set, independent of class membership
	locked     map[string]bool // explicit membership in each list, keyed by module/pattern
	controlled map[string]bool
	unlock     map[string]bool

	mode   mode
	filter textinput.Model
	adder  textinput.Model

	matches []int // indices into items, filtered + ranked
	cursor  int   // index into matches
	offset  int   // first visible match (scroll)
	height  int   // visible list rows
	width   int   // terminal width, for the full-row cursor bar

	confirmed bool
}

func newModel(items []Item, initial Lists) model {
	ti := tui.NewFilter("filter modules…")
	add := textinput.New()
	add.Prompt = "+ "
	add.Placeholder = "github.com/anchore/..."

	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Module
	}

	m := model{
		items:      items,
		names:      names,
		marked:     map[string]bool{},
		locked:     toSet(initial.Locked),
		controlled: toSet(initial.Controlled),
		unlock:     toSet(initial.Unlock),
		filter:     ti,
		adder:      add,
		height:     15,
	}
	m.recompute()
	return m
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// reserve rows for title, control line, blank line, and help.
		m.height = max(3, msg.Height-5)
		m.width = msg.Width
		m.fixOffset()
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeFilter:
			return m.updateFilter(msg)
		case modeAdding:
			return m.updateAdding(msg)
		default:
			return m.updateNormal(msg)
		}
	}
	return m, nil
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", keyEsc, "q":
		return m, tea.Quit
	case keyEnter:
		m.confirmed = true
		return m, tea.Quit
	case "up", "ctrl+p":
		m.move(-1)
	case "down", "ctrl+n":
		m.move(1)
	case "pgup":
		m.move(-m.height)
	case "pgdown":
		m.move(m.height)
	case " ", "tab":
		// toggle the mark on the cursor row.
		if mod, ok := m.cursorMod(); ok {
			toggle(m.marked, mod)
		}
	case "a":
		// toggle-mark every currently shown row: if all are marked, clear them; else mark all.
		all := true
		for _, idx := range m.matches {
			if !m.marked[m.items[idx].Module] {
				all = false
				break
			}
		}
		for _, idx := range m.matches {
			mod := m.items[idx].Module
			if all {
				delete(m.marked, mod)
			} else {
				m.marked[mod] = true
			}
		}
	case "l":
		m.apply(m.locked)
	case "c":
		m.apply(m.controlled)
	case "u":
		m.apply(m.unlock)
	case "i", "+":
		m.mode = modeAdding
		m.filter.Blur()
		return m, m.adder.Focus()
	case "/":
		m.mode = modeFilter
		return m, m.filter.Focus()
	}
	return m, nil
}

func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m.filter.SetValue("")
		tui.StyleFilter(&m.filter)
		m.filter.Blur()
		m.mode = modeNormal
		m.cursor, m.offset = 0, 0
		m.recompute()
		return m, nil
	case keyEnter:
		m.filter.Blur()
		m.mode = modeNormal
		return m, nil
	}
	var cmd tea.Cmd
	prev := m.filter.Value()
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != prev {
		tui.StyleFilter(&m.filter)
		m.cursor, m.offset = 0, 0
		m.recompute()
	}
	return m, cmd
}

func (m model) updateAdding(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m.adder.SetValue("")
		m.adder.Blur()
		m.mode = modeNormal
		return m, nil
	case keyEnter:
		if v := strings.TrimSpace(m.adder.Value()); v != "" {
			m.addPattern(v)
		}
		m.adder.SetValue("")
		m.adder.Blur()
		m.mode = modeNormal
		return m, nil
	}
	var cmd tea.Cmd
	m.adder, cmd = m.adder.Update(msg)
	return m, cmd
}

// apply bulk-toggles a class over the marked rows (or the cursor row if nothing is marked): if
// every target already has the class it is removed from all, otherwise it is added to all.
func (m *model) apply(set map[string]bool) {
	rows := m.targets()
	if len(rows) == 0 {
		return
	}
	allIn := true
	for _, r := range rows {
		if !set[r] {
			allIn = false
			break
		}
	}
	for _, r := range rows {
		if allIn {
			delete(set, r)
		} else {
			set[r] = true
		}
	}
}

// targets is the marked set, or the single cursor row when nothing is marked.
func (m model) targets() []string {
	if len(m.marked) > 0 {
		out := make([]string, 0, len(m.marked))
		for r := range m.marked {
			out = append(out, r)
		}
		return out
	}
	if mod, ok := m.cursorMod(); ok {
		return []string{mod}
	}
	return nil
}

func (m model) cursorMod() (string, bool) {
	if m.cursor < len(m.matches) {
		return m.items[m.matches[m.cursor]].Module, true
	}
	return "", false
}

// addPattern appends a typed pattern as a new row and marks it; if the string already names a
// row, it just marks that row.
func (m *model) addPattern(s string) {
	for _, it := range m.items {
		if it.Module == s {
			m.marked[s] = true
			return
		}
	}
	m.items = append(m.items, Item{Module: s, Pattern: true})
	m.names = append(m.names, s)
	m.marked[s] = true
	m.recompute()
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

// lists returns the three policy lists, each sorted and de-duplicated (keys are unique already).
func (m model) lists() Lists {
	return Lists{
		Locked:     sortedKeys(m.locked),
		Controlled: sortedKeys(m.controlled),
		Unlock:     sortedKeys(m.unlock),
	}
}

func (m model) View() string {
	var b strings.Builder
	fmt.Fprintln(&b, tui.Title.Render("Edit module policy")+tui.Dim.Render("  (locked / controlled / unlocked)"))

	// one control line, stable height: filter when filtering, add-prompt when adding, else a
	// dim summary of the mark set.
	switch m.mode {
	case modeFilter:
		fmt.Fprintln(&b, m.filter.View())
	case modeAdding:
		fmt.Fprintln(&b, m.adder.View())
	default:
		fmt.Fprintln(&b, tui.Dim.Render(fmt.Sprintf("%d marked · %d shown", len(m.marked), len(m.matches))))
	}
	fmt.Fprintln(&b)

	if len(m.matches) == 0 {
		fmt.Fprintln(&b, tui.Dim.Render("  no modules match"))
	}
	end := min(m.offset+m.height, len(m.matches))
	for i := m.offset; i < end; i++ {
		it := m.items[m.matches[i]]
		marked := m.marked[it.Module]
		if i == m.cursor {
			glyph := tui.GlyphOff
			if marked {
				glyph = tui.GlyphOn
			}
			plain := fmt.Sprintf("%s %s%s%s", glyph, it.Module, m.badgesPlain(it.Module), suffixPlain(it))
			fmt.Fprintln(&b, tui.RowCursor.Render(tui.Fit(plain, max(m.width, 1))))
			continue
		}
		// selection rides the greyscale axis: unchecked rows recede (dim glyph + name), checked
		// rows step forward (normal weight). No hue — green is reserved for freed weight elsewhere.
		glyph := tui.Dim.Render(tui.GlyphOff)
		name := tui.Dim.Render(it.Module)
		if marked {
			glyph = tui.GlyphOn
			name = it.Module
		}
		fmt.Fprintf(&b, "%s %s%s%s\n", glyph, name, m.badges(it.Module), suffix(it))
	}

	fmt.Fprintln(&b)
	fmt.Fprint(&b, tui.Help.Render(m.help()))
	return b.String()
}

func (m model) help() string {
	switch m.mode {
	case modeFilter:
		return "type to filter · enter/esc done"
	case modeAdding:
		return "type a pattern (e.g. github.com/anchore/...) · enter add · esc cancel"
	default:
		return "space mark · a all · l/c/u lock/control/unlock · i add pattern · / filter · enter save · esc cancel"
	}
}

// badges renders the [L][C][U] membership badges (explicit bright, covered-by-pattern faint).
func (m model) badges(mod string) string {
	var parts []string
	if s := classBadge("L", styLocked, m.locked[mod], !m.locked[mod] && covered(m.locked, mod)); s != "" {
		parts = append(parts, s)
	}
	if s := classBadge("C", styControlled, m.controlled[mod], !m.controlled[mod] && covered(m.controlled, mod)); s != "" {
		parts = append(parts, s)
	}
	if s := classBadge("U", styUnlock, m.unlock[mod], !m.unlock[mod] && covered(m.unlock, mod)); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "")
}

// badgesPlain is badges without color, for the cursor row's purple bar (nested resets there
// fight the bar background).
func (m model) badgesPlain(mod string) string {
	var parts []string
	for _, c := range []struct {
		label string
		set   map[string]bool
	}{{"L", m.locked}, {"C", m.controlled}, {"U", m.unlock}} {
		switch {
		case c.set[mod]:
			parts = append(parts, "["+c.label+"]")
		case covered(c.set, mod):
			parts = append(parts, "·"+c.label)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "")
}

func classBadge(label string, style lipgloss.Style, explicit, isCovered bool) string {
	switch {
	case explicit:
		return style.Render("[" + label + "]")
	case isCovered:
		return tui.Dim.Render("·" + label)
	}
	return ""
}

// covered reports whether mod is matched by some pattern in the set other than an exact entry
// for mod itself (i.e. a broader rule covers it).
func covered(set map[string]bool, mod string) bool {
	pats := make([]string, 0, len(set))
	for k := range set {
		if k != mod {
			pats = append(pats, k)
		}
	}
	return bonsai.Matches(pats, mod)
}

func suffix(it Item) string {
	if it.Pattern {
		return tui.Dim.Render(" (pattern)")
	}
	if it.Direct {
		return " (direct)" // normal weight: direct deps lead, indirect recede
	}
	return tui.Dim.Render(" (indirect)")
}

func suffixPlain(it Item) string {
	if it.Pattern {
		return " (pattern)"
	}
	if it.Direct {
		return " (direct)"
	}
	return " (indirect)"
}

func toSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out[s] = true
		}
	}
	return out
}

func toggle(set map[string]bool, key string) {
	if set[key] {
		delete(set, key)
	} else {
		set[key] = true
	}
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
