// Package report renders a completed bonsai analysis as human- or machine-readable output
// (aligned color tables, markdown, or JSON). It depends only on the engine's result types,
// keeping all terminal/styling concerns out of the analysis core.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/muesli/termenv"

	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/humanize"
	"github.com/wagoodman/bonsai/internal/style"
)

// colModule is the recurring "module" column header shared across the report's tables.
const colModule = "MODULE"

// WriteJSON renders v as indented JSON — the canonical, complete data for whichever report
// subject (size, prune, go-version) produced it. The table/markdown views curate; JSON does not.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// setColor pins lipgloss's renderer profile when color is requested. Reports are rendered into
// a buffer and printed later, so lipgloss's own stdout-based detection doesn't apply; the caller
// already verified the destination is a color-capable TTY. Match the terminal's real capability
// (truecolor / 256 / 16) so adaptive 256-color tokens render true — gold stays gold instead of
// flattening to the nearest of 16 — but never drop below ANSI, so a requested-color report always
// emits color even on a terminal termenv can't fingerprint.
func setColor(color bool) {
	if !color {
		return
	}
	profile := termenv.ColorProfile()
	if profile == termenv.Ascii {
		profile = termenv.ANSI
	}
	lipgloss.SetColorProfile(profile)
}

// WriteSizeTable renders the binary anatomy (size by content and by owner, largest modules) as
// aligned color tables. The section layout is shown only when sections is true.
func WriteSizeTable(w io.Writer, rep *bonsai.SizeReport, top int, sections, color bool) error {
	setColor(color)
	return (&report{w: w, top: top, sections: sections, pal: palette{on: color}}).writeSize(rep)
}

// WriteSizeMarkdown renders the anatomy with markdown headings and pipe tables (no color).
func WriteSizeMarkdown(w io.Writer, rep *bonsai.SizeReport, top int, sections bool) error {
	return (&report{w: w, top: top, sections: sections, md: true}).writeSize(rep)
}

// WritePruneTable renders the prune subject (candidates, greedy plan, optional fair-blame) as
// aligned color tables.
func WritePruneTable(w io.Writer, rep *bonsai.PruneReport, top int, color bool) error {
	setColor(color)
	return (&report{w: w, top: top, pal: palette{on: color}}).writePrune(rep)
}

// WritePruneMarkdown renders the prune subject with markdown headings and pipe tables.
func WritePruneMarkdown(w io.Writer, rep *bonsai.PruneReport, top int) error {
	return (&report{w: w, top: top, md: true}).writePrune(rep)
}

// WriteGoFloorTable renders the go-version floor subject.
func WriteGoFloorTable(w io.Writer, f bonsai.GoFloor, color bool) error {
	setColor(color)
	return (&report{w: w, pal: palette{on: color}}).writeGoFloor(f)
}

// WriteGoFloorMarkdown renders the go-version floor with markdown headings.
func WriteGoFloorMarkdown(w io.Writer, f bonsai.GoFloor) error {
	return (&report{w: w, md: true}).writeGoFloor(f)
}

// CheckReport is the result of evaluating the budget: the measured values plus any violations.
// It is assembled by the check command from already-exported engine results, not by the engine.
type CheckReport struct {
	Pass            bool        `json:"pass"`            // true if no fail-action violations
	BinarySize      uint64      `json:"binarySize"`      // size gated this run (see BinarySizeLabel for which metric)
	BinarySizeLabel string      `json:"binarySizeLabel"` // "stripped binary" (accounted) or "on-disk binary" (--binary)
	GoFloor         string      `json:"goFloor"`         // dep-imposed floor measured this run
	Configured      bool        `json:"configured"`      // false when no check: block is set
	Violations      []Violation `json:"violations,omitempty"`
}

// Violation is a single budget breach: which rule, how bad, and the limit-vs-actual numbers in
// human form for direct display.
type Violation struct {
	Rule    string `json:"rule"`             // "max-binary-size" | "max-go-version" | "deny" | "max-module-size"
	Action  string `json:"action"`           // "fail" | "warn"
	Module  string `json:"module,omitempty"` // set for deny / max-module-size
	Limit   string `json:"limit"`            // human form, e.g. "25MB" or "1.23"
	Actual  string `json:"actual"`           // human form, e.g. "27MB" or "1.24"
	Message string `json:"message"`          // one-line human explanation
}

