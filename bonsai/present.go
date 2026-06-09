package bonsai

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/muesli/termenv"
)

// WriteJSON renders the analysis as indented JSON.
func WriteJSON(w io.Writer, an *Analysis) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(an)
}

// WriteTable renders the human-readable report with aligned tables, using ANSI color when
// color is true (a TTY destination). Shows up to top rows in each ranked table.
//
// The report is rendered into a buffer and printed later, so lipgloss's own stdout-based
// color detection doesn't apply; we force the renderer's profile to match the caller's
// decision (caller already verified the destination is a color-capable TTY).
func WriteTable(w io.Writer, an *Analysis, top int, color bool) error {
	if color {
		lipgloss.SetColorProfile(termenv.ANSI)
	}
	return (&report{w: w, top: top, pal: palette{on: color}}).write(an)
}

// WriteMarkdown renders the report with markdown headings and fenced/pipe tables (no color).
func WriteMarkdown(w io.Writer, an *Analysis, top int) error {
	return (&report{w: w, top: top, md: true}).write(an)
}

// palette gates ANSI styling: when off, every helper returns its input unchanged so the
// same rendering code produces plain text for pipes, markdown, and NO_COLOR.
type palette struct{ on bool }

var (
	styTitle  = lipgloss.NewStyle().Bold(true)
	styHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")) // cyan
	styDim    = lipgloss.NewStyle().Faint(true)
	styGood   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	styWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	styStrong = lipgloss.NewStyle().Bold(true)
)

func (p palette) render(s lipgloss.Style, txt string) string {
	if !p.on {
		return txt
	}
	return s.Render(txt)
}

func (p palette) title(s string) string  { return p.render(styTitle, s) }
func (p palette) head(s string) string   { return p.render(styHead, s) }
func (p palette) dim(s string) string    { return p.render(styDim, s) }
func (p palette) good(s string) string   { return p.render(styGood, s) }
func (p palette) warn(s string) string   { return p.render(styWarn, s) }
func (p palette) strong(s string) string { return p.render(styStrong, s) }

// report carries the rendering mode and writer through the section helpers.
type report struct {
	w   io.Writer
	top int
	md  bool
	pal palette
}

func (r *report) write(an *Analysis) error {
	r.summary(an)
	r.breakdown(an)
	r.sections(an)
	r.largestModules(an)
	r.pruneCandidates(an)
	r.prunePlan(an)
	r.goFloor(an)
	r.blame(an)
	return nil
}

// goFloor reports the lowest `go` directive the owned (main + 1st-class) modules could declare,
// the headroom available to reclaim right now (your declared version vs the dep-imposed floor),
// and the dependencies pinning that floor — the modules to prune to push it lower.
func (r *report) goFloor(an *Analysis) {
	f := an.GoFloor
	if f.Version == "" {
		r.heading("Go version floor", "no dependency declares a `go` directive — nothing constrains your minimum")
		return
	}

	r.heading("Go version floor",
		"the lowest `go` directive your own modules can declare; deps pin it — prune them to push it lower")

	// headroom you can reclaim now (drop your `go` line to the floor) vs what pruning would buy.
	if f.OwnedMax != "" && cmpGo(f.OwnedMax, f.Version) > 0 {
		r.floorNote(r.pal.good(fmt.Sprintf(
			"you declare go %s but deps only require go %s — you can drop to %s now",
			f.OwnedMax, f.Version, f.Version)))
	} else {
		r.floorNote(r.pal.dim(fmt.Sprintf("deps require go ≥ %s", f.Version)))
	}
	if f.NextVersion != "" {
		r.floorNote(r.pal.dim(fmt.Sprintf(
			"prune the %d module(s) below to reach go %s", len(f.Critical), f.NextVersion)))
	}
	fmt.Fprintln(r.w)

	rows := make([][]string, 0, len(f.Critical))
	for _, mod := range f.Critical {
		rows = append(rows, []string{r.pal.warn("go " + f.Version), mod})
	}
	r.table([]string{"REQUIRES", "MODULE (pins the floor)"}, rows, nil)
}

// floorNote prints one of the go-floor headline lines, italicized in markdown and indented
// elsewhere (matching pruneHeadline). The text is pre-styled for the plain/color path; markdown
// strips it back to plain.
func (r *report) floorNote(line string) {
	if r.md {
		fmt.Fprintf(r.w, "_%s_\n\n", stripANSI(line))
		return
	}
	fmt.Fprintf(r.w, "  %s\n", line)
}

