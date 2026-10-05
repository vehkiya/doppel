package ui

import "github.com/charmbracelet/lipgloss"

// Text styles for command output.
var (
	Title  = lipgloss.NewStyle().Bold(true).Foreground(ColorCoral)
	OK     = lipgloss.NewStyle().Foreground(ColorGreen)
	Warn   = lipgloss.NewStyle().Foreground(ColorAmber)
	Error  = lipgloss.NewStyle().Foreground(ColorRed)
	Dim    = lipgloss.NewStyle().Foreground(ColorDim)
	Label  = lipgloss.NewStyle().Foreground(ColorGray)
	Accent = lipgloss.NewStyle().Bold(true).Foreground(ColorCyan)
	Star   = lipgloss.NewStyle().Foreground(ColorAmber)
)

// lineStyle colors a diff line by its kind. Tabs are kept as they are, so
// the diff shows files exactly as they'll be written.
func lineStyle(kind byte) lipgloss.Style {
	style := lipgloss.NewStyle().TabWidth(lipgloss.NoTabConversion)
	switch kind {
	case '+':
		return style.Foreground(ColorGreen)
	case '-':
		return style.Foreground(ColorRed)
	case '@':
		return style.Foreground(ColorCyan)
	}
	return style
}
