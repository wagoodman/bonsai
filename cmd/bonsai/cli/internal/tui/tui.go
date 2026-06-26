// Package tui holds the styling and small layout helpers shared by bonsai's interactive
// TUIs (the prune explorer and the lock editor) so they read as one tool: same circle
// glyphs, same purple cursor bar, same color palette.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// circle glyphs for selectable rows: filled = on (in binary / locked), hollow = off.
const (
	GlyphOn  = "●"
	GlyphOff = "○"
)

// FilterGlyph is the search symbol that prefixes the fuzzy filters.
const FilterGlyph = "⌕"

var (
	// the filter prompt is dim while empty and turns an accent color once it holds text, so an
	// in-use filter reads as "active" at a glance. The prompt width is constant either way.
	filterIdle   = lipgloss.NewStyle().Faint(true)
	filterActive = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan accent
)

// NewFilter builds the shared fuzzy-filter input: a search-glyph prompt with the given
// placeholder, already styled for its empty state.
func NewFilter(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.PlaceholderStyle = filterIdle
	StyleFilter(&ti)
	return ti
}

// StyleFilter refreshes the prompt to reflect whether the filter is in use: a dim ⌕ glyph when
// empty, a reverse accent chip with bold text once it holds a query. Call after the value may
// have changed.
func StyleFilter(ti *textinput.Model) {
	ti.Prompt = FilterGlyph + " "
	if strings.TrimSpace(ti.Value()) == "" {
		ti.PromptStyle = filterIdle
		ti.TextStyle = lipgloss.NewStyle()
		return
	}
	// active: accent the glyph and the typed text the same color.
	ti.PromptStyle = filterActive
	ti.TextStyle = filterActive
}

var (
	Title = lipgloss.NewStyle().Bold(true)
	Dim   = lipgloss.NewStyle().Faint(true)
	Help  = lipgloss.NewStyle().Faint(true)
	Good  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	Cyan  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	// RowCursor is the highlighted row: an explicit purple bar with bright text, rather than
	// Reverse(true) (whose fg/bg swap over an unset background renders inconsistently across
	// terminals).
	RowCursor = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("135"))
)

// Clamp constrains v to [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Fit right-pads s with spaces to width w (no-op if already wider), so a styled background
// fills the whole row.
func Fit(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// Truncate trims s to width with a leading … when it's too long, keeping the tail (module
// paths are most distinctive at the end).
func Truncate(s string, width int) string {
	if width < 4 || len(s) <= width {
		return s
	}
	return "…" + s[len(s)-(width-1):]
}

// Pad fixes s to a w×h block: each line is capped to w and the block to h rows (padding with
// blanks).
func Pad(s string, w, h int) string {
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
