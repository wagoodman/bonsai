// Package style is bonsai's single source of truth for terminal color and emphasis.
// Every experience (the interactive TUIs and the plain-text reports) keeps its own local
// style set as a convenient interface, but builds those from THESE semantic tokens — so a
// given meaning (a win, a caution, code you control) renders identically everywhere instead
// of drifting across three hand-maintained palettes.
//
// The discipline is one job per color:
//
//	greyscale (Title/Strong/Heading bold, Subtle faint) — hierarchy: step forward / step back
//	Yours   (gold)   — identity: the main module and 1st-class deps, the code you control
//	Win     (green)  — a positive outcome: freed / saved / reclaimable weight
//	Caution (yellow) — a constraint or held-back thing: go-floor pinners, warnings, survivors
//	Active  (cyan)   — an interactive affordance or the focused pane: keys, in-use filter
//	Selection (purple) — the cursor row / an item singled out from the rest
//
// Colors are adaptive so they stay legible on both light and dark terminals.
package style

import "github.com/charmbracelet/lipgloss"

// semantic colors. Named for what they MEAN, never for the hue, so usage stays honest:
// reach for Win because something is freed, not because "it's the green one".
var (
	// Yours marks code you control — the main module and 1st-class deps.
	Yours = lipgloss.AdaptiveColor{Light: "136", Dark: "220"} // gold

	// Win marks a positive outcome — freed / saved / reclaimable weight.
	Win = lipgloss.AdaptiveColor{Light: "28", Dark: "2"} // green

	// Caution marks something that constrains you or is held back — go-floor pinners,
	// warnings, survivors that won't actually leave, co-prune requirements.
	Caution = lipgloss.AdaptiveColor{Light: "130", Dark: "3"} // amber/yellow

	// Active marks an interactive affordance or the focused pane — keybinding letters,
	// the in-use filter, the focused header bar.
	Active = lipgloss.AdaptiveColor{Light: "30", Dark: "6"} // cyan

	// Rule is the grey chrome — pane dividers and the focused header-bar fill.
	Rule = lipgloss.AdaptiveColor{Light: "250", Dark: "240"}
	// BarBg is the unfocused header-bar fill (a touch dimmer than Rule).
	BarBg = lipgloss.AdaptiveColor{Light: "253", Dark: "237"}

	// Notice is an out-of-band notification printed to stderr.
	Notice = lipgloss.AdaptiveColor{Light: "92", Dark: "13"} // magenta

	// selection is the cursor bar / picked-out item: bright text on a purple fill.
	selectionFg = lipgloss.AdaptiveColor{Light: "231", Dark: "15"}
	selectionBg = lipgloss.AdaptiveColor{Light: "135", Dark: "135"} // purple
)

// semantic styles. Hierarchy rides the greyscale axis — bold to step forward, faint to step
// back — leaving the hued styles below to carry meaning rather than just decoration.
var (
	Title   = lipgloss.NewStyle().Bold(true)  // a screen or report title
	Strong  = lipgloss.NewStyle().Bold(true)  // an emphasized value (a size, a module name)
	Heading = lipgloss.NewStyle().Bold(true)  // a section or table-column header
	Subtle  = lipgloss.NewStyle().Faint(true) // auxiliary / secondary / de-emphasized text

	YoursStyle   = lipgloss.NewStyle().Foreground(Yours)
	WinStyle     = lipgloss.NewStyle().Foreground(Win)
	CautionStyle = lipgloss.NewStyle().Foreground(Caution)
	ActiveStyle  = lipgloss.NewStyle().Foreground(Active)
	NoticeStyle  = lipgloss.NewStyle().Foreground(Notice)

	// Cursor is the highlighted row: bright text on the purple selection bar. An explicit
	// fg+bg (not Reverse) renders consistently over an unset terminal background.
	Cursor = lipgloss.NewStyle().Foreground(selectionFg).Background(selectionBg)
	// Picked is an item singled out by foreground alone — the why-tree's currently-selected
	// module. Same purple as the cursor bar, so the two read as one idea.
	Picked = lipgloss.NewStyle().Foreground(selectionBg)
)
