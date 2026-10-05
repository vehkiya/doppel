package ui

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// HuhTheme styles doppel's wizards like sshx's.
func HuhTheme() *huh.Theme {
	t := huh.ThemeBase()

	// Left border indicator on focused fields
	t.Focused.Base = t.Focused.Base.BorderForeground(ColorPurple)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = lipgloss.NewStyle().Bold(true).Foreground(ColorCoral)
	t.Focused.Description = lipgloss.NewStyle().Foreground(ColorDim)
	t.Focused.Directory = lipgloss.NewStyle().Foreground(ColorCyan)

	// Validation errors
	t.Focused.ErrorIndicator = lipgloss.NewStyle().Bold(true).Foreground(ColorRed).SetString(" ✘")
	t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(ColorRed)

	// Select / Options
	t.Focused.SelectSelector = lipgloss.NewStyle().Bold(true).Foreground(ColorCoral).SetString("› ")
	t.Focused.NextIndicator = lipgloss.NewStyle().Foreground(ColorCoral).MarginLeft(1).SetString("→")
	t.Focused.PrevIndicator = lipgloss.NewStyle().Foreground(ColorCoral).MarginRight(1).SetString("←")
	t.Focused.Option = lipgloss.NewStyle().Foreground(ColorLightGray)

	// Multi-select / Checkboxes
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Bold(true).Foreground(ColorCoral).SetString("› ")
	t.Focused.SelectedOption = lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Bold(true).Foreground(ColorGreen).SetString("✔ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(ColorDim).SetString("• ")
	t.Focused.UnselectedOption = lipgloss.NewStyle().Foreground(ColorDim)

	// Confirm buttons
	button := lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Bold(true)
	t.Focused.FocusedButton = button.Foreground(ColorWhite).Background(ColorPurple)
	t.Focused.BlurredButton = button.Foreground(ColorDim).Background(ColorDarkGray)

	// Text inputs
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(ColorCyan)
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(ColorDim)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Bold(true).Foreground(ColorCyan).SetString("› ")
	t.Focused.TextInput.Text = lipgloss.NewStyle().Foreground(ColorLightGray)

	// Blurred fields (when moving between inputs in a group)
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = lipgloss.NewStyle().Foreground(ColorDim)
	t.Blurred.Description = lipgloss.NewStyle().Foreground(ColorDim)
	t.Blurred.TextInput.Prompt = lipgloss.NewStyle().Foreground(ColorDim).SetString("  ")
	t.Blurred.TextInput.Text = lipgloss.NewStyle().Foreground(ColorLightGray)
	t.Blurred.Option = lipgloss.NewStyle().Foreground(ColorDim)

	// Group title and description
	t.Group.Title = lipgloss.NewStyle().Bold(true).Foreground(ColorPurple).MarginBottom(1)
	t.Group.Description = lipgloss.NewStyle().Foreground(ColorDim).MarginBottom(1)

	// Help
	t.Help.ShortKey = lipgloss.NewStyle().Bold(true).Foreground(ColorCyan)
	t.Help.ShortDesc = lipgloss.NewStyle().Foreground(ColorDim)
	t.Help.ShortSeparator = lipgloss.NewStyle().Foreground(ColorSeparator)
	t.Help.FullKey = lipgloss.NewStyle().Bold(true).Foreground(ColorCyan)
	t.Help.FullDesc = lipgloss.NewStyle().Foreground(ColorDim)
	t.Help.FullSeparator = lipgloss.NewStyle().Foreground(ColorSeparator)

	return t
}
