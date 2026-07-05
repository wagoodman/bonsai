package report

import (
	"fmt"
	"io"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// WriteDiffTable renders the size + go-floor delta against a baseline ref: the two headline
// scalars first (net size, floor), then the modules added/removed/changed.
func WriteDiffTable(w io.Writer, rep *bonsai.DiffReport, top int, color bool) error {
	setColor(color)
	return (&report{w: w, top: top, pal: palette{on: color}}).writeDiff(rep)
}

// WriteDiffMarkdown renders the diff with markdown headings and pipe tables (for pasting into a PR).
func WriteDiffMarkdown(w io.Writer, rep *bonsai.DiffReport, top int) error {
	return (&report{w: w, top: top, md: true}).writeDiff(rep)
}

func (r *report) writeDiff(rep *bonsai.DiffReport) error {
	r.diffSummary(rep)
	r.diffSection("added", rep.Added)
	r.diffSection("removed", rep.Removed)
	r.diffSection("changed", rep.Changed)
	return nil
}

// diffSummary leads with the two gate-relevant scalars: net size and floor movement.
func (r *report) diffSummary(rep *bonsai.DiffReport) {
	target := shortCommit(rep.BaselineCommit, rep.Ref)
	if rep.BaselineBinary != "" {
		target = rep.BaselineBinary
	}
	base := fmt.Sprintf("diff against %s", target)
	if rep.Dirty {
		base += " (working tree dirty)"
	}
	r.heading(base, "what this branch does to your size and your go floor")

	if rep.SizeDelta == 0 && rep.GoFloor.Direction == 0 && len(rep.GoFloor.NewlyCritical) == 0 &&
		len(rep.Added) == 0 && len(rep.Removed) == 0 && len(rep.Changed) == 0 {
		r.floorNote(r.pal.dim("no change in size or go floor"))
		fmt.Fprintln(r.w)
		return
	}

	fmt.Fprintf(r.w, "  size      %s → %s   %s\n",
		humize(rep.BaselineSize), humize(rep.CurrentSize), r.pal.strong(signedBytes(rep.SizeDelta)))
	r.diffFloorLine(rep.GoFloor)
	fmt.Fprintln(r.w)
}

func (r *report) diffFloorLine(f bonsai.GoFloorDiff) {
	switch {
	case f.Direction > 0:
		fmt.Fprintf(r.w, "  go floor  %s → %s   %s%s\n",
			emptyDash(f.Before), emptyDash(f.After), r.pal.warn("raised"),
			r.newlyCriticalNote("raised by ", f.NewlyCritical))
	case f.Direction < 0:
		fmt.Fprintf(r.w, "  go floor  %s → %s   %s\n",
			emptyDash(f.Before), emptyDash(f.After), r.pal.good("lowered"))
	default:
		// floor version held, but a different dep may now pin it — that churn is the signal.
		fmt.Fprintf(r.w, "  go floor  %s   %s%s\n",
			emptyDash(f.After), r.pal.dim("unchanged"),
			r.newlyCriticalNote("now pinned by ", f.NewlyCritical))
	}
}

func (r *report) newlyCriticalNote(prefix string, mods []string) string {
	if len(mods) == 0 {
		return ""
	}
	return "  " + r.pal.dim("("+prefix+joinModules(mods)+")")
}

// diffSection renders one add/remove/changed block: a count + net header, then a byte-tagged row
// per module (direct vs transitive), capped at --top.
func (r *report) diffSection(label string, mods []bonsai.ModuleDiff) {
	if len(mods) == 0 {
		return
	}
	var net int64
	transitive := 0
	for _, m := range mods {
		net += m.Bytes
		if !m.Direct {
			transitive++
		}
	}
	// "new transitive" only makes sense for added deps; removed/changed already existed.
	subtitle := ""
	if label == "added" {
		subtitle = fmt.Sprintf("%d new transitive", transitive)
	}
	r.heading(fmt.Sprintf("%s (%d, %s)", label, len(mods), signedBytes(net)), subtitle)

	rows := [][]string{}
	for i, m := range mods {
		if i >= r.top {
			rows = append(rows, []string{r.pal.dim(fmt.Sprintf("+%d more", len(mods)-r.top)), "", ""})
			break
		}
		rows = append(rows, []string{signedBytes(m.Bytes), m.Module, directLabel(m.Direct)})
	}
	r.table([]string{"BYTES", colModule, "KIND"}, rows, nil)
}

func directLabel(direct bool) string {
	if direct {
		return "direct"
	}
	return "transitive"
}

// signedBytes formats a signed byte delta with an explicit sign, e.g. "+2.1 MB", "-0.3 MB".
func signedBytes(n int64) string {
	if n < 0 {
		return "-" + humize(uint64(-n))
	}
	return "+" + humize(uint64(n))
}

func emptyDash(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// shortCommit prefers a 12-char commit prefix, falling back to the ref when the baseline is a
// symbolic ref rather than a resolved sha.
func shortCommit(commit, ref string) string {
	if len(commit) >= 12 && isHex(commit) {
		return commit[:12]
	}
	if commit != "" {
		return commit
	}
	return ref
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
