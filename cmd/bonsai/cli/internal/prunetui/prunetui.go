// Package prunetui is the interactive prune explorer. Everything in the binary starts
// checked ([x] = present); you prune by unchecking ([ ] = removed) and watch the projected
// binary size and the set of modules that actually leave update live. The right panes explain
// the highlighted module: what pruning it would drag out (and what survives, held by others)
// and why it's in the build (go mod why). Read-only — enter prints the final pruned set.
package prunetui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wagoodman/bonsai/bonsai"
)

var (
	styBar  = lipgloss.NewStyle().Bold(true)
	styHelp = lipgloss.NewStyle().Faint(true)
	styDim  = lipgloss.NewStyle().Faint(true)
	styGold = lipgloss.NewStyle().Foreground(lipgloss.Color("220")) // 1st-class (yours)
	styGood = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styHead = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styRow  = lipgloss.NewStyle().Reverse(true) // highlighted row (dive-style)
)

// sort modes for the candidate list.
const (
	sortPrune = iota // by prune value (what unchecking it frees) — default
	sortSize         // by size contribution in the binary
	sortName         // alphabetical
)

// checkbox glyphs: filled = in the binary, hollow = pruned, dot = present but not a candidate.
const (
	glyphIn   = "●"
	glyphOut  = "○"
	glyphNone = "·"
)

// State is the per-target explorer state that persists across runs: which modules are pruned
// (unchecked) and the classification inputs the user set in-session.
type State struct {
	Pruned []string
	Inputs bonsai.ClassInputs
}

// Result is what the explorer returns on exit, including the state to persist.
type Result struct {
	Confirmed bool
	Pruned    []string
	State     State
}

// Run launches the explorer over a session seeded with prior State and returns the chosen
// prune set plus the state to persist.
func Run(s *bonsai.Session, initial State) (Result, error) {
	res, err := tea.NewProgram(newModel(s, initial), tea.WithAltScreen()).Run()
	if err != nil {
		return Result{}, err
	}
	m := res.(model)
	out := Result{Confirmed: m.confirmed, State: State{Pruned: keys(m.pruned), Inputs: m.inputs}}
	if m.confirmed {
		out.Pruned = m.whatif.PrunedModules
	}
	return out, nil
}

type model struct {
	s       *bonsai.Session
	all     []bonsai.Module // every module, recomputed on reclassify
	visible []bonsai.Module // filtered + sorted view
	pruned  map[string]bool // modules unchecked (marked for removal)
	inputs  bonsai.ClassInputs

	showAll  bool // show every module, not just prune candidates
	sortMode int

	cursor, offset int
	termW, termH   int

	whatif     bonsai.WhatIf
	detail     bonsai.Detail
	dragStatus []bonsai.DepStatus

	confirmed bool
}

func newModel(s *bonsai.Session, initial State) model {
	m := model{
		s:      s,
		pruned: map[string]bool{},
		inputs: initial.Inputs,
		termW:  100,
		termH:  30,
	}
	for _, p := range initial.Pruned {
		m.pruned[p] = true
	}
	if len(initial.Inputs.Controlled)+len(initial.Inputs.Locked)+len(initial.Inputs.Unlock) > 0 {
		s.Reclassify(initial.Inputs)
	}
	m.inputs = s.Inputs()
	m.all = s.Modules()
	m.rebuildVisible()
	m.recompute()
	return m
}

func (m model) Init() tea.Cmd { return nil }

func (m *model) rebuildVisible() {
	vis := make([]bonsai.Module, 0, len(m.all))
	for _, mod := range m.all {
		if m.showAll || mod.Target {
			vis = append(vis, mod)
		}
	}
	less := func(i, j int) bool {
		a, b := vis[i], vis[j]
		switch m.sortMode {
		case sortName:
			return a.Module < b.Module
		case sortSize:
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		default: // sortPrune
			if a.Exclusive != b.Exclusive {
				return a.Exclusive > b.Exclusive
			}
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		}
		return a.Module < b.Module
	}
	sort.Slice(vis, less)
	m.visible = vis
}