func (r *report) heading(title, subtitle string) {
	if r.md {
		fmt.Fprintf(r.w, "## %s\n\n", title)
		if subtitle != "" {
			fmt.Fprintf(r.w, "_%s_\n\n", subtitle)
		}
		return
	}
	fmt.Fprintf(r.w, "%s\n", r.pal.title(title))
	if subtitle != "" {
		fmt.Fprintf(r.w, "%s\n", r.pal.dim(subtitle))
	}
}

func (r *report) summary(an *Analysis) {
	if r.md {
		fmt.Fprintf(r.w, "# binary size analysis\n\n")
	} else {
		fmt.Fprintf(r.w, "%s\n\n", r.pal.title("binary size analysis"))
	}

	if !an.Stripped && an.BinarySize > an.AccountedSize {
		removed := an.BinarySize - an.AccountedSize
		fmt.Fprintf(r.w, "  analyzed (unstripped)    %s\n", r.pal.strong(humize(an.BinarySize)))
		fmt.Fprintf(r.w, "  stripped (≈ release)     %s   %s\n",
			r.pal.strong(humize(an.AccountedSize)),
			r.pal.dim(fmt.Sprintf("debug + symbols −%s removed by `-s -w`", humize(removed))))
	} else {
		fmt.Fprintf(r.w, "  binary size              %s\n", r.pal.strong(humize(an.AccountedSize)))
	}
	if an.Stripped {
		fmt.Fprintf(r.w, "\n  %s\n", r.pal.warn("binary is stripped — only executable code could be attributed (no data/pclntab)"))
	}
	fmt.Fprintln(r.w)
}

func (r *report) breakdown(an *Analysis) {
	denom := an.AccountedSize
	var thirdParty uint64
	for _, m := range an.Modules {
		if m.Module != an.MainModule {
			thirdParty += m.Size
		}
	}

	fmt.Fprintf(r.w, "%s\n", r.pal.head("by content"))
	fmt.Fprintf(r.w, "  executable code    %9s  %s\n", humize(an.CodeSize), r.pal.dim(pctStr(an.CodeSize, denom)))
	fmt.Fprintf(r.w, "  named data         %9s  %s\n", humize(an.DataSize), r.pal.dim(pctStr(an.DataSize, denom)))
	fmt.Fprintf(r.w, "  gopclntab metadata %9s  %s\n", humize(an.PclntabSize), r.pal.dim(pctStr(an.PclntabSize, denom)))
	fmt.Fprintln(r.w)

	fmt.Fprintf(r.w, "%s  %s\n", r.pal.head("by owner"), r.pal.dim("(code + data + pclntab share)"))
	fmt.Fprintf(r.w, "  main module (%s)  %9s\n", shortModule(an.MainModule), humize(an.MainSize))
	fmt.Fprintf(r.w, "  third-party        %9s\n", humize(thirdParty))
	fmt.Fprintf(r.w, "  std library        %9s\n", humize(an.StdSize))
	fmt.Fprintf(r.w, "  generated/anon     %9s  %s\n", humize(an.GeneratedSize), r.pal.dim("(pooled constants, type metadata)"))
	fmt.Fprintln(r.w)
}

func (r *report) sections(an *Analysis) {
	secs := append([]SectionInfo(nil), an.Sections...)
	sort.Slice(secs, func(i, j int) bool { return secs[i].Size > secs[j].Size })

	r.heading("Sections (file-backed)", "")
	rows := [][]string{}
	for i, s := range secs {
		if i >= 8 || s.Size == 0 {
			break
		}
		rows = append(rows, []string{humize(s.Size), pctStr(s.Size, an.AccountedSize), s.Name})
	}
	r.table([]string{"SIZE", "%BIN", "SECTION"}, rows, nil)
}

// largestModules ranks third-party modules by size. With import-why trees present (--why) it
// renders each entry with its "← imported by" trace beneath; otherwise it keeps the compact
// table.
func (r *report) largestModules(an *Analysis) {
	withWhy := false
	for _, m := range an.Modules {
		if m.Why != nil {
			withWhy = true
			break
		}
	}
	if !withWhy {
		r.largestModulesTable(an)
		return
	}

	r.heading("Largest modules by size",
		"class is relative to code you control; ← traces who imports it back to your 1st-class code")
	if !r.md {
		fmt.Fprintf(r.w, "  %s\n", r.pal.head(fmt.Sprintf("%9s  %5s  %-5s  %-8s  %s", "SIZE", "%BIN", "CLASS", "KIND", "MODULE")))
	}
	shown := 0
	for _, m := range an.Modules {
		if m.Module == an.MainModule {
			continue
		}
		if m.Ignored && an.HideIgnored {
			continue
		}
		r.moduleRow(m, an.AccountedSize)
		whyPrefix := "               " // plain: indent under the row
		if r.md {
			whyPrefix = "  " // markdown: nest one list level under the module item
		}
		r.renderWhy(m.Why, whyPrefix)
		if shown++; shown >= r.top {
			break
		}
	}
	fmt.Fprintln(r.w)
}

