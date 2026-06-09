// Package prunetui is the interactive prune explorer. Everything in the binary starts checked
// (● = present); you prune by unchecking (○ = removed) and watch the projected binary size and
// the modules that actually leave update live. Tab moves focus between the left list and the
// right detail / why panes (each scrolls); the detail pane explains what the highlighted module
// pulls in and, on demand, who keeps each shared dep; the why pane is go-mod-why. Read-only —
// enter prints the final pruned set.
package prunetui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/wagoodman/bonsai/bonsai"
)

var (
	styBar   = lipgloss.NewStyle().Bold(true)
	styHelp  = lipgloss.NewStyle().Faint(true)
	styDim   = lipgloss.NewStyle().Faint(true)
	styGold  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")) // 1st-class (yours)
	styGood  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow: go-floor pinners
	styCyan  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styHead  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styRow   = lipgloss.NewStyle().Reverse(true)
	styFocus = lipgloss.NewStyle().Bold(true).Reverse(true)
)

const (
	glyphIn    = "●" // in the binary
	glyphOut   = "○" // pruned
	glyphNone  = "·" // present, not a candidate
	glyphFloor = "△" // this module pins the go-version floor
)

const (
	focusList = iota
	focusDetail
	focusWhy
)

const (
	sortPrune = iota
	sortSize
	sortName
	sortGo
	sortModes // count of sort modes, for cycling
)

const detailInfoLines = 8 // module info (size/own, prune-alone, coupling, go directive) + "pulls in" header

// State is the per-target explorer state that persists across runs.
type State struct {
	Pruned []string
	Inputs bonsai.ClassInputs
}

// Result is what the explorer returns on exit.
type Result struct {
	Confirmed bool
	Pruned    []string
	State     State
}

// Run launches the explorer and returns the chosen prune set plus the state to persist.
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
	all     []bonsai.Module
	visible []bonsai.Module
	pruned  map[string]bool
	inputs  bonsai.ClassInputs

	showAll   bool
	sortMode  int
	filter    textinput.Model
	filtering bool

	focus  int
	cursor int // list cursor
	offset int // list scroll

	detailModule string
	detailCursor int // index into dragStatus (focusDetail)
	detailOffset int
	expanded     map[string]bool // dep module -> show its importers
	whyOffset    int

	termW, termH int

	whatif     bonsai.WhatIf
	detail     bonsai.Detail
	dragStatus []bonsai.DepStatus

	goFloor   bonsai.GoFloor  // projected go floor under the current selection
	baseFloor bonsai.GoFloor  // go floor with nothing pruned (to show how far pruning moves it)
	floorPins map[string]bool // surviving modules pinning the current floor (Critical, for row marking)

	confirmed bool
}

func newModel(s *bonsai.Session, initial State) model {
	ti := textinput.New()
	ti.Prompt = "filter> "
	ti.Placeholder = "fuzzy match…"

	m := model{
		s:        s,
		pruned:   map[string]bool{},
		inputs:   initial.Inputs,
		filter:   ti,
		expanded: map[string]bool{},
		termW:    100,
		termH:    30,
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
	base := make([]bonsai.Module, 0, len(m.all))
	for _, mod := range m.all {
		if m.showAll || mod.Target {
			base = append(base, mod)
		}
	}
	if q := strings.TrimSpace(m.filter.Value()); q != "" {
		names := make([]string, len(base))
		for i, mod := range base {
			names[i] = mod.Module
		}
		ranked := fuzzy.Find(q, names)
		filtered := make([]bonsai.Module, len(ranked))
		for i, r := range ranked {
			filtered[i] = base[r.Index]
		}
		m.visible = filtered // fuzzy already ranks; don't re-sort
		return
	}
	sort.Slice(base, func(i, j int) bool {
		a, b := base[i], base[j]
		switch m.sortMode {
		case sortName:
			return a.Module < b.Module
		case sortSize:
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		case sortGo:
			// highest required go version first, so the modules pinning the floor float up; ties
			// fall back to size.
			if c := bonsai.CompareGoVersions(a.GoVersion, b.GoVersion); c != 0 {
				return c > 0
			}
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		default:
			if a.Exclusive != b.Exclusive {
				return a.Exclusive > b.Exclusive
			}
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		}
		return a.Module < b.Module
	})
	m.visible = base
}

func (m *model) recompute() {
	m.whatif = m.s.WhatIf(m.pruned)
	m.goFloor = m.s.GoFloor(m.pruned)
	m.baseFloor = m.s.GoFloor(nil)
	m.floorPins = make(map[string]bool, len(m.goFloor.Critical))
	for _, mod := range m.goFloor.Critical {
		m.floorPins[mod] = true
	}
	if m.cursor >= len(m.visible) {
		m.dragStatus = nil
		return
	}
	cm := m.visible[m.cursor].Module
	m.detail = m.s.Detail(cm)
	m.dragStatus = m.s.DragOutStatus(m.pruned, cm)
	if cm != m.detailModule { // moved to a new module: reset detail-pane navigation
		m.detailModule = cm
		m.detailCursor, m.detailOffset = 0, 0
		m.expanded = map[string]bool{}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		m.fixOffset()
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			return m.updateFilter(msg)
		}
		return m.updateKey(msg)
	}
	return m, nil
}

