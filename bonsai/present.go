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
	r.sharedModules(an)
	return nil
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

func (r *report) largestModules(an *Analysis) {
	r.heading("Largest modules by size", "")
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
		kind := "indirect"
		if m.Direct {
			kind = "direct"
		}
		if m.Ignored {
			kind = "ignored"
		}
		rows = append(rows, []string{humize(m.Size), pctStr(m.Size, an.AccountedSize), kind, m.Module})
		dim = append(dim, m.Ignored)
		if shown++; shown >= r.top {
			break
		}
	}
	r.table([]string{"SIZE", "%BIN", "KIND", "MODULE"}, rows, dim)
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
		"drop a direct dep → bytes freed (own size + transitively-orphaned deps); coupling = how wired-in it is")
	rows := [][]string{}
	for i, m := range prunable {
		if i >= r.top {
			break
		}
		c := coup(m)
		rows = append(rows, []string{
			r.pal.good(humize(m.Prune.FreedBytes)), humize(m.Size), itoa(len(m.Prune.FreedModules)),
			itoa(c.ImportingPackages), itoa(c.ImportSites), itoa(c.DistinctSymbols), m.Module,
		})
	}
	r.table([]string{"FREED", "OWN", "ORPHANS", "IMP-PKGS", "IMP-SITES", "SYMS", "MODULE"}, rows, nil)
}

func (r *report) sharedModules(an *Analysis) {
	shared := an.Shared
	if an.HideIgnored {
		shared = shared[:0:0]
		for _, s := range an.Shared {
			if !s.Ignored {
				shared = append(shared, s)
			}
		}
	}

	r.heading("Load-bearing / shared",
		"pulled in by 2+ direct deps — structural weight no single prune removes (good ignore-list candidates)")
	if len(shared) == 0 {
		fmt.Fprintf(r.w, "  %s\n\n", r.pal.dim("none — every freeable module is exclusive to one direct dep"))
		return
	}
	rows := [][]string{}
	var dim []bool
	for i, s := range shared {
		if i >= r.top {
			break
		}
		rows = append(rows, []string{humize(s.Bytes), fmt.Sprintf("%d deps", s.SharedBy), s.Module})
		dim = append(dim, s.Ignored)
	}
	r.table([]string{"SIZE", "SHARED-BY", "MODULE"}, rows, dim)
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