// largestModulesTable is the compact ranked table shown when import-why traces are off.
func (r *report) largestModulesTable(an *Analysis) {
	r.heading("Largest modules by size",
		"class is relative to code you control: 1st = yours, 2nd = direct dep of yours, 3rd = transitive (--why explains each)")
	rows := [][]string{}
	var dim []bool
	shown := 0
	for _, m := range an.Modules {
		if m.Module == an.MainModule {
			continue
		}
		if m.Ignored && an.HideIgnored {
			continue
		}
		rows = append(rows, []string{humize(m.Size), pctStr(m.Size, an.AccountedSize), m.Class, kindLabel(m), m.Module})
		dim = append(dim, m.Ignored)
		if shown++; shown >= r.top {
			break
		}
	}
	r.table([]string{"SIZE", "%BIN", "CLASS", "KIND", "MODULE"}, rows, dim)
}

// moduleRow prints one largest-modules entry as a fixed-width line (so its why tree can hang
// beneath it), dimming locked modules.
func (r *report) moduleRow(m ModuleSize, denom uint64) {
	if r.md {
		fmt.Fprintf(r.w, "- **%s** — %s · %s · class %s · %s\n",
			m.Module, humize(m.Size), pctStr(m.Size, denom), m.Class, kindLabel(m))
		return
	}
	row := fmt.Sprintf("%9s  %5s  %-5s  %-8s  %s", humize(m.Size), pctStr(m.Size, denom), m.Class, kindLabel(m), m.Module)
	if m.Ignored {
		row = r.pal.dim(row)
	}
	fmt.Fprintf(r.w, "  %s\n", row)
}

func (r *report) pruneCandidates(an *Analysis) {
	prunable := make([]ModuleSize, 0, len(an.Modules))
	for _, m := range an.Modules {
		if m.Prune != nil {
			prunable = append(prunable, m)
		}
	}
	sort.Slice(prunable, func(i, j int) bool { return prunable[i].Prune.FreedBytes > prunable[j].Prune.FreedBytes })

	r.heading("Prune candidates",
		"EXCL = freed by pruning this alone; POT = freeable weight in its subtree; GET% = EXCL/POT (the rest is shared with other deps)")
	if len(prunable) > 0 {
		r.pruneHeadline(prunable[0])
	}
	rows := [][]string{}
	for i, m := range prunable {
		if i >= r.top {
			break
		}
		p := m.Prune
		c := coup(m)
		rows = append(rows, []string{
			r.pal.good(humize(p.FreedBytes)), humize(p.PotentialBytes), getPct(p), itoa(len(p.FreedModules)),
			itoa(c.ImportingPackages), itoa(c.ImportSites), itoa(c.DistinctSymbols), m.Class, m.Module,
		})
	}
	r.table([]string{"EXCL", "POT", "GET%", "ORPHANS", "IMP-PKGS", "IMP-SITES", "SYMS", "CLASS", "MODULE"}, rows, nil)
}

// pruneHeadline renders the one-line "biggest realistic win" sentence for the top prune
// candidate, naming the shared weight that pruning it would NOT free and who holds it.
func (r *report) pruneHeadline(m ModuleSize) {
	p := m.Prune
	line := fmt.Sprintf("best single win: prune %s → %s now", m.Module, humize(p.FreedBytes))
	if p.PotentialBytes > p.FreedBytes {
		line += fmt.Sprintf(", %s of the %s freeable in its subtree", getPct(p), humize(p.PotentialBytes))
	}
	if p.SharedBytes > 0 {
		extra := ""
		if len(p.SharedWith) > 0 && len(p.SharedWith[0].AlsoVia) > 0 {
			extra = " — co-prune " + joinModules(p.SharedWith[0].AlsoVia, 3) + " to free it"
		}
		line += fmt.Sprintf(" (%s shared%s)", humize(p.SharedBytes), extra)
	}
	if r.md {
		fmt.Fprintf(r.w, "_%s_\n\n", line)
		return
	}
	fmt.Fprintf(r.w, "  %s\n\n", r.pal.good(line))
}

func (r *report) prunePlan(an *Analysis) {
	if len(an.Plan) == 0 {
		return
	}
	r.heading("Prune plan (greedy order)",
		"breakdown = the dep's own code vs the deps it drags out; an item with no note is freed by this prune alone, "+
			"\"also prune X\" means X must go too")
	for i, s := range an.Plan {
		if i >= r.top {
			break
		}
		r.planStep(i+1, s)
	}
}