func (m *model) recompute() {
	m.whatif = m.s.WhatIf(m.pruned)
	if m.cursor >= len(m.visible) {
		return
	}
	cm := m.visible[m.cursor].Module
	m.detail = m.s.Detail(cm)
	// reflect the ACTUAL current selection — not a hypothesis. With nothing pruned every dep is
	// in the binary; a dep only shows removed (○) once your selection actually orphans it.
	m.dragStatus = m.s.DragOutStatus(m.pruned, cm)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		m.fixOffset()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			return m, tea.Quit
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "up", "k", "ctrl+p":
			m.move(-1)
		case "down", "j", "ctrl+n":
			m.move(1)
		case "pgup":
			m.move(-m.listH())
		case "pgdown":
			m.move(m.listH())
		case "home", "g":
			m.move(-len(m.visible))
		case "end", "G":
			m.move(len(m.visible))
		case " ", "x":
			m.togglePrune()
		case "a":
			m.refilter(func() { m.showAll = !m.showAll })
		case "s":
			m.refilter(func() { m.sortMode = (m.sortMode + 1) % 3 })
		case "c":
			m.reclass(&m.inputs.Controlled)
		case "l":
			m.reclass(&m.inputs.Locked)
		case "u":
			m.reclass(&m.inputs.Unlock)
		}
	}
	return m, nil
}

func (m *model) move(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.visible)-1)
	m.fixOffset()
	m.recompute()
}

