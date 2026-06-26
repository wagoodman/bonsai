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

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/internal/tui"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/humanize"
)

// key bindings and the "main" class label, recurring across the update/view switch arms.
const (
	keyEsc    = "esc"
	keyEnter  = "enter"
	keyDown   = "down"
	keyPgUp   = "pgup"
	keyPgDown = "pgdown"
	classMain = "main"
)

var (
	// shared with the lock editor (see internal/tui) so the two TUIs read as one tool.
	styHelp = tui.Help
	styDim  = tui.Dim
	styGood = tui.Good
	styCyan = tui.Cyan
	styRow  = tui.RowCursor

	styBar    = lipgloss.NewStyle().Bold(true)
	styGold   = lipgloss.NewStyle().Foreground(lipgloss.Color("220")) // 1st-class (yours)
	styWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))   // yellow: go-floor pinners
	styHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styPurple = lipgloss.NewStyle().Foreground(lipgloss.Color("135")) // selection / the module matched elsewhere

	// the panes split with a grey vertical line and a grey-background header bar (not a ─ rule):
	// both are grey so the line meets the bar at a clean junction instead of the characters crossing.
	styDivide  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))                                             // vertical rule
	styHeadBar = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Background(lipgloss.Color("237"))  // header bar
	styFocus   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("240")) // focused pane: lighter bar, bright title
)

