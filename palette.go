package main

import "github.com/charmbracelet/lipgloss"

// The doppel palette, shared with sshx (see AGENTS.md). Colors join this
// list as the interface starts using them.
var (
	colorCoral = lipgloss.Color("#FF5F87") // headers / selections
	colorCyan  = lipgloss.Color("#00D7D7") // prompts / cursors / keys
	colorGreen = lipgloss.Color("#5FD787") // success / badges
	colorAmber = lipgloss.Color("#FFAF00") // warnings
	colorRed   = lipgloss.Color("#FF4672") // errors / destructive actions

	colorGray = lipgloss.Color("#8A8A8A")
	colorDim  = lipgloss.Color("#767676")
)