func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filter.SetValue("")
		m.filtering = false
		m.rebuildVisible()
		m.clampCursor()
		m.recompute()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	prev := m.filter.Value()
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != prev {
		m.rebuildVisible()
		m.clampCursor()
		m.recompute()
	}
	return m, cmd
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		return m, tea.Quit
	case "enter":
		m.confirmed = true
		return m, tea.Quit
	case "tab":
		m.focus = (m.focus + 1) % 3
		return m, nil
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
		return m, nil
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	}

	switch m.focus {
	case focusDetail:
		return m.updateDetail(msg)
	case focusWhy:
		return m.updateWhy(msg)
	default:
		return m.updateList(msg)
	}
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "ctrl+p":
		m.moveList(-1)
	case "down", "j", "ctrl+n":
		m.moveList(1)
	case "pgup":
		m.moveList(-m.listH())
	case "pgdown":
		m.moveList(m.listH())
	case "home", "g":
		m.moveList(-len(m.visible))
	case "end", "G":
		m.moveList(len(m.visible))
	case " ", "x":
		m.togglePrune()
	case "a":
		m.refilter(func() { m.showAll = !m.showAll })
	case "s":
		m.refilter(func() { m.sortMode = (m.sortMode + 1) % sortModes })
	case "c":
		m.reclass(&m.inputs.Controlled)
	case "l":
		m.reclass(&m.inputs.Locked)
	case "u":
		m.reclass(&m.inputs.Unlock)
	}
	return m, nil
}

func (m model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < len(m.dragStatus)-1 {
			m.detailCursor++
		}
	case " ", "x", "right", "l", "enter":
		if m.detailCursor < len(m.dragStatus) {
			mod := m.dragStatus[m.detailCursor].Module
			m.expanded[mod] = !m.expanded[mod]
		}
		if msg.String() == "enter" { // enter shouldn't quit while exploring the detail pane
			return m, nil
		}
	}
	m.scrollDetailToCursor()
	return m, nil
}

func (m model) updateWhy(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.whyOffset = max(0, m.whyOffset-1)
	case "down", "j":
		m.whyOffset++
	case "pgup":
		m.whyOffset = max(0, m.whyOffset-5)
	case "pgdown":
		m.whyOffset += 5
	}
	return m, nil
}

func (m *model) moveList(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.visible)-1)
	m.fixOffset()
	m.recompute()
}