// planStep renders one plan step with its own-vs-dragged-in breakdown: it answers whether a
// prune's payoff is the module's own code or the dependencies it pulls out of the build.
func (r *report) planStep(n int, s PrunePlanStep) {
	dragged := s.Marginal - s.OwnBytes

	if r.md {
		fmt.Fprintf(r.w, "%d. **%s**%s — +%s freed (cumulative %s)\n",
			n, s.Module, importerNote(s.Importers), humize(s.Marginal), humize(s.Cumulative))
		fmt.Fprintf(r.w, "    - own code: %s (%s)\n", humize(s.OwnBytes), pctStr(s.OwnBytes, s.Marginal))
		fmt.Fprintf(r.w, "    - drags out: %s (%s)\n", humize(dragged), pctStr(dragged, s.Marginal))
		r.planDeps(s.Freed, "        - ")
		fmt.Fprintln(r.w)
		return
	}

	fmt.Fprintf(r.w, "  %s  %s  %s  %s%s\n",
		r.pal.strong(fmt.Sprintf("%d.", n)),
		r.pal.good(fmt.Sprintf("+%s", humize(s.Marginal))),
		r.pal.dim(fmt.Sprintf("(cumulative %s)", humize(s.Cumulative))),
		r.pal.strong(s.Module),
		r.pal.dim(importerNote(s.Importers)))
	fmt.Fprintf(r.w, "       own code   %9s  %s\n", humize(s.OwnBytes), r.pal.dim(pctStr(s.OwnBytes, s.Marginal)))
	fmt.Fprintf(r.w, "       drags out  %9s  %s\n", humize(dragged), r.pal.dim(pctStr(dragged, s.Marginal)))
	r.planDeps(s.Freed, "         ")
	fmt.Fprintln(r.w)
}

// planDeps lists the orphaned dependency modules under a step. It shows every one by default,
// honoring the same --top limit as the rest of the report (default 40) only as a safety valve
// against a pathologically wide step; the overflow collapses into a "+N more" line.
func (r *report) planDeps(freed []FreedModule, prefix string) {
	line := func(b uint64, label string) {
		if r.md {
			fmt.Fprintf(r.w, "%s%s — %s\n", prefix, label, humize(b))
			return
		}
		fmt.Fprintf(r.w, "%s%9s  %s\n", prefix, humize(b), label)
	}
	for i, f := range freed {
		if i >= r.top {
			var rest uint64
			for _, g := range freed[r.top:] {
				rest += g.Bytes
			}
			line(rest, r.pal.dim(fmt.Sprintf("+%d more", len(freed)-r.top)))
			return
		}
		line(f.Bytes, r.depLabel(f))
		// trace who imports this dragged-out dep back to your 1st-class code.
		whyPrefix := prefix + "            " // plain: clear the byte column
		if r.md {
			whyPrefix = strings.TrimSuffix(prefix, "- ") + "  " // md: nest under the dep item
		}
		r.renderWhy(f.Why, whyPrefix)
	}
}

// renderWhy prints a module's import-why tree as indented "← imported by (class)" branches,
// tracing back to the 1st-class code that pulled it in. prefix is the indent for this level.
func (r *report) renderWhy(node *ImportNode, prefix string) {
	if node == nil {
		return
	}
	for _, child := range node.Via {
		if r.md {
			fmt.Fprintf(r.w, "%s- ← %s (%s)\n", prefix, child.Module, child.Class)
		} else {
			fmt.Fprintf(r.w, "%s%s\n", prefix, r.pal.dim(fmt.Sprintf("← %s (%s)", child.Module, child.Class)))
		}
		r.renderWhy(child, prefix+"  ")
	}
	if node.More > 0 {
		marker := fmt.Sprintf("← +%d more", node.More)
		if r.md {
			fmt.Fprintf(r.w, "%s- %s\n", prefix, marker)
		} else {
			fmt.Fprintf(r.w, "%s%s\n", prefix, r.pal.dim(marker))
		}
	}
}

// depLabel renders a freed unit's name: a "(std)" tag for standard-library packages (so e.g.
// pruning x/tools reads as the go/types toolchain, not a mystery "stdlib"), plus a co-prune
// note when the item only leaves once other targets are dropped too. No note means this prune
// alone frees it — answering "do I have to drop anything else to get rid of this?".
func (r *report) depLabel(f FreedModule) string {
	label := f.Module
	if f.Std {
		label += " (std)"
	}
	if len(f.CoPrune) > 0 {
		label += "  " + r.pal.warn(fmt.Sprintf("(also prune %s)", joinModules(f.CoPrune, 3)))
	}
	return label
}

