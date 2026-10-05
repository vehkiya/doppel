package ui

import "github.com/charmbracelet/lipgloss"

// Text styles for command output.
var (
	Title  = lipgloss.NewStyle().Bold(true).Foreground(colorCoral)
	OK     = lipgloss.NewStyle().Foreground(colorGreen)
	Warn   = lipgloss.NewStyle().Foreground(colorAmber)
	Error  = lipgloss.NewStyle().Foreground(colorRed)
	Dim    = lipgloss.NewStyle().Foreground(colorDim)
	Label  = lipgloss.NewStyle().Foreground(colorGray)
	Accent = lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	Star   = lipgloss.NewStyle().Foreground(colorAmber)
)

// lineStyle colors a diff line by its kind. Tabs are kept as they are, so
// the diff shows files exactly as they'll be written.
func lineStyle(kind byte) lipgloss.Style {
	style := lipgloss.NewStyle().TabWidth(lipgloss.NoTabConversion)
	switch kind {
	case '+':
		return style.Foreground(colorGreen)
	case '-':
		return style.Foreground(colorRed)
	case '@':
		return style.Foreground(colorCyan)
	}
	return style
}