func (m *model) fixOffset() {
	h := m.listH()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// togglePrune unchecks (prunes) / re-checks (restores) the highlighted candidate. Only prune
// targets can be toggled.
func (m *model) togglePrune() {
	if m.cursor >= len(m.visible) || !m.visible[m.cursor].Target {
		return
	}
	mod := m.visible[m.cursor].Module
	if m.pruned[mod] {
		delete(m.pruned, mod)
	} else {
		m.pruned[mod] = true
	}
	m.recompute()
}

// refilter applies a view change (filter/sort) and keeps the cursor on the same module.
func (m *model) refilter(change func()) {
	mod := ""
	if m.cursor < len(m.visible) {
		mod = m.visible[m.cursor].Module
	}
	change()
	m.rebuildVisible()
	m.cursor = indexOf(m.visible, mod)
	m.fixOffset()
	m.recompute()
}

// reclass toggles the highlighted module's membership in a classification list and re-derives
// candidates, keeping the cursor on the same module.
func (m *model) reclass(list *[]string) {
	if m.cursor >= len(m.visible) {
		return
	}
	mod := m.visible[m.cursor].Module
	*list = toggleItem(*list, mod)
	m.s.Reclassify(m.inputs)
	m.all = m.s.Modules()
	// drop pruned entries that are no longer prune targets (e.g. one just locked / made 1st-class).
	targets := map[string]bool{}
	for _, md := range m.all {
		if md.Target {
			targets[md.Module] = true
		}
	}
	for p := range m.pruned {
		if !targets[p] {
			delete(m.pruned, p)
		}
	}
	m.rebuildVisible()
	m.cursor = indexOf(m.visible, mod)
	m.fixOffset()
	m.recompute()
}

// View

func (m model) View() string {
	bodyH := max(3, m.termH-2)
	leftW := clamp(m.termW*42/100, 28, 70)
	rightW := max(24, m.termW-leftW-1)

	left := pad(m.viewList(leftW), leftW, bodyH)
	rightTopH := bodyH * 6 / 10
	right := lipgloss.JoinVertical(lipgloss.Left,
		pad(m.viewDetail(rightW), rightW, rightTopH),
		pad(m.viewWhy(rightW), rightW, bodyH-rightTopH))

	divider := styDim.Render(strings.TrimRight(strings.Repeat("│\n", bodyH), "\n"))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
	return strings.Join([]string{m.viewBar(), body, m.viewHelp()}, "\n")
}

func (m model) listH() int { return max(1, m.termH-2-1) }

func (m model) viewBar() string {
	w := m.whatif
	delta := w.OriginalSize - w.ProjectedSize
	pct := 0.0
	if w.OriginalSize > 0 {
		pct = float64(delta) / float64(w.OriginalSize) * 100
	}
	saved := styDim.Render("(unchanged)")
	if delta > 0 {
		saved = styGood.Render(fmt.Sprintf("−%s (−%.1f%%)", humize(delta), pct))
	}
	return styBar.Render(fmt.Sprintf("binary %s → %s", humize(w.OriginalSize), styGood.Render(humize(w.ProjectedSize)))) +
		"  " + saved +
		"  " + styDim.Render(fmt.Sprintf("· %d candidates unchecked · %d modules removed",
		len(m.pruned), len(w.PrunedModules)))
}

func (m model) candidateCount() int {
	n := 0
	for _, mod := range m.all {
		if mod.Target {
			n++
		}
	}
	return n
}

func (m model) viewList(width int) string {
	scope := "Candidates"
	if m.showAll {
		scope = "All modules"
	}
	lines := []string{header(fmt.Sprintf("%s · %s", scope, sortLabel(m.sortMode)), width)}
	h := m.listH()
	end := min(m.offset+h, len(m.visible))
	for i := m.offset; i < end; i++ {
		mod := m.visible[i]
		box := glyphNone
		if mod.Target {
			if m.pruned[mod.Module] {
				box = glyphOut // pruned
			} else {
				box = glyphIn // in the binary
			}
		}
		val := mod.Size
		if m.sortMode == sortPrune && mod.Exclusive > 0 {
			val = mod.Exclusive
		}
		if i == m.cursor {
			plain := fmt.Sprintf("%s %8s  %s", box, humize(val), truncate(mod.Module, width-15))
			lines = append(lines, styRow.Render(fit(plain, width)))
			continue
		}
		name := classStyle(mod.Class, mod.Controlled, mod.Locked, truncate(mod.Module, width-15))
		switch {
		case mod.Target && m.pruned[mod.Module]:
			lines = append(lines, styDim.Render(fmt.Sprintf("%s %8s  %s", glyphOut, humize(val), stripStyle(name))))
		case mod.Target:
			lines = append(lines, fmt.Sprintf("%s %8s  %s", styGood.Render(glyphIn), humize(val), name))
		default:
			lines = append(lines, fmt.Sprintf("%s %8s  %s", styDim.Render(glyphNone), humize(val), name))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) viewDetail(width int) string {
	d := m.detail
	if d.Module == "" {
		return header("Details", width)
	}
	lines := []string{
		header("Details", width),
		classStyle(d.Class, d.Controlled, d.Locked, truncate(d.Module, width)),
		styDim.Render(detailTags(d)),
		fmt.Sprintf("size %s · own %s · prune-alone %s", humize(d.Size), humize(d.Own), valStr(d)),
		styDim.Render(fmt.Sprintf("coupling %d pkgs · %d imports · %d syms · imported by %d",
			d.Coupling.ImportingPackages, d.Coupling.ImportSites, d.Coupling.DistinctSymbols, d.Importers)),
		"",
		styHead.Render("pulls in  ") + styDim.Render(glyphIn+" in binary  "+glyphOut+" pruned"),
	}
	for _, st := range m.dragStatus {
		label := st.Module
		if st.Module == "std" {
			label = "(standard library)"
		}
		if st.Freed { // removed by the current selection
			lines = append(lines, styDim.Render(fmt.Sprintf("%s %8s  %s", glyphOut, humize(st.Bytes), label)))
			continue
		}
		suffix := styDim.Render("  ← only via this")
		if len(st.NeededBy) > 0 {
			suffix = styDim.Render("  ← also needed by " + neededList(st.NeededBy))
		}
		lines = append(lines, fmt.Sprintf("%s %8s  %s%s", styGood.Render(glyphIn), humize(st.Bytes), label, suffix))
	}
	return strings.Join(lines, "\n")
}

func (m model) viewWhy(width int) string {
	lines := []string{header("Why it's here  (go mod why)", width)}
	if m.detail.Why == nil {
		lines = append(lines, styDim.Render("  (an entrypoint / nothing imports it)"))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, renderWhy(m.detail.Why, "  ", width)...)
	return strings.Join(lines, "\n")
}

func renderWhy(n *bonsai.ImportNode, prefix string, width int) []string {
	var out []string
	for _, child := range n.Via {
		gold := child.Class == "1st" || child.Class == "main"
		out = append(out, prefix+styDim.Render("← ")+classStyle(child.Class, gold, false, truncate(child.Module, width-len(prefix)-4))+styDim.Render(" ("+child.Class+")"))
		out = append(out, renderWhy(child, prefix+"  ", width)...)
	}
	if n.More > 0 {
		out = append(out, prefix+styDim.Render(fmt.Sprintf("← +%d more", n.More)))
	}
	return out
}

func (m model) viewHelp() string {
	return styHelp.Render("↑/↓ move · space prune/restore · a all/candidates · s sort · c 1st · l lock · u unlock · enter apply · q cancel")
}

// styling helpers

func classStyle(class string, controlled, locked bool, name string) string {
	switch {
	case controlled || class == "1st" || class == "main":
		return styGold.Render(name)
	case locked:
		return styDim.Render(name)
	default:
		return name
	}
}

func sortLabel(mode int) string {
	switch mode {
	case sortSize:
		return "by size"
	case sortName:
		return "A–Z"
	default:
		return "by prune value"
	}
}

func detailTags(d bonsai.Detail) string {
	parts := []string{"class " + d.Class}
	if d.Controlled {
		parts = append(parts, "1st-class (yours)")
	}
	if d.Locked {
		parts = append(parts, "locked")
	}
	if d.Target {
		parts = append(parts, "candidate")
	}
	return strings.Join(parts, " · ")
}

func valStr(d bonsai.Detail) string {
	if d.Target {
		return styGood.Render(humize(d.Exclusive))
	}
	return styDim.Render("—")
}

func header(title string, width int) string {
	rule := ""
	if n := width - lipgloss.Width(title) - 1; n > 0 {
		rule = " " + strings.Repeat("─", n)
	}
	return styHead.Render(title) + styDim.Render(rule)
}

func pad(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(ln)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

// neededList renders the other modules keeping a dep alive — compact short names, capped.
func neededList(mods []string) string {
	const n = 2
	short := make([]string, 0, n)
	for i, mod := range mods {
		if i >= n {
			break
		}
		short = append(short, shortName(mod))
	}
	s := strings.Join(short, ", ")
	if len(mods) > n {
		s += fmt.Sprintf(" +%d", len(mods)-n)
	}
	return s
}

func shortName(m string) string {
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		return m[i+1:]
	}
	return m
}

func humize(b uint64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGT"[exp])
}

func truncate(s string, width int) string {
	if width < 4 || len(s) <= width {
		return s
	}
	return "…" + s[len(s)-(width-1):]
}

func fit(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func stripStyle(s string) string {
	for {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 {
			return s
		}
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+1:]
	}
}

func toggleItem(list []string, item string) []string {
	for i, v := range list {
		if v == item {
			return append(list[:i], list[i+1:]...)
		}
	}
	return append(list, item)
}

func indexOf(mods []bonsai.Module, module string) int {
	for i, mod := range mods {
		if mod.Module == module {
			return i
		}
	}
	return 0
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
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