// importerNote formats a module's fan-in for inline annotation, or "" when there's nothing
// useful to say (no other module imports it).
func importerNote(importers int) string {
	switch {
	case importers <= 0:
		return ""
	case importers == 1:
		return "  (imported by 1 module)"
	default:
		return fmt.Sprintf("  (imported by %d modules)", importers)
	}
}

func (r *report) blame(an *Analysis) {
	if len(an.Blame) == 0 {
		return
	}
	how := "sampled"
	if an.Blame[0].Exact {
		how = "exact"
	}
	r.heading("Fair-blame (Shapley)",
		fmt.Sprintf("each target's fair share of the total prunable weight, %s — shared deps are split across the targets that hold them", how))
	rows := [][]string{}
	for i, b := range an.Blame {
		if i >= r.top {
			break
		}
		rows = append(rows, []string{humize(b.Blame), b.Module})
	}
	r.table([]string{"BLAME", "MODULE"}, rows, nil)
}

// table renders a titled table. In markdown mode it emits a pipe table; otherwise a
// color-aware aligned table (lipgloss measures width ignoring ANSI, so styled cells stay
// aligned). dim[i], when set, faints row i (used for ignored modules). goodCol, when >= 0,
// is unused here but reserved for column-specific emphasis.
func (r *report) table(headers []string, rows [][]string, dim []bool) {
	if len(rows) == 0 {
		fmt.Fprintf(r.w, "  %s\n\n", r.pal.dim("(none)"))
		return
	}
	if r.md {
		r.markdownTable(headers, rows)
		return
	}

	t := table.New().
		Border(lipgloss.Border{}).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).BorderRow(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().PaddingRight(2)
			switch {
			case !r.pal.on:
				return base
			case row == table.HeaderRow:
				return base.Inherit(styHead)
			case row >= 0 && row < len(dim) && dim[row]:
				return base.Inherit(styDim)
			default:
				return base
			}
		})
	fmt.Fprintln(r.w, indent(trimTrailingSpace(t.String()), "  "))
	fmt.Fprintln(r.w)
}

func (r *report) markdownTable(headers []string, rows [][]string) {
	fmt.Fprintf(r.w, "| %s |\n", strings.Join(headers, " | "))
	seps := make([]string, len(headers))
	for i := range seps {
		seps[i] = "---"
	}
	fmt.Fprintf(r.w, "| %s |\n", strings.Join(seps, " | "))
	for _, row := range rows {
		// markdown cells must not carry ANSI; rows here are built plain except colorized
		// numbers, so strip any escapes defensively.
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = stripANSI(c)
		}
		fmt.Fprintf(r.w, "| %s |\n", strings.Join(cells, " | "))
	}
	fmt.Fprintln(r.w)
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

func coup(m ModuleSize) *Coupling {
	if m.Coupling != nil {
		return m.Coupling
	}
	return &Coupling{}
}

// kindLabel describes a module's relationship to the build: locked deps (never pruned) win
// over the direct/indirect distinction since that is what gates a prune suggestion.
func kindLabel(m ModuleSize) string {
	switch {
	case m.Ignored:
		return "locked"
	case m.Direct:
		return "direct"
	default:
		return "indirect"
	}
}

// getPct renders the share of a target's subtree that pruning it actually frees (EXCL/POT).
func getPct(p *PruneResult) string {
	if p.PotentialBytes == 0 {
		return "-"
	}
	return pctStr(p.FreedBytes, p.PotentialBytes)
}

// joinModules renders up to n full module paths, collapsing the overflow into "+k more".
// Full paths (not last-segment short names) because these lists are actionable — a co-prune
// of "v2" or "generic" is meaningless, github.com/aws/aws-sdk-go-v2 is not.
func joinModules(modules []string, n int) string {
	if len(modules) <= n {
		return strings.Join(modules, ", ")
	}
	return strings.Join(modules[:n], ", ") + fmt.Sprintf(" +%d more", len(modules)-n)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func pct(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

func pctStr(part, whole uint64) string { return fmt.Sprintf("%.1f%%", pct(part, whole)) }

// trimTrailingSpace strips trailing spaces from each line (lipgloss pads the final column).
func trimTrailingSpace(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.Join(lines, "\n")
}

// indent prefixes every non-empty line of s with pad.
func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n")
}

// stripANSI removes ANSI SGR escape sequences so styled cells can be embedded in markdown.
func stripANSI(s string) string {
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