// WriteCheckTable renders the budget evaluation: a pass/fail summary line and a table of any
// violations.
func WriteCheckTable(w io.Writer, rep *CheckReport, color bool) error {
	setColor(color)
	return (&report{w: w, pal: palette{on: color}}).writeCheck(rep)
}

// WriteCheckMarkdown renders the budget evaluation with markdown headings and a pipe table.
func WriteCheckMarkdown(w io.Writer, rep *CheckReport) error {
	return (&report{w: w, md: true}).writeCheck(rep)
}

// writeCheck renders the budget evaluation: a summary line then the violations table.
func (r *report) writeCheck(rep *CheckReport) error {
	if !rep.Configured {
		r.heading("Budget check", "no check: block configured — nothing to enforce")
		return nil
	}

	r.heading("Budget check", fmt.Sprintf("%s %s · go floor %s", rep.BinarySizeLabel, humize(rep.BinarySize), goFloorLabel(rep.GoFloor)))
	if rep.Pass {
		r.floorNote(r.pal.good("PASS — within budget"))
	} else {
		fails := 0
		for _, v := range rep.Violations {
			if v.Action == "fail" {
				fails++
			}
		}
		r.floorNote(r.pal.warn(fmt.Sprintf("FAIL — %d violation(s)", fails)))
	}
	fmt.Fprintln(r.w)

	rows := make([][]string, 0, len(rep.Violations))
	for _, v := range rep.Violations {
		act := r.pal.warn(v.Action)
		if v.Action == "warn" {
			act = r.pal.dim(v.Action)
		}
		rows = append(rows, []string{act, v.Rule, v.Module, v.Limit, v.Actual})
	}
	r.table([]string{"ACTION", "RULE", colModule, "LIMIT", "ACTUAL"}, rows, nil)
	return nil
}

// goFloorLabel renders a measured floor for the summary line, naming the empty case so a build
// that imposes no floor doesn't read as a blank.
func goFloorLabel(v string) string {
	if v == "" {
		return "(none)"
	}
	return "go " + v
}

// WriteInspectTable renders the single-module drill-down: entry-package weights, import sites,
// drag-out, and the go-version floor delta.
func WriteInspectTable(w io.Writer, rep *bonsai.InspectReport, color bool) error {
	setColor(color)
	return (&report{w: w, pal: palette{on: color}}).writeInspect(rep)
}

// WriteInspectMarkdown renders the single-module drill-down with markdown headings.
func WriteInspectMarkdown(w io.Writer, rep *bonsai.InspectReport) error {
	return (&report{w: w, md: true}).writeInspect(rep)
}

// palette gates ANSI styling: when off, every helper returns its input unchanged so the
// same rendering code produces plain text for pipes, markdown, and NO_COLOR.
type palette struct{ on bool }

