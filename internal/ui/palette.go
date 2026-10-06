// Package ui holds doppel's terminal styling, shared with sshx, and the diff
// renderer behind --dry-run.
package ui

import "charm.land/lipgloss/v2"

// The doppel palette, shared with sshx (see AGENTS.md).
var (
	ColorPurple = lipgloss.Color("#7D56F4") // brand / accent
	ColorCoral  = lipgloss.Color("#FF5F87") // headers / selections
	ColorCyan   = lipgloss.Color("#00D7D7") // prompts / cursors / keys
	ColorGreen  = lipgloss.Color("#5FD787") // success / badges
	ColorAmber  = lipgloss.Color("#FFAF00") // warnings
	ColorRed    = lipgloss.Color("#FF4672") // errors / destructive actions

	ColorWhite     = lipgloss.Color("#FFFFFF")
	ColorBlack     = lipgloss.Color("#000000")
	ColorLightGray = lipgloss.Color("#EEEEEE")
	ColorGray      = lipgloss.Color("#8A8A8A")
	ColorDim       = lipgloss.Color("#767676")
	ColorSeparator = lipgloss.Color("#444444")
	ColorDarkGray  = lipgloss.Color("#262626")
	ColorSlate     = lipgloss.Color("#5F87AF")
)

// Badge renders a short bold label on a solid background.
type Badge int

// The badge kinds, by meaning.
const (
	BadgeOK     Badge = iota // green: a good state
	BadgeWarn                // amber: works, but worth knowing
	BadgeInfo                // slate: neutral
	BadgeAccent              // purple: the brand
	BadgeDanger              // red: destructive
)

// Render draws text as a badge.
func (b Badge) Render(text string) string {
	fg, bg := ColorBlack, ColorGreen
	switch b {
	case BadgeWarn:
		bg = ColorAmber
	case BadgeInfo:
		fg, bg = ColorWhite, ColorSlate
	case BadgeAccent:
		fg, bg = ColorWhite, ColorPurple
	case BadgeDanger:
		fg, bg = ColorWhite, ColorRed
	}
	return lipgloss.NewStyle().Bold(true).Foreground(fg).Background(bg).Padding(0, 1).Render(text)
}