const (
	glyphIn    = tui.GlyphOn  // in the binary
	glyphOut   = tui.GlyphOff // pruned
	glyphNone  = "·"          // present, not a candidate
	glyphFloor = "△"          // this module pins the go-version floor
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

// State is the per-target explorer state that persists across runs. It holds only the
// ephemeral what-if selection (which candidates are checked for pruning); the durable lock/
// class decisions live in .bonsai.yaml, the single source of truth, not here.
type State struct {
	Pruned []string
}

// Result is what the explorer returns on exit. Inputs is the final lock/class state the caller
// writes back to .bonsai.yaml; State is the selection cache to persist.
type Result struct {
	Confirmed bool
	Pruned    []string
	Inputs    bonsai.ClassInputs
	State     State
}

// Run launches the explorer and returns the chosen prune set plus the state to persist. version is
// shown in the status bar corner (empty to hide it).
func Run(s *bonsai.Session, initial State, version string) (Result, error) {
	m := newModel(s, initial)
	m.version = version
	res, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return Result{}, err
	}
	final := res.(model)
	out := Result{Confirmed: final.confirmed, Inputs: final.inputs, State: State{Pruned: keys(final.pruned)}}
	if final.confirmed {
		out.Pruned = final.whatif.PrunedModules
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

	binSize    uint64 // original on-disk binary size (captured once; read by viewBar)
	whatif     bonsai.WhatIf
	detail     bonsai.Detail
	dragStatus []bonsai.DepStatus

	goFloor   bonsai.GoFloor  // projected go floor under the current selection
	baseFloor bonsai.GoFloor  // go floor with nothing pruned (to show how far pruning moves it)
	floorPins map[string]bool // surviving modules pinning the current floor (Critical, for row marking)

	showHelp   bool // the ? overlay is open
	helpOffset int  // scroll position within the help overlay

	version   string // shown in the status bar corner
	confirmed bool
}

func newModel(s *bonsai.Session, initial State) model {
	ti := tui.NewFilter("fuzzy match…")

	m := model{
		s:        s,
		binSize:  s.BinarySize(),
		pruned:   map[string]bool{},
		filter:   ti,
		expanded: map[string]bool{},
		termW:    100,
		termH:    30,
	}
	for _, p := range initial.Pruned {
		m.pruned[p] = true
	}
	// the session is already classified from .bonsai.yaml + flags (see NewSession); the lock/
	// class state is not restored from the per-target cache — config is the source of truth.
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
	case keyEsc:
		m.filter.SetValue("")
		tui.StyleFilter(&m.filter)
		m.filtering = false
		m.rebuildVisible()
		m.clampCursor()
		m.recompute()
		return m, nil
	case keyEnter:
		m.filtering = false
		m.filter.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	prev := m.filter.Value()
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != prev {
		tui.StyleFilter(&m.filter)
		m.rebuildVisible()
		m.clampCursor()
		m.recompute()
	}
	return m, cmd
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" { // hard quit, even out of the help overlay
		return m, tea.Quit
	}
	if m.showHelp {
		return m.updateHelp(msg)
	}
	switch msg.String() {
	case "?":
		m.showHelp = true
		m.helpOffset = 0
		return m, nil
	case keyEsc, "q":
		return m, tea.Quit
	case keyEnter:
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
	case keyDown, "j", "ctrl+n":
		m.moveList(1)
	case keyPgUp:
		m.moveList(-m.listH())
	case keyPgDown:
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
	case keyDown, "j":
		if m.detailCursor < len(m.dragStatus)-1 {
			m.detailCursor++
		}
	case " ", "x", "right", "l", keyEnter:
		if m.detailCursor < len(m.dragStatus) {
			mod := m.dragStatus[m.detailCursor].Module
			m.expanded[mod] = !m.expanded[mod]
		}
		if msg.String() == keyEnter { // enter shouldn't quit while exploring the detail pane
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
	case keyDown, "j":
		m.whyOffset++
	case keyPgUp:
		m.whyOffset = max(0, m.whyOffset-5)
	case keyPgDown:
		m.whyOffset += 5
	}
	return m, nil
}

// updateHelp handles keys while the ? overlay is open: scroll the legend or dismiss it. Nothing
// leaks through to the explorer underneath, so esc/q close the help instead of quitting.
func (m model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, "q", "?", keyEnter:
		m.showHelp = false
	case "up", "k":
		m.helpOffset = max(0, m.helpOffset-1)
	case keyDown, "j":
		m.helpOffset = min(m.helpOffset+1, m.helpMaxOffset())
	case keyPgUp:
		m.helpOffset = max(0, m.helpOffset-5)
	case keyPgDown:
		m.helpOffset = min(m.helpOffset+5, m.helpMaxOffset())
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
	_, _, _, detailH, _ := m.layout() //nolint:dogsled // layout() returns five sizing values; only one is needed here
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

// candidateCount reports how many modules are prune targets. Exercised by the unit tests;
// the linter's unused check ignores test files (run.tests=false), so it is annotated here.
//
//nolint:unused
func (m model) candidateCount() int {
	n := 0
	for _, mod := range m.all {
		if mod.Target {
			n++
		}
	}
	return n
}

// layout splits the screen: the why pane floats to the bottom at its content height, and the
// detail pane (on top) takes the rest — so any slack falls between the two as blank in the detail
// pane, keeping go-mod-why anchored at the bottom. Unlike a fixed half split, the why pane may
// grow past half to use the space a short detail pane leaves (a long go-mod-why tree on a module
// that pulls in little), but it never shrinks the detail pane below its own need / a fair half.
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

	// each pane wants its content height plus a header row. Size the why pane to its content but
	// never let it eat into the detail pane's need (capped at a fair half when both overflow); the
	// detail pane takes the remainder, floating the why pane to the bottom.
	detailNeed := len(m.detailBody(rightW)) + 1
	whyNeed := len(m.whyBody(rightW)) + 1
	whyH = min(whyNeed, bodyH-min(detailNeed, bodyH/2))
	detailH = bodyH - whyH
	return
}

func (m model) listH() int {
	//nolint:dogsled // layout() returns five sizing values; only one is needed here
	_, _, bodyH, _, _ := m.layout()
	return max(1, bodyH-2) // title bar + column header
}

// View

func (m model) View() string {
	if m.showHelp {
		return m.viewHelpOverlay()
	}
	leftW, rightW, bodyH, detailH, whyH := m.layout()

	left := pad(m.viewList(leftW), leftW, bodyH)
	detail := renderPane("Details", m.detailBody(rightW), m.detailOffset, rightW, detailH, m.focus == focusDetail)
	why := renderPane("Why it's here  (go mod why)", m.whyBody(rightW), m.whyOffset, rightW, whyH, m.focus == focusWhy)
	right := lipgloss.JoinVertical(lipgloss.Left, detail, why)

	divider := styDivide.Render(strings.TrimRight(strings.Repeat("│\n", bodyH), "\n"))
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

	// size progression: the original binary on disk → the stripped baseline the what-if projects
	// from (OriginalSize ≈ a `-s -w` release build) → the projected size after pruning. Surface the
	// on-disk → stripped step only when stripping actually removes bytes; otherwise the input is
	// already stripped and "original" and "stripped" are the same number.
	pruned := styGood.Render(humize(w.ProjectedSize))
	head := fmt.Sprintf("stripped %s → pruned %s", humize(w.OriginalSize), pruned)
	if m.binSize > w.OriginalSize {
		head = fmt.Sprintf("original %s → stripped %s → pruned %s", humize(m.binSize), humize(w.OriginalSize), pruned)
	}
	return styBar.Render(head) +
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
	lines := []string{headerFor(title, width, m.focus == focusList), m.listCols(width)}
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

// listCols renders the column-header row, aligned to the same widths as the rows above
// (box · class · value · go · module). The value column tracks the sort: prune-value vs raw size.
func (m model) listCols(width int) string {
	valHdr := "size"
	if m.sortMode == sortPrune {
		valHdr = "prune"
	}
	cols := fmt.Sprintf("%s %s %8s  %-7s %s", " ", " ", valHdr, "go min", "module")
	return styDim.Render(fit(cols, width))
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
	if len(m.dragStatus) == 0 {
		return append(body, styDim.Render("no modules pulled in!"))
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
	case d.Controlled || d.Class == classMain:
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
		gold := kid.class == "1st" || kid.class == classMain
		label := truncate(kid.mod, width-len(prefix)-8)
		name := classStyle(kid.class, gold, false, label)
		// the selected module is shown purple here too, tying it back to the left-pane selection.
		if kid.mod == target {
			name = styPurple.Render(label)
		}
		tag := styDim.Render(" " + kid.class)
		out = append(out, prefix+styDim.Render(branch)+name+tag)
		out = append(out, renderWhyTrie(kid, prefix+cont, width, target)...)
	}
	return out
}

func (m model) viewHelp() string {
	var left string
	switch m.focus {
	case focusDetail:
		left = styHelp.Render("DETAIL · ↑/↓ dep · space expand importers · tab next pane · ? help · q cancel")
	case focusWhy:
		left = styHelp.Render("WHY · ↑/↓ scroll · tab next pane · ? help · q cancel")
	default:
		left = styHelp.Render("↑/↓ move · space prune · / filter · a all · s sort · c/l/u class · tab panes · enter apply · q quit") +
			styDim.Render("   "+glyphFloor+" pins go floor · ") + styHelp.Render("? help")
	}
	if m.version == "" {
		return left
	}
	// pin "bonsai · <version>" to the bottom-right corner; drop it if the terminal is too narrow.
	right := styDim.Render("bonsai · " + m.version)
	gap := m.termW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// viewHelpOverlay renders the ? help: the summary bar stays for orientation, a centered card
// explains the classes / glyphs / panes / keys, and a dismiss hint sits at the bottom.
func (m model) viewHelpOverlay() string {
	bodyH := max(6, m.termH-2)
	card := m.helpCard(bodyH)
	body := lipgloss.Place(m.termW, bodyH, lipgloss.Center, lipgloss.Center, card)
	hint := styHelp.Render("? or esc to close · ↑/↓ scroll")
	return strings.Join([]string{m.viewBar(), body, hint}, "\n")
}

// helpAvail is the number of legend rows that fit in a card of height maxH, reserving the
// border(2) + title + blank. helpCard and helpMaxOffset both derive geometry from it so scrolling
// and rendering agree on where the bottom is. The floor is 1 (not a fixed minimum that could make
// the card taller than maxH) so the overlay degrades to a single scrollable row on a tiny terminal
// instead of overflowing the placement region.
func helpAvail(maxH int) int {
	return clamp(maxH-4, 1, len(helpLines()))
}

// helpMaxOffset is the furthest the legend can scroll before the last row sits at the bottom, so
// updateHelp can clamp helpOffset there instead of letting it drift past the end.
func (m model) helpMaxOffset() int {
	return max(0, len(helpLines())-helpAvail(max(6, m.termH-2)))
}

// helpCard builds the bordered, scrollable legend box. The title stays pinned; only the legend
// body scrolls (helpOffset), with a [from–to/total] marker when it doesn't all fit.
func (m model) helpCard(maxH int) string {
	lines := helpLines()
	avail := helpAvail(maxH)
	off := clamp(m.helpOffset, 0, max(0, len(lines)-avail))
	end := min(off+avail, len(lines))

	title := styHead.Render("bonsai prune explorer — help")
	if len(lines) > avail {
		title += styDim.Render(fmt.Sprintf("   [%d–%d/%d]", off+1, end, len(lines)))
	}
	content := strings.Join(append([]string{title, ""}, lines[off:end]...), "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(0, 2).
		Render(content)
}

// helpLines is the legend content. It reuses the live UI's styles and glyph constants so the
// colors and symbols here match exactly what's on screen.
func helpLines() []string {
	dim := styDim.Render
	head := styHead.Render
	return []string{
		"Everything in the binary starts checked — prune by unchecking,",
		dim("and the projected binary size updates live."),
		"",
		head("The class column   M  1  2  3  L"),
		styGold.Render("M") + "  main module — your entrypoint; always yours to edit",
		styGold.Render("1") + "  1st-class — code you control (" + dim("main + --controlled") + ")",
		dim("    bonsai cuts imports OUT of these; they're never pruned"),
		styCyan.Render("2") + "  2nd-class — a dep your controlled code imports directly",
		"    " + styGood.Render("← these are the prune candidates"),
		dim("3") + "  3rd-class — reached only via other deps, not your code",
		dim("    can't cut directly; it leaves only when its importer does"),
		dim("L") + "  locked — never offered for pruning (1st-class by default)",
		"",
		head("Glyphs"),
		styGood.Render(glyphIn) + " in binary    " + glyphOut + " pruned    " + dim(glyphNone) + " not a candidate",
		styWarn.Render(glyphFloor) + " pins the go floor — the lowest go version your modules",
		dim("  can require; prune its pinners to lower it"),
		"",
		head("Details pane   (Tab to focus · Space expands a dep)"),
		"size / own   total reachable bytes vs this module's own code",
		"prune-alone  bytes freed if you prune ONLY this — its retained",
		dim("             size, not its gross; shared weight stays behind"),
		"pulls in     what leaves (" + glyphOut + ") vs survives (" + styGood.Render(glyphIn) + ", held by others)",
		"",
		head("Why it's here   (go mod why)"),
		"the import path from your code down to this module — shows",
		dim("which import to drop to make it actually leave"),
		"",
		head("Reclassify live"),
		styCyan.Render("c") + "  controlled — mark a module 1st-class (cut its imports)",
		styCyan.Render("l") + "  locked     — protect from / expose to pruning",
		styCyan.Render("u") + "  unlock     — drop one of your own modules wholesale",
		"",
		head("Keys"),
		dim("↑/↓") + " move   " + dim("space") + " prune   " + dim("/") + " filter   " + dim("a") + " all/candidates",
		dim("s") + " sort   " + dim("Tab") + " panes   " + dim(keyEnter) + " apply   " + dim("q") + " quit   " + dim("?") + " help",
	}
}

// rendering helpers

// classTag is a colored one-letter class indicator: M(ain), 1st, 2nd candidate, L(ocked), 3rd.
func classTag(mod bonsai.Module) string {
	switch {
	case mod.Class == classMain:
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
	case mod.Class == classMain:
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
	case controlled || class == "1st" || class == classMain:
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
		// plain text: it rides the header bar's grey background, so no embedded style/reset.
		more = fmt.Sprintf("  [%d–%d/%d]", offset+1, end, len(body))
	}
	lines := append([]string{headerFor(title+more, w, focused)}, visible...)
	return pad(strings.Join(lines, "\n"), w, h)
}

// headerFor renders a pane title as a filled grey bar across the row (not a ─ rule), so it meets
// the grey vertical divider at a clean junction. The bar fills the full width via the background.
func headerFor(title string, width int, focused bool) string {
	style := styHeadBar
	if focused {
		style = styFocus
	}
	s := " " + title
	// keep it to one row; pad() truncates too, but Width() would wrap an over-long title first.
	if r := []rune(s); width > 0 && len(r) > width {
		s = string(r[:width])
	}
	return style.Width(width).Render(s)
}

// the layout helpers live in internal/tui (shared with the lock editor); these locals keep the
// dense view code terse.
func pad(s string, w, h int) string       { return tui.Pad(s, w, h) }
func truncate(s string, width int) string { return tui.Truncate(s, width) }
func fit(s string, w int) string          { return tui.Fit(s, w) }

// humize is a local shorthand for the shared byte formatter, keeping the dense view code readable.
func humize(b uint64) string { return humanize.Bytes(b) }

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

func clamp(v, lo, hi int) int { return tui.Clamp(v, lo, hi) }

func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