// these mirror the interactive explorer's styles by sourcing the same semantic tokens from
// internal/style — the report and the TUI can't drift because there's one definition of each.
var (
	styTitle  = style.Title
	styHead   = style.Heading      // section + table-column headers: bold, no hue
	styDim    = style.Subtle       // percentages, notes, locked rows, secondary text
	styGood   = style.WinStyle     // freed / saved / reclaimable weight
	styWarn   = style.CautionStyle // warnings, survivors, go-floor constraints
	styGold   = style.YoursStyle   // 1st-class / main (the code you control)
	styStrong = style.Strong       // emphasized sizes and module names
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
func (p palette) gold(s string) string   { return p.render(styGold, s) }
func (p palette) strong(s string) string { return p.render(styStrong, s) }

// report carries the rendering mode and writer through the section helpers.
type report struct {
	w        io.Writer
	top      int
	md       bool
	sections bool // anatomy: render the section-layout block (off by default; --sections)
	pal      palette
}

// writeSize renders the binary anatomy: how big it is and what occupies the space.
func (r *report) writeSize(rep *bonsai.SizeReport) error {
	r.summary(rep)
	r.breakdown(rep)
	if r.sections {
		r.sectionsBlock(rep)
	}
	r.largestModules(rep)
	r.footer()
	return nil
}

// writePrune renders the prune subject: candidates, the greedy plan, and optional fair-blame.
func (r *report) writePrune(rep *bonsai.PruneReport) error {
	r.pruneCandidates(rep)
	r.prunePlan(rep)
	return nil
}

// writeGoFloor renders the go-version floor subject.
func (r *report) writeGoFloor(f bonsai.GoFloor) error {
	r.goFloor(f)
	return nil
}

// writeInspect renders the single-module drill-down: the headline (size, exclusive/potential,
// go-floor delta), the entry-package weights (rewrite scope), the import sites (edit locations),
// the drag-out (what leaves vs survives), and the why trace.
func (r *report) writeInspect(rep *bonsai.InspectReport) error {
	r.inspectSummary(rep)
	r.inspectEntryPackages(rep)
	r.inspectSites(rep)
	r.inspectDragOut(rep)
	if rep.Why != nil {
		r.heading("Why it's in the build", "the \"← imported by\" trace back to your 1st-class code")
		r.renderWhy(rep.Why, "  ")
		fmt.Fprintln(r.w)
	}
	return nil
}

func (r *report) inspectSummary(rep *bonsai.InspectReport) {
	kind := kindLabel(bonsai.ModuleSize{Module: rep.Module, Direct: true, Locked: rep.Locked})
	r.heading(fmt.Sprintf("inspect %s", rep.Module),
		fmt.Sprintf("class %s · %s", rep.Class, kind))

	if !rep.Target {
		r.floorNote(r.pal.warn("not a prune candidate (locked or 1st-class) — shown for context only"))
	}
	fmt.Fprintf(r.w, "  freed by pruning this alone   %s\n", r.pal.good(humize(rep.FreedBytes)))
	if rep.PotentialBytes > rep.FreedBytes {
		fmt.Fprintf(r.w, "  potential (whole subtree)     %s   %s\n",
			humize(rep.PotentialBytes),
			r.pal.dim("if co-holders are pruned too"))
	}
	r.inspectFloorLine(rep.FloorDelta)
	fmt.Fprintln(r.w)
}

func (r *report) inspectFloorLine(d bonsai.FloorDelta) {
	switch {
	case d.Before == "":
		return // no dependency imposes a floor
	case d.MovesFloor:
		fmt.Fprintf(r.w, "  go-version floor              %s\n",
			r.pal.good(fmt.Sprintf("go %s → %s  (pruning this lowers it)", d.Before, d.After)))
	default:
		fmt.Fprintf(r.w, "  go-version floor              %s\n",
			r.pal.dim(fmt.Sprintf("go %s  (pruning this doesn't lower it)", d.Before)))
	}
}

// inspectEntryPackages renders the per-entry-package retained weight — the rewrite-scope map:
// which directly-imported packages of the module account for how many of the freeable bytes.
func (r *report) inspectEntryPackages(rep *bonsai.InspectReport) {
	if len(rep.EntryPackages) == 0 {
		return
	}
	r.heading("Entry packages (rewrite scope)",
		"each directly-imported package and the bytes that leave if you stop importing it")
	rows := [][]string{}
	for i, e := range rep.EntryPackages {
		if i >= 40 {
			break
		}
		rows = append(rows, []string{humize(e.RetainedBytes), e.ImportPath, joinModules(e.ImportedByPackages)})
	}
	r.table([]string{"RETAINED", "IMPORT PATH", "IMPORTED BY (1st-class)"}, rows, nil)
}

// inspectSites renders the concrete import statements to edit to sever the dependency.
func (r *report) inspectSites(rep *bonsai.InspectReport) {
	if len(rep.Sites) == 0 {
		return
	}
	r.heading("Import sites (edit here to cut)",
		"the first-party import statements that reference this module")
	rows := [][]string{}
	for i, s := range rep.Sites {
		if i >= 40 {
			rows = append(rows, []string{r.pal.dim(fmt.Sprintf("+%d more", len(rep.Sites)-40)), "", ""})
			break
		}
		rows = append(rows, []string{fmt.Sprintf("%s:%d", s.File, s.Line), s.FromPackage, s.ImportPath})
	}
	r.table([]string{"FILE:LINE", "FROM PACKAGE", "IMPORTS"}, rows, nil)
}

// inspectDragOut renders what leaves vs survives if the module is pruned, naming who holds the
// survivors — the consequence map.
func (r *report) inspectDragOut(rep *bonsai.InspectReport) {
	if len(rep.DragOut) == 0 {
		return
	}
	r.heading("Drag-out (what pruning this frees)",
		"freed = leaves the build with this module; survives = held by another importer (named)")
	rows := [][]string{}
	for i, d := range rep.DragOut {
		if i >= 40 {
			break
		}
		status := r.pal.good("freed")
		held := ""
		if !d.Freed {
			status = r.pal.warn("survives")
			held = joinModules(d.NeededBy)
		}
		rows = append(rows, []string{humize(d.Bytes), status, d.Module, held})
	}
	r.table([]string{"BYTES", "STATUS", colModule, "HELD BY"}, rows, nil)
}

// footer points readers to the sibling subjects, since anatomy deliberately carries no prune or
// go-version analysis. Skipped in markdown, which is usually embedded rather than browsed.
func (r *report) footer() {
	if r.md {
		return
	}
	fmt.Fprintf(r.w, "%s\n", r.pal.dim("→ bonsai prune       which dependencies to cut, and in what order"))
	fmt.Fprintf(r.w, "%s\n", r.pal.dim("→ bonsai go-version  the lowest go directive you can declare"))
}

// goFloor reports the lowest `go` directive the owned (main + 1st-class) modules could declare,
// the headroom available to reclaim right now (your declared version vs the dep-imposed floor),
// and the dependencies pinning that floor — the modules to prune to push it lower.
func (r *report) goFloor(f bonsai.GoFloor) {
	if f.Version == "" {
		r.heading("Go version floor", "no dependency declares a `go` directive — nothing constrains your minimum")
		return
	}

	r.heading("Go version floor",
		"the lowest `go` directive your own modules can declare; deps pin it — prune them to push it lower")

	// headroom you can reclaim now (drop your `go` line to the floor) vs what pruning would buy.
	if f.OwnedMax != "" && bonsai.CompareGoVersions(f.OwnedMax, f.Version) > 0 {
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

func (r *report) summary(an *bonsai.SizeReport) {
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

func (r *report) breakdown(an *bonsai.SizeReport) {
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

func (r *report) sectionsBlock(an *bonsai.SizeReport) {
	secs := append([]bonsai.SectionInfo(nil), an.Sections...)
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
// renders each entry with its importer tree beneath; otherwise it keeps the compact table.
func (r *report) largestModules(an *bonsai.SizeReport) {
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
		"class is relative to code you control; the tree under each module traces who imports it, back to your 1st-class code")
	if !r.md {
		fmt.Fprintf(r.w, "  %s\n", r.pal.head(fmt.Sprintf("%9s  %5s  %-5s  %-8s  %s", "SIZE", "%BIN", "CLASS", "KIND", colModule)))
	}
	shown := 0
	for _, m := range an.Modules {
		if m.Module == an.MainModule {
			continue
		}
		if m.Locked && an.HideLocked {
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
func (r *report) largestModulesTable(an *bonsai.SizeReport) {
	r.heading("Largest modules by size",
		"class is relative to code you control: 1st = yours, 2nd = direct dep of yours, 3rd = transitive (--why explains each)")
	rows := [][]string{}
	var dim []bool
	shown := 0
	for _, m := range an.Modules {
		if m.Module == an.MainModule {
			continue
		}
		if m.Locked && an.HideLocked {
			continue
		}
		rows = append(rows, []string{humize(m.Size), pctStr(m.Size, an.AccountedSize), m.Class, kindLabel(m), m.Module})
		dim = append(dim, m.Locked)
		if shown++; shown >= r.top {
			break
		}
	}
	r.table([]string{"SIZE", "%BIN", "CLASS", "KIND", colModule}, rows, dim)
}

// moduleRow prints one largest-modules entry as a fixed-width line (so its why tree can hang
// beneath it), dimming locked modules.
func (r *report) moduleRow(m bonsai.ModuleSize, denom uint64) {
	if r.md {
		fmt.Fprintf(r.w, "- **%s** — %s · %s · class %s · %s\n",
			m.Module, humize(m.Size), pctStr(m.Size, denom), m.Class, kindLabel(m))
		return
	}
	row := fmt.Sprintf("%9s  %5s  %-5s  %-8s  %s", humize(m.Size), pctStr(m.Size, denom), m.Class, kindLabel(m), m.Module)
	if m.Locked {
		row = r.pal.dim(row)
	}
	fmt.Fprintf(r.w, "  %s\n", row)
}

func (r *report) pruneCandidates(an *bonsai.PruneReport) {
	prunable := make([]bonsai.ModuleSize, 0, len(an.Modules))
	for _, m := range an.Modules {
		if m.Prune != nil {
			prunable = append(prunable, m)
		}
	}
	sort.Slice(prunable, func(i, j int) bool { return prunable[i].Prune.FreedBytes > prunable[j].Prune.FreedBytes })

	// when --blame is set, fold each target's fair-share of shared weight into a BLAME column
	// rather than a separate section, so EXCL (freed alone) and BLAME (fair cost) read across one row.
	blame := map[string]uint64{}
	for _, b := range an.Blame {
		blame[b.Module] = b.Blame
	}
	desc := "EXCL = freed by pruning this alone; POT = freeable weight in its subtree; GET% = EXCL/POT (the rest is shared with other deps)"
	if len(blame) > 0 {
		how := "sampled"
		if an.Blame[0].Exact {
			how = "exact"
		}
		desc += fmt.Sprintf("; BLAME = fair share of shared weight (Shapley, %s)", how)
	}

	r.heading("Prune candidates", desc)
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
		row := []string{r.pal.good(humize(p.FreedBytes)), humize(p.PotentialBytes)}
		if len(blame) > 0 {
			row = append(row, humize(blame[m.Module]))
		}
		row = append(row, getPct(p), itoa(len(p.FreedModules)),
			itoa(c.ImportingPackages), itoa(c.ImportSites), itoa(c.DistinctSymbols), m.Class, m.Module)
		rows = append(rows, row)
	}
	headers := []string{"EXCL", "POT"}
	if len(blame) > 0 {
		headers = append(headers, "BLAME")
	}
	headers = append(headers, "GET%", "ORPHANS", "IMP-PKGS", "IMP-SITES", "SYMS", "CLASS", colModule)
	r.table(headers, rows, nil)
}

// pruneHeadline renders the one-line "biggest realistic win" sentence for the top prune
// candidate, naming the shared weight that pruning it would NOT free and who holds it.
func (r *report) pruneHeadline(m bonsai.ModuleSize) {
	p := m.Prune
	line := fmt.Sprintf("best single win: prune %s → %s now", m.Module, humize(p.FreedBytes))
	if p.PotentialBytes > p.FreedBytes {
		line += fmt.Sprintf(", %s of the %s freeable in its subtree", getPct(p), humize(p.PotentialBytes))
	}
	if p.SharedBytes > 0 {
		extra := ""
		if len(p.SharedWith) > 0 && len(p.SharedWith[0].AlsoVia) > 0 {
			extra = " — co-prune " + joinModules(p.SharedWith[0].AlsoVia) + " to free it"
		}
		line += fmt.Sprintf(" (%s shared%s)", humize(p.SharedBytes), extra)
	}
	if r.md {
		fmt.Fprintf(r.w, "_%s_\n\n", line)
		return
	}
	fmt.Fprintf(r.w, "  %s\n\n", r.pal.good(line))
}

func (r *report) prunePlan(an *bonsai.PruneReport) {
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
func (r *report) planStep(n int, s bonsai.PrunePlanStep) {
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
func (r *report) planDeps(freed []bonsai.FreedModule, prefix string) {
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

// renderWhy prints a module's import-why tree beneath its already-labeled row, matching the
// explorer's tree style: ├─/└─ branch connectors, the module name colored by class (gold for
// 1st-class/main, the code you control), and a dim class tag. node is the module itself (the
// labeled row above), so its importers — node.Via — are the top-level branches. No purple
// selection highlight: every module here is a candidate, not one picked out from the rest.
func (r *report) renderWhy(node *bonsai.ImportNode, prefix string) {
	if node == nil {
		return
	}
	r.renderWhyChildren(node, prefix)
}

// renderWhyChildren draws node's importers (and a collapsed "+N more" leaf) as a connector
// forest, recursing toward the 1st-class code that pulled the dep in.
func (r *report) renderWhyChildren(node *bonsai.ImportNode, prefix string) {
	total := len(node.Via)
	if node.More > 0 {
		total++
	}
	for i, child := range node.Via {
		last := i == total-1
		r.whyLine(prefix, child.Module, child.Class, last)
		r.renderWhyChildren(child, prefix+r.whyCont(last))
	}
	if node.More > 0 {
		r.whyLine(prefix, fmt.Sprintf("+%d more", node.More), "", true)
	}
}

// whyLine renders one importer branch: a connector, the class-colored module name, and a dim
// class tag. In markdown it falls back to a nested bullet (box-drawing reads poorly there).
func (r *report) whyLine(prefix, mod, class string, last bool) {
	if r.md {
		tag := ""
		if class != "" {
			tag = fmt.Sprintf(" (%s)", class)
		}
		fmt.Fprintf(r.w, "%s- %s%s\n", prefix, mod, tag)
		return
	}
	branch := "├─ "
	if last {
		branch = "└─ "
	}
	name := mod
	if class == "1st" || class == "main" {
		name = r.pal.gold(mod)
	}
	tag := ""
	if class != "" {
		tag = r.pal.dim(" " + class)
	}
	fmt.Fprintf(r.w, "%s%s%s%s\n", prefix, r.pal.dim(branch), name, tag)
}

// whyCont returns the continuation indent threaded to a branch's children: a vertical rule when
// more siblings follow, blank when the branch was the last. Markdown nests by two spaces.
func (r *report) whyCont(last bool) string {
	switch {
	case r.md:
		return "  "
	case last:
		return "   "
	default:
		return "│  "
	}
}

// depLabel renders a freed unit's name: a "(std)" tag for standard-library packages (so e.g.
// pruning x/tools reads as the go/types toolchain, not a mystery "stdlib"), plus a co-prune
// note when the item only leaves once other targets are dropped too. No note means this prune
// alone frees it — answering "do I have to drop anything else to get rid of this?".
func (r *report) depLabel(f bonsai.FreedModule) string {
	label := f.Module
	if f.Std {
		label += " (std)"
	}
	if len(f.CoPrune) > 0 {
		label += "  " + r.pal.warn(fmt.Sprintf("(also prune %s)", joinModules(f.CoPrune)))
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

// table renders a titled table. In markdown mode it emits a pipe table; otherwise a
// color-aware aligned table (lipgloss measures width ignoring ANSI, so styled cells stay
// aligned). dim[i], when set, faints row i (used for locked modules). goodCol, when >= 0,
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
		StyleFunc(func(row, _ int) lipgloss.Style {
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

// humize is a local shorthand for the shared byte formatter, keeping the dense table-formatting
// code below readable.
func humize(b uint64) string { return humanize.Bytes(b) }

func coup(m bonsai.ModuleSize) *bonsai.Coupling {
	if m.Coupling != nil {
		return m.Coupling
	}
	return &bonsai.Coupling{}
}

// kindLabel describes a module's relationship to the build: locked deps (never pruned) win
// over the direct/indirect distinction since that is what gates a prune suggestion.
func kindLabel(m bonsai.ModuleSize) string {
	switch {
	case m.Locked:
		return "locked"
	case m.Direct:
		return "direct"
	default:
		return "indirect"
	}
}

// getPct renders the share of a target's subtree that pruning it actually frees (EXCL/POT).
func getPct(p *bonsai.PruneResult) string {
	if p.PotentialBytes == 0 {
		return "-"
	}
	return pctStr(p.FreedBytes, p.PotentialBytes)
}

// maxJoinedModules caps how many full module paths joinModules renders inline.
const maxJoinedModules = 3

// joinModules renders up to maxJoinedModules full module paths, collapsing the overflow into
// "+k more". Full paths (not last-segment short names) because these lists are actionable — a
// co-prune of "v2" or "generic" is meaningless, github.com/aws/aws-sdk-go-v2 is not.
func joinModules(modules []string) string {
	if len(modules) <= maxJoinedModules {
		return strings.Join(modules, ", ")
	}
	return strings.Join(modules[:maxJoinedModules], ", ") + fmt.Sprintf(" +%d more", len(modules)-maxJoinedModules)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// shortModule returns the last path element of a module path, used as a friendly label.
func shortModule(module string) string {
	if module == "" {
		return "main"
	}
	if i := strings.LastIndex(module, "/"); i >= 0 {
		return module[i+1:]
	}
	return module
}

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
