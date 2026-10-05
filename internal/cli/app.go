// Package cli implements doppel's commands.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/ui"
	"github.com/vehkiya/doppel/internal/version"
)

// errCancelled is returned when the user declines a confirmation.
var errCancelled = errors.New("cancelled; nothing was changed")

// app carries what every command needs: where files live and where to talk
// to the user.
type app struct {
	env         *paths.Env
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
	_, _ = fmt.Fprintln(a.stdout, ui.OK.Render("✓")+" "+fmt.Sprintf(format, args...))
}

func (a *app) notef(format string, args ...any) {
	_, _ = fmt.Fprintln(a.stdout, ui.Dim.Render(fmt.Sprintf(format, args...)))
}

func (a *app) warnf(format string, args ...any) {
	_, _ = fmt.Fprintln(a.stderr, ui.Warn.Render("⚠")+" "+fmt.Sprintf(format, args...))
}

// fail prints an error and returns the exit status for it.
func (a *app) fail(err error) int {
	_, _ = fmt.Fprintln(a.stderr, ui.Error.Render("✗")+" "+err.Error())
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

// Run runs doppel with the process's arguments and terminal, and returns
// the exit status.
func Run(args []string) int {
	a, err := newApp()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return a.run(args)
}

func newApp() (*app, error) {
	env, err := paths.Load()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolving current directory: %w", err)
	}
	return &app{
		env:         env,
		cwd:         cwd,
		stdin:       bufio.NewReader(os.Stdin),
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		interactive: term.IsTerminal(os.Stdin.Fd()),
	}, nil
}

// run dispatches the command line and returns the process exit status.
func (a *app) run(args []string) int {
	cmd := "ls"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "-v", "--version", "version":
		a.printf("doppel %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.BuildDate)
		return 0
	case "-h", "--help", "help":
		a.printUsage()
		return 0
	}

	commands := map[string]func([]string) int{
		"ls":        a.cmdLs,
		"list":      a.cmdLs,
		"add":       a.cmdAdd,
		"edit":      a.cmdEdit,
		"rm":        a.cmdRm,
		"remove":    a.cmdRm,
		"delete":    a.cmdRm,
		"rename":    a.cmdRename,
		"bind":      a.cmdBind,
		"unbind":    a.cmdUnbind,
		"default":   a.cmdDefault,
		"whoami":    a.cmdWhoami,
		"uninstall": a.cmdUninstall,
	}
	handler, ok := commands[cmd]
	if !ok {
		_, _ = fmt.Fprintf(a.stderr, "Unknown command %q. Run `doppel help` for the list of commands.\n", cmd)
		return 2
	}
	if err := git.Check(); err != nil {
		return a.fail(err)
	}
	return handler(args)
}

func (a *app) printUsage() {
	a.printf("%s\n\n", ui.Title.Render("doppel — Git accounts per folder"))
	a.printf(`Usage:
  doppel                                 List accounts
  doppel ls                              List accounts
  doppel add <id> --name <name> --email <email> [--host <host>]...
             [--github-user <user>] [--folder <folder>]... [--default]
                                         Add an account
  doppel edit <id> [same flags as add]   Change an account (--host and --folder
                                         replace the current list)
  doppel rm <id>                         Delete an account (key files are kept)
  doppel rename <id> <new-id>            Change an account's ID
  doppel bind <id> <folder>...           Use an account for repos in these folders
  doppel unbind <folder>...              Remove folder rules
  doppel default [<id> | --none]         Show or set the default account
  doppel whoami [path]                   Show which account applies, and why
  doppel uninstall                       Remove doppel's include from your Git config
  doppel version                         Show the version

Commands that change files accept:
  --dry-run   show the changes without writing them
  --yes       answer yes to confirmations

Accounts live in ~/.config/doppel/accounts/<id>.gitconfig. Repos inside a
bound folder use that folder's account; everything else uses the default.
`)
}