func (m *model) clampCursor() {
	m.cursor = clamp(m.cursor, 0, max(0, len(m.visible)-1))
	m.offset = 0
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

func (m *model) scrollDetailToCursor() {
	_, _, _, detailH, _ := m.layout()
	avail := max(1, detailH-1)
	line := detailInfoLines
	for k := 0; k < m.detailCursor && k < len(m.dragStatus); k++ {
		line++
		if m.expanded[m.dragStatus[k].Module] {
			line += len(m.dragStatus[k].NeededBy)
		}
	}
	if line < m.detailOffset {
		m.detailOffset = line
	}
	if line >= m.detailOffset+avail {
		m.detailOffset = line - avail + 1
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
}

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

func (m *model) refilter(change func()) {
	mod := m.cursorModule()
	change()
	m.rebuildVisible()
	m.cursor = indexOf(m.visible, mod)
	m.fixOffset()
	m.recompute()
}

func (m *model) reclass(list *[]string) {
	mod := m.cursorModule()
	if mod == "" {
		return
	}
	*list = toggleItem(*list, mod)
	m.s.Reclassify(m.inputs)
	m.all = m.s.Modules()
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

func (m model) cursorModule() string {
	if m.cursor < len(m.visible) {
		return m.visible[m.cursor].Module
	}
	return ""
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

// layout splits the screen: the why pane floats to the bottom at its content height (capped),
// leaving the rest to the details pane.
func (m model) layout() (leftW, rightW, bodyH, detailH, whyH int) {
	filterH := 0
	if m.filtering {
		filterH = 1
	}
	bodyH = max(4, m.termH-2-filterH)
	// the left list is the primary surface (you select modules there and the paths are long),
	// so favor it slightly and let it scale with the terminal rather than capping it.
	leftW = clamp(m.termW*55/100, 34, m.termW-28)
	rightW = max(24, m.termW-leftW-1)
	whyH = clamp(len(m.whyBody(rightW))+1, 2, bodyH/2)
	detailH = bodyH - whyH
	return
}

func (m model) listH() int {
	_, _, bodyH, _, _ := m.layout()
	return max(1, bodyH-1)
}

// View

func (m model) View() string {
	leftW, rightW, bodyH, detailH, whyH := m.layout()

	left := pad(m.viewList(leftW), leftW, bodyH)
	detail := renderPane("Details", m.detailBody(rightW), m.detailOffset, rightW, detailH, m.focus == focusDetail)
	why := renderPane("Why it's here  (go mod why)", m.whyBody(rightW), m.whyOffset, rightW, whyH, m.focus == focusWhy)
	right := lipgloss.JoinVertical(lipgloss.Left, detail, why)

	divider := styDim.Render(strings.TrimRight(strings.Repeat("│\n", bodyH), "\n"))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)

	parts := []string{m.viewBar()}
	if m.filtering {
		parts = append(parts, m.filter.View())
	}
	parts = append(parts, body, m.viewHelp())
	return strings.Join(parts, "\n")
}

func (m model) viewBar() string {
	w := m.whatif
	delta := w.OriginalSize - w.ProjectedSize
	saved := styDim.Render("(unchanged)")
	if delta > 0 {
		pct := float64(delta) / float64(max64(w.OriginalSize, 1)) * 100
		saved = styGood.Render(fmt.Sprintf("−%s (−%.1f%%)", humize(delta), pct))
	}
	return styBar.Render(fmt.Sprintf("binary %s → %s", humize(w.OriginalSize), styGood.Render(humize(w.ProjectedSize)))) +
		"  " + saved +
		"  " + styDim.Render(fmt.Sprintf("· %d unchecked · %d modules removed", len(m.pruned), len(w.PrunedModules))) +
		"  " + m.floorBar()
}

// floorBar renders the live go-version floor: the lowest `go` directive the owned modules can
// declare under the current selection, how far pruning has already lowered it from the baseline,
// and how many surviving deps (△) still pin it. This is the "set the most minimum go version"
// readout that updates as candidates are unchecked.
func (m model) floorBar() string {
	f := m.goFloor
	if f.Version == "" {
		return styDim.Render("· go floor: none")
	}
	pins := styWarn.Render(fmt.Sprintf("%s%d pinning", glyphFloor, len(f.Critical)))
	if b := m.baseFloor.Version; b != "" && b != f.Version { // pruning has dropped the floor
		return styDim.Render("· go ≥ ") + styDim.Render(b) + styDim.Render(" → ") +
			styGood.Render(f.Version) + "  " + pins
	}
	return styDim.Render(fmt.Sprintf("· go ≥ %s  ", f.Version)) + pins
}

func (m model) viewList(width int) string {
	scope := "Candidates"
	if m.showAll {
		scope = "All modules"
	}
	title := fmt.Sprintf("%s · %s", scope, sortLabel(m.sortMode))
	lines := []string{headerFor(title, width, m.focus == focusList)}
	h := m.listH()
	end := min(m.offset+h, len(m.visible))
	for i := m.offset; i < end; i++ {
		mod := m.visible[i]
		box := glyphNone
		if mod.Target {
			if m.pruned[mod.Module] {
				box = glyphOut
			} else {
				box = glyphIn
			}
		}
		val := mod.Size
		if m.sortMode == sortPrune && mod.Exclusive > 0 {
			val = mod.Exclusive
		}
		// the go column: the module's declared `go` minimum, △-flagged and highlighted when it
		// pins the floor.
		goPlain, goStyled := goVerCell(mod, m.floorPins[mod.Module])
		if i == m.cursor {
			plain := fmt.Sprintf("%s %s %8s  %s %s", box, classTagPlain(mod), humize(val), goPlain, truncate(mod.Module, width-23))
			lines = append(lines, styRow.Render(fit(plain, width)))
			continue
		}
		boxR := styDim.Render(box)
		if box == glyphIn {
			boxR = styGood.Render(box)
		}
		name := classStyle(mod.Class, mod.Controlled, mod.Locked, truncate(mod.Module, width-23))
		if mod.Target && m.pruned[mod.Module] {
			name = styDim.Render(stripStyle(name))
		}
		lines = append(lines, fmt.Sprintf("%s %s %8s  %s %s", boxR, classTag(mod), humize(val), goStyled, name))
	}
	return strings.Join(lines, "\n")
}

// detailBody returns the scrollable lines of the details pane (module info + what it pulls in,
// with per-dep importer branches when expanded).
func (m model) detailBody(width int) []string {
	d := m.detail
	if d.Module == "" {
		return nil
	}
	body := []string{
		classStyle(d.Class, d.Controlled, d.Locked, truncate(d.Module, width)),
		styDim.Render(detailTags(d)),
		fmt.Sprintf("size %s · own %s", humize(d.Size), humize(d.Own)),
		pruneAloneLine(d),
		styDim.Render(fmt.Sprintf("coupling %d pkg · %d imp · %d sym · imported by %d",
			d.Coupling.ImportingPackages, d.Coupling.ImportSites, d.Coupling.DistinctSymbols, d.Importers)),
		m.goDirectiveLine(d),
		"",
		styHead.Render("pulls in  ") + styDim.Render(glyphIn+" in binary  "+glyphOut+" pruned · tab here, space expands"),
	}
	for i, st := range m.dragStatus {
		label := st.Module
		if st.Module == "std" {
			label = "(standard library)"
		}
		var line string
		switch {
		case st.Freed:
			line = styDim.Render(fmt.Sprintf("%s %8s  %s", glyphOut, humize(st.Bytes), label))
		case len(st.NeededBy) == 0:
			line = fmt.Sprintf("%s %8s  %s%s", styGood.Render(glyphIn), humize(st.Bytes), label, styDim.Render("  (only via this)"))
		default:
			note := fmt.Sprintf("  (needed by %d)", len(st.NeededBy))
			line = fmt.Sprintf("%s %8s  %s%s", styGood.Render(glyphIn), humize(st.Bytes), label, styDim.Render(note))
		}
		if m.focus == focusDetail && i == m.detailCursor {
			line = styRow.Render(fit(plainDep(st, label), width))
		}
		body = append(body, line)
		if m.expanded[st.Module] {
			for j, imp := range st.NeededBy {
				conn := "├ "
				if j == len(st.NeededBy)-1 {
					conn = "└ "
				}
				body = append(body, "      "+styDim.Render(conn+truncate(imp, width-8)))
			}
		}
	}
	return body
}

// pruneAloneLine reconciles the two numbers that confuse people: a module pulls in a gross
// amount, but pruning it only frees the part that isn't also reached some other way. Saying
// both — "frees X of Y pulled in, Z held by other importers" — makes the gap explicit.
func pruneAloneLine(d bonsai.Detail) string {
	if !d.Target {
		return styDim.Render("prune-alone  — (locked / not a candidate)")
	}
	frees := styGood.Render(humize(d.Exclusive))
	if d.PullsIn > d.Exclusive {
		held := d.PullsIn - d.Exclusive
		return "prune-alone frees " + frees + styDim.Render(fmt.Sprintf(
			" of %s pulled in · %s held by others (stays)", humize(d.PullsIn), humize(held)))
	}
	return "prune-alone frees " + frees + styDim.Render(" (all it pulls in — nothing shared)")
}

// goDirectiveLine describes the highlighted module's `go` directive and its role in the floor:
// a △ pinner is why the owned modules can't go lower (and what they'd drop to if it left); your
// own modules just report theirs; everything else sits harmlessly below the floor.
func (m model) goDirectiveLine(d bonsai.Detail) string {
	if d.GoVersion == "" {
		return styDim.Render("go directive  (none declared)")
	}
	prefix := styDim.Render(fmt.Sprintf("go directive  %s  ", d.GoVersion))
	switch {
	case d.Controlled || d.Class == "main":
		return prefix + styDim.Render("(yours — set freely)")
	case m.floorPins[d.Module]:
		note := fmt.Sprintf("%s pins go floor %s", glyphFloor, m.goFloor.Version)
		if n := len(m.goFloor.Critical); n > 1 {
			note = fmt.Sprintf("%s 1 of %d pinning go floor %s", glyphFloor, n, m.goFloor.Version)
		} else if m.goFloor.NextVersion != "" {
			note = fmt.Sprintf("%s pins go floor %s (→ %s if pruned)", glyphFloor, m.goFloor.Version, m.goFloor.NextVersion)
		}
		return prefix + styWarn.Render(note)
	default:
		return prefix + styDim.Render(fmt.Sprintf("(below floor go ≥ %s)", m.goFloor.Version))
	}
}

// whyBody renders go-mod-why as a tree: your 1st-class code at the top, the target at the
// bottom, each parent importing its child (the same direction as `go mod graph`'s "A B"). The
// Session hands us the reverse importer tree (target at the root); we invert it into
// consumer-rooted import paths and draw them with tree connectors.
func (m model) whyBody(width int) []string {
	root := m.detail.Why
	if root == nil {
		return []string{styDim.Render("(an entrypoint — nothing imports it)")}
	}
	var paths [][]wnode
	collectWhyPaths(root, nil, &paths)
	trie := &whyTrie{}
	for _, p := range paths {
		trie.insert(p)
	}
	legend := styDim.Render("imports flow ↓ (your code → this module)")
	return append([]string{legend}, renderWhyTrie(trie, "", width, m.detail.Module)...)
}

type wnode struct{ mod, class string }

// collectWhyPaths walks the reverse importer tree (root = target, children = importers) and
// emits each root→leaf path reversed, so it reads consumer→…→target.
func collectWhyPaths(n *bonsai.ImportNode, acc []wnode, out *[][]wnode) {
	acc = append(acc, wnode{n.Module, n.Class})
	if len(n.Via) == 0 {
		rev := make([]wnode, len(acc))
		for i, w := range acc {
			rev[len(acc)-1-i] = w
		}
		*out = append(*out, rev)
		return
	}
	for _, child := range n.Via {
		collectWhyPaths(child, acc, out)
	}
}

// whyTrie merges the inverted import paths into a single tree (shared prefixes collapse).
type whyTrie struct {
	mod   string
	class string
	kids  []*whyTrie
}

func (t *whyTrie) insert(path []wnode) {
	cur := t
	for _, w := range path {
		var kid *whyTrie
		for _, k := range cur.kids {
			if k.mod == w.mod {
				kid = k
				break
			}
		}
		if kid == nil {
			kid = &whyTrie{mod: w.mod, class: w.class}
			cur.kids = append(cur.kids, kid)
		}
		cur = kid
	}
}

func renderWhyTrie(t *whyTrie, prefix string, width int, target string) []string {
	var out []string
	for i, kid := range t.kids {
		last := i == len(t.kids)-1
		branch, cont := "├─ ", "│  "
		if last {
			branch, cont = "└─ ", "   "
		}
		gold := kid.class == "1st" || kid.class == "main"
		name := classStyle(kid.class, gold, false, truncate(kid.mod, width-len(prefix)-8))
		tag := styDim.Render(" " + kid.class)
		if kid.mod == target {
			tag = styGood.Render(" ◂ this module")
		}
		out = append(out, prefix+styDim.Render(branch)+name+tag)
		out = append(out, renderWhyTrie(kid, prefix+cont, width, target)...)
	}
	return out
}

func (m model) viewHelp() string {
	switch m.focus {
	case focusDetail:
		return styHelp.Render("DETAIL · ↑/↓ dep · space expand importers · tab next pane · q cancel")
	case focusWhy:
		return styHelp.Render("WHY · ↑/↓ scroll · tab next pane · q cancel")
	default:
		return styHelp.Render("↑/↓ move · space prune · / filter · a all · s sort · c/l/u class · tab panes · enter apply · q quit") +
			styDim.Render("   "+glyphFloor+" pins go floor")
	}
}

// rendering helpers

// classTag is a colored one-letter class indicator: M(ain), 1st, 2nd candidate, L(ocked), 3rd.
func classTag(mod bonsai.Module) string {
	switch {
	case mod.Class == "main":
		return styGold.Render("M")
	case mod.Controlled:
		return styGold.Render("1")
	case mod.Locked:
		return styDim.Render("L")
	case mod.Target:
		return styCyan.Render("2")
	default:
		return styDim.Render("3")
	}
}

func classTagPlain(mod bonsai.Module) string {
	switch {
	case mod.Class == "main":
		return "M"
	case mod.Controlled:
		return "1"
	case mod.Locked:
		return "L"
	case mod.Target:
		return "2"
	default:
		return "3"
	}
}

// goVerCell renders a module's declared `go` minimum as a fixed-width column, returning both the
// plain (for the reverse-video cursor row) and styled forms: △-flagged and yellow when it pins
// the floor, dim otherwise, and an em-dash when the module declares no directive.
func goVerCell(mod bonsai.Module, pins bool) (plain, styled string) {
	const w = 7 // fits "△1.24.0"
	switch {
	case mod.GoVersion == "":
		plain = fmt.Sprintf("%-*s", w, "—")
		return plain, styDim.Render(plain)
	case pins:
		plain = fmt.Sprintf("%-*s", w, glyphFloor+mod.GoVersion)
		return plain, styWarn.Render(plain)
	default:
		plain = fmt.Sprintf("%-*s", w, mod.GoVersion)
		return plain, styDim.Render(plain)
	}
}

func plainDep(st bonsai.DepStatus, label string) string {
	box := glyphIn
	note := "  (only via this)"
	switch {
	case st.Freed:
		box, note = glyphOut, ""
	case len(st.NeededBy) > 0:
		note = fmt.Sprintf("  (needed by %d)", len(st.NeededBy))
	}
	return fmt.Sprintf("%s %8s  %s%s", box, humize(st.Bytes), label, note)
}

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
	case sortGo:
		return "by go min"
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

// renderPane composes a pane: a (focus-aware) title rule plus a scrolled window of body lines,
// fixed to w×h.
func renderPane(title string, body []string, offset, w, h int, focused bool) string {
	avail := max(1, h-1)
	if offset > max(0, len(body)-avail) {
		offset = max(0, len(body)-avail)
	}
	if offset < 0 {
		offset = 0
	}
	end := min(offset+avail, len(body))
	visible := []string{}
	if offset < len(body) {
		visible = body[offset:end]
	}
	more := ""
	if len(body) > avail {
		more = styDim.Render(fmt.Sprintf("  [%d–%d/%d]", offset+1, end, len(body)))
	}
	lines := append([]string{headerFor(title+more, w, focused)}, visible...)
	return pad(strings.Join(lines, "\n"), w, h)
}

func headerFor(title string, width int, focused bool) string {
	style := styHead
	if focused {
		style = styFocus
	}
	rule := ""
	if n := width - lipgloss.Width(title) - 1; n > 0 {
		rule = " " + strings.Repeat("─", n)
	}
	return style.Render(title) + styDim.Render(rule)
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

func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
