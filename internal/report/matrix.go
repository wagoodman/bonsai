package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// WriteMatrixTable renders the build-matrix subject: the worst-case go floor and the cells that
// pin it, the per-cell floor (and size, when built), and the platform-specific modules. wide
// adds the full module-by-cell grid.
func WriteMatrixTable(w io.Writer, rep *bonsai.MatrixReport, wide, color bool) error {
	setColor(color)
	return (&report{w: w, pal: palette{on: color}}).writeMatrix(rep, wide)
}

// WriteMatrixMarkdown renders the build-matrix subject with markdown headings and pipe tables.
func WriteMatrixMarkdown(w io.Writer, rep *bonsai.MatrixReport, wide bool) error {
	return (&report{w: w, md: true}).writeMatrix(rep, wide)
}

func (r *report) writeMatrix(rep *bonsai.MatrixReport, wide bool) error {
	r.matrixHeadline(rep)
	r.matrixPerCell(rep)
	r.matrixPlatformSpecific(rep)
	if wide {
		r.matrixGrid(rep)
	}
	return nil
}

// matrixHeadline leads with the number that actually constrains go.mod: the MAX floor across
// cells, the cells that pin it, and where it drops to if their critical deps are pruned.
func (r *report) matrixHeadline(rep *bonsai.MatrixReport) {
	r.heading("Build matrix", "the worst-case go floor is the MAX over every platform you ship — that's the number for go.mod")
	if rep.SuccessfulCells() == 0 {
		r.floorNote(r.pal.warn(fmt.Sprintf("all %d cells failed to build — see per-cell errors below", len(rep.Cells))))
		fmt.Fprintln(r.w)
		return
	}
	if rep.WorstGo.Version == "" {
		r.floorNote(r.pal.dim("no dependency imposes a go floor across the matrix"))
		fmt.Fprintln(r.w)
		return
	}

	pinned := r.cellsPinning(rep)
	r.floorNote(r.pal.warn(fmt.Sprintf("worst-case go floor: %s", rep.WorstGo.Version)) +
		r.pal.dim(fmt.Sprintf("  (set by %s)", strings.Join(pinned, ", "))))
	if rep.WorstGo.NextVersion != "" && len(rep.WorstGo.Critical) > 0 {
		r.floorNote(r.pal.dim(fmt.Sprintf("drops to %s if you prune: %s",
			rep.WorstGo.NextVersion, joinModules(rep.WorstGo.Critical))))
	}
	fmt.Fprintln(r.w)
}

// cellsPinning returns the labels of the (successful) cells whose floor equals the worst-case
// floor — the platforms responsible for the constraint.
func (r *report) cellsPinning(rep *bonsai.MatrixReport) []string {
	var out []string
	for _, c := range rep.Cells {
		if c.Err == "" && c.Floor.Version == rep.WorstGo.Version {
			out = append(out, c.Label)
		}
	}
	return out
}

// matrixPerCell renders one row per declared cell: its floor (with a marker when it pins the
// worst case), its size when built, or the build error when the cell failed.
func (r *report) matrixPerCell(rep *bonsai.MatrixReport) {
	r.heading("Per-cell floor", "")
	headers := []string{"PLATFORM", "GO FLOOR"}
	if rep.WithSize {
		headers = append(headers, "SIZE")
	}
	headers = append(headers, "")
	rows := [][]string{}
	for _, c := range rep.Cells {
		if c.Err != "" {
			row := []string{c.Label, r.pal.warn("build failed")}
			if rep.WithSize {
				row = append(row, "-")
			}
			rows = append(rows, append(row, r.pal.dim(firstLine(c.Err))))
			continue
		}
		floor := c.Floor.Version
		if floor == "" {
			floor = r.pal.dim("(none)")
		}
		row := []string{c.Label, floor}
		if rep.WithSize {
			sz := "-"
			if c.Size != nil {
				sz = humize(c.Size.AccountedSize)
			}
			row = append(row, sz)
		}
		marker := ""
		if rep.WorstGo.Version != "" && c.Floor.Version == rep.WorstGo.Version {
			marker = r.pal.warn("← pins the floor")
		}
		rows = append(rows, append(row, marker))
	}
	r.table(headers, rows, nil)
}

// matrixPlatformSpecific lists the modules that are NOT in every successful cell — the
// divergence the matrix exists to surface. Universal modules are omitted (they're the same
// everywhere); the count is noted instead.
func (r *report) matrixPlatformSpecific(rep *bonsai.MatrixReport) {
	var specific []bonsai.MatrixModule
	for _, m := range rep.Modules {
		if !m.Universal {
			specific = append(specific, m)
		}
	}
	r.heading("Platform-specific modules",
		fmt.Sprintf("not in every cell (%d universal modules omitted)", len(rep.Universal)))
	if len(specific) == 0 {
		r.table([]string{colModule, "IN CELLS", "GO"}, nil, nil)
		return
	}

	headers := []string{colModule, "IN CELLS", "GO"}
	if rep.WithSize {
		headers = append(headers, "MAX SIZE")
	}
	rows := [][]string{}
	for i, m := range specific {
		if i >= 40 {
			rows = append(rows, []string{r.pal.dim(fmt.Sprintf("+%d more", len(specific)-40)), "", ""})
			break
		}
		gv := m.GoVersion
		if gv == "" {
			gv = "-"
		}
		row := []string{m.Module, strings.Join(m.InCells, ", "), gv}
		if rep.WithSize {
			row = append(row, humize(maxSize(m.SizeByCell)))
		}
		rows = append(rows, row)
	}
	r.table(headers, rows, nil)
}

// matrixGrid prints the full module-by-cell grid (--wide): every module as a row, every cell as
// a column, showing per-cell size when built or ✓/· presence otherwise.
func (r *report) matrixGrid(rep *bonsai.MatrixReport) {
	labels := make([]string, 0, len(rep.Cells))
	for _, c := range rep.Cells {
		labels = append(labels, c.Label)
	}
	r.heading("Full grid", "every module across every cell")
	headers := append([]string{colModule}, labels...)
	rows := [][]string{}
	for _, m := range rep.Modules {
		row := []string{m.Module}
		present := map[string]bool{}
		for _, l := range m.InCells {
			present[l] = true
		}
		for _, l := range labels {
			switch {
			case rep.WithSize && present[l]:
				row = append(row, humize(m.SizeByCell[l]))
			case present[l]:
				row = append(row, "✓")
			default:
				row = append(row, r.pal.dim("·"))
			}
		}
		rows = append(rows, row)
	}
	r.table(headers, rows, nil)
}

func maxSize(byCell map[string]uint64) uint64 {
	var largest uint64
	for _, v := range byCell {
		if v > largest {
			largest = v
		}
	}
	return largest
}

// firstLine returns the first non-empty line of s, for compact one-line error display.
func firstLine(s string) string {
	for ln := range strings.SplitSeq(s, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return ln
		}
	}
	return s
}
