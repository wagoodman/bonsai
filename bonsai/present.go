package bonsai

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// WriteJSON renders the analysis as indented JSON.
func WriteJSON(w io.Writer, an *Analysis) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(an)
}

// WriteText renders the human-readable report (plain text) to w, showing up to top rows
// in each ranked table.
func WriteText(w io.Writer, an *Analysis, top int) error {
	return writeReport(w, an, top, false)
}

// WriteMarkdown renders the report with markdown headings and fenced code blocks.
func WriteMarkdown(w io.Writer, an *Analysis, top int) error {
	return writeReport(w, an, top, true)
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

func writeReport(w io.Writer, an *Analysis, top int, md bool) error {
	var thirdParty uint64
	for _, m := range an.Modules {
		if m.Module != an.MainModule {
			thirdParty += m.Size
		}
	}
	denom := an.AccountedSize // the stripped size: all per-content/per-owner numbers are of this
	fmt.Fprintf(w, "# binary size analysis\n\n")
	if !an.Stripped && an.BinarySize > an.AccountedSize {
		// the analyzed file is an unstripped build; show what stripping removes.
		removed := an.BinarySize - an.AccountedSize
		fmt.Fprintf(w, "analyzed binary (unstripped):   %s\n", humize(an.BinarySize))
		fmt.Fprintf(w, "  debug info + symbol table:    %s (%.0f%%)   removed by `-s -w` stripping\n", humize(removed), pct(removed, an.BinarySize))
		fmt.Fprintf(w, "  stripped binary:              %s (%.0f%%)   matches the release artifact\n", humize(an.AccountedSize), pct(an.AccountedSize, an.BinarySize))
	} else {
		fmt.Fprintf(w, "binary size:                    %s\n", humize(an.AccountedSize))
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "stripped binary by content:\n")
	fmt.Fprintf(w, "  executable code:    %s (%.0f%%)\n", humize(an.CodeSize), pct(an.CodeSize, denom))
	fmt.Fprintf(w, "  named data:         %s (%.0f%%)\n", humize(an.DataSize), pct(an.DataSize, denom))
	fmt.Fprintf(w, "  gopclntab metadata: %s (%.0f%%)  (distributed per-package, proportional to code)\n", humize(an.PclntabSize), pct(an.PclntabSize, denom))
	fmt.Fprintf(w, "stripped binary by owner (code + data + pclntab share):\n")
	fmt.Fprintf(w, "  main module (%s): %s\n", shortModule(an.MainModule), humize(an.MainSize))
	fmt.Fprintf(w, "  third-party:        %s\n", humize(thirdParty))
	fmt.Fprintf(w, "  std library:        %s\n", humize(an.StdSize))
	fmt.Fprintf(w, "  generated/anonymous:%s  (pooled constants & type metadata; incl. anonymous rodata)\n", humize(an.GeneratedSize))
	if an.Stripped {
		fmt.Fprintf(w, "\nwarning: binary is stripped — only executable code could be attributed (no data/pclntab).\n")
	}
	fmt.Fprintln(w)

	// section breakdown shows where bytes live (text, pclntab, rodata, ...).
	secs := append([]SectionInfo(nil), an.Sections...)
	sort.Slice(secs, func(i, j int) bool { return secs[i].Size > secs[j].Size })
	t0 := newTable(w, md, "Sections (file-backed)", "", "SIZE", "%BIN", "SECTION")
	for i, s := range secs {
		if i >= 8 || s.Size == 0 {
			break
		}
		t0.row(humize(s.Size), fmt.Sprintf("%.1f%%", pct(s.Size, denom)), s.Name)
	}
	t0.flush()

	// table 1: largest modules overall, flagging indirect (transitively pulled-in) ones.
	t1 := newTable(w, md, "Largest modules by size",
		"", "SIZE", "%BIN", "KIND", "MODULE")
	shown := 0
	for _, m := range an.Modules {
		if m.Module == an.MainModule {
			continue
		}
		kind := "indirect"
		if m.Direct {
			kind = "direct"
		}
		t1.row(humize(m.Size), fmt.Sprintf("%.1f%%", pct(m.Size, an.AccountedSize)), kind, m.Module)
		if shown++; shown >= top {
			break
		}
	}
	t1.flush()

	// table 2: prune candidates — direct deps ranked by bytes freed if removed.
	prunable := make([]ModuleSize, 0, len(an.Modules))
	for _, m := range an.Modules {
		if m.Prune != nil {
			prunable = append(prunable, m)
		}
	}
	sort.Slice(prunable, func(i, j int) bool { return prunable[i].Prune.FreedBytes > prunable[j].Prune.FreedBytes })
	t2 := newTable(w, md, "Prune candidates (direct deps, ranked by bytes freed if removed)",
		"freed = own size + transitively-orphaned deps if first-party code stopped importing it",
		"FREED", "OWN", "MODS", "IMP-PKGS", "IMP-SITES", "SYMS", "MODULE")
	for i, m := range prunable {
		if i >= top {
			break
		}
		c := coup(m)
		t2.row(humize(m.Prune.FreedBytes), humize(m.Size), itoa(len(m.Prune.FreedModules)),
			itoa(c.ImportingPackages), itoa(c.ImportSites), itoa(c.DistinctSymbols), m.Module)
	}
	t2.flush()

	// table 3: low-hanging fruit — direct deps with the fewest tendrils into the code.
	easy := append([]ModuleSize(nil), prunable...)
	sort.Slice(easy, func(i, j int) bool {
		ci, cj := coup(easy[i]), coup(easy[j])
		if ci.DistinctSymbols != cj.DistinctSymbols {
			return ci.DistinctSymbols < cj.DistinctSymbols
		}
		return easy[i].Prune.FreedBytes > easy[j].Prune.FreedBytes
	})
	t3 := newTable(w, md, "Easiest to remove (direct deps with fewest first-party tendrils)",
		"low symbol/import counts => loosely coupled; pair with FREED to spot cheap wins",
		"SYMS", "IMP-PKGS", "IMP-SITES", "FREED", "MODULE")
	for i, m := range easy {
		if i >= top {
			break
		}
		c := coup(m)
		t3.row(itoa(c.DistinctSymbols), itoa(c.ImportingPackages), itoa(c.ImportSites),
			humize(m.Prune.FreedBytes), m.Module)
	}
	t3.flush()

	writeActionTree(w, an, top, md)
	return nil
}

// writeActionTree renders the prune actions as a tree: each "drop ..." line is an action
// and the modules beneath it are what that action frees. Single-dep actions are direct
// wins; co-prune actions free modules that no single drop would (they need every listed
// dep dropped because each independently pulls the modules in).
func writeActionTree(w io.Writer, an *Analysis, top int, md bool) {
	// only show actions that reveal structure the candidates table doesn't already:
	// single drops that orphan OTHER modules, and co-prune groups above a real floor.
	const coPruneFloor = 100_000 // bytes; below this a multi-dep group is noise
	var singles, coprune []PruneAction
	for _, a := range an.Actions {
		switch {
		case len(a.Deps) == 1 && freesBeyondSelf(a):
			singles = append(singles, a)
		case len(a.Deps) >= 2 && a.Bytes >= coPruneFloor:
			coprune = append(coprune, a)
		}
	}

	blockStart(w, md, "Prune action tree",
		"each leaf is the portion of a module freed by the action; a module can appear in several actions (its packages have different blockers). co-prune groups free nothing until ALL their deps are dropped together")

	fmt.Fprintln(w, "single-dep drops that also orphan transitive deps (see the table above for the rest):")
	for i, a := range singles {
		if i >= top {
			fmt.Fprintf(w, "  … %d more single-dep drops\n", len(singles)-i)
			break
		}
		renderAction(w, a, false)
	}

	fmt.Fprintln(w)
	if len(coprune) == 0 {
		fmt.Fprintln(w, "co-prune groups: none — every freeable module is exclusive to one direct dep")
	} else {
		fmt.Fprintln(w, "co-prune groups (freed ONLY if every listed dep is dropped together):")
		for i, a := range coprune {
			if i >= top {
				fmt.Fprintf(w, "  … %d more co-prune groups\n", len(coprune)-i)
				break
			}
			renderAction(w, a, true)
		}
	}
	blockEnd(w, md)
}

func renderAction(w io.Writer, a PruneAction, co bool) {
	const maxLeaves = 6
	header := "drop " + strings.Join(a.Deps, " + ")
	suffix := fmt.Sprintf("%s (%d modules)", humize(a.Bytes), len(a.Modules))
	if co {
		suffix = fmt.Sprintf("+%s (%d modules, needs all %d deps)", humize(a.Bytes), len(a.Modules), len(a.Deps))
	}
	fmt.Fprintf(w, "\n%s  →  %s\n", header, suffix)

	leaves := a.Modules
	var moreCount int
	var moreBytes uint64
	if len(leaves) > maxLeaves {
		for _, m := range leaves[maxLeaves:] {
			moreBytes += m.Bytes
		}
		moreCount = len(leaves) - maxLeaves
		leaves = leaves[:maxLeaves]
	}
	hasTail := moreCount > 0 || a.StdBytes > 0
	for i, m := range leaves {
		last := i == len(leaves)-1 && !hasTail
		fmt.Fprintf(w, "%s %-52s %s\n", branch(last), m.Module, humize(m.Bytes))
	}
	if moreCount > 0 {
		fmt.Fprintf(w, "%s %-52s %s\n", branch(a.StdBytes == 0), fmt.Sprintf("+ %d more modules", moreCount), humize(moreBytes))
	}
	if a.StdBytes > 0 {
		fmt.Fprintf(w, "%s %-52s %s\n", branch(true), "(standard-library packages)", humize(a.StdBytes))
	}
}

// freesBeyondSelf reports whether a single-dep drop orphans modules other than the dep
// itself — i.e. it has a transitive tree worth showing. A drop that frees only its own
// module is already conveyed by the prune-candidates table.
func freesBeyondSelf(a PruneAction) bool {
	for _, m := range a.Modules {
		if m.Module != a.Deps[0] {
			return true
		}
	}
	return false
}

func branch(last bool) string {
	if last {
		return "└─"
	}
	return "├─"
}

func blockStart(w io.Writer, md bool, title, subtitle string) {
	if md {
		fmt.Fprintf(w, "## %s\n\n", title)
		if subtitle != "" {
			fmt.Fprintf(w, "_%s_\n\n", subtitle)
		}
		fmt.Fprintln(w, "```")
		return
	}
	fmt.Fprintf(w, "== %s ==\n", title)
	if subtitle != "" {
		fmt.Fprintf(w, "%s\n", subtitle)
	}
}

func blockEnd(w io.Writer, md bool) {
	if md {
		fmt.Fprintln(w, "```")
	}
	fmt.Fprintln(w)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// table renders an aligned columnar table, fenced as a code block in markdown mode.
type table struct {
	w   io.Writer
	md  bool
	tw  *tabwriter.Writer
	hdr []string
}

func newTable(w io.Writer, md bool, title, subtitle string, header ...string) *table {
	if md {
		fmt.Fprintf(w, "## %s\n\n", title)
		if subtitle != "" {
			fmt.Fprintf(w, "_%s_\n\n", subtitle)
		}
		fmt.Fprintln(w, "```")
	} else {
		fmt.Fprintf(w, "== %s ==\n", title)
		if subtitle != "" {
			fmt.Fprintf(w, "%s\n", subtitle)
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	return &table{w: w, md: md, tw: tw, hdr: header}
}

func (t *table) row(cells ...string) { fmt.Fprintln(t.tw, strings.Join(cells, "\t")) }

func (t *table) flush() {
	t.tw.Flush()
	if t.md {
		fmt.Fprintln(t.w, "```")
	}
	fmt.Fprintln(t.w)
}

func pct(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}
