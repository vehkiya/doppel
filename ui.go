package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(colorCoral)
	styleOK     = lipgloss.NewStyle().Foreground(colorGreen)
	styleWarn   = lipgloss.NewStyle().Foreground(colorAmber)
	styleError  = lipgloss.NewStyle().Foreground(colorRed)
	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleLabel  = lipgloss.NewStyle().Foreground(colorGray)
	styleAccent = lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	styleStar   = lipgloss.NewStyle().Foreground(colorAmber)
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

// errCancelled is returned when the user declines a confirmation.
var errCancelled = errors.New("cancelled; nothing was changed")

// app carries what every command needs: where files live and where to talk
// to the user.
type app struct {
	env         *Env
	cwd         string
	stdin       *bufio.Reader
	stdout      io.Writer
	stderr      io.Writer
	interactive bool // stdin is a terminal, so confirmations can be asked
}

func (a *app) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stdout, format, args...)
}

func (a *app) successf(format string, args ...any) {
	_, _ = fmt.Fprintln(a.stdout, styleOK.Render("✓")+" "+fmt.Sprintf(format, args...))
}

func (a *app) notef(format string, args ...any) {
	_, _ = fmt.Fprintln(a.stdout, styleDim.Render(fmt.Sprintf(format, args...)))
}

func (a *app) warnf(format string, args ...any) {
	_, _ = fmt.Fprintln(a.stderr, styleWarn.Render("⚠")+" "+fmt.Sprintf(format, args...))
}

// fail prints an error and returns the exit status for it.
func (a *app) fail(err error) int {
	_, _ = fmt.Fprintln(a.stderr, styleError.Render("✗")+" "+err.Error())
	return 1
}

// usageError prints a usage message and returns exit status 2.
func (a *app) usageError(usage string) int {
	_, _ = fmt.Fprintln(a.stderr, "Usage: "+usage)
	return 2
}

// confirm asks a yes/no question that defaults to no. assumeYes (--yes)
// answers it up front; without a terminal to ask on, it fails instead.
func (a *app) confirm(question string, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !a.interactive {
		return fmt.Errorf("%s Rerun with --yes to confirm", question)
	}
	a.printf("%s [y/N] ", question)
	line, err := a.stdin.ReadString('\n')
	if err != nil && line == "" {
		return errCancelled
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errCancelled
}
