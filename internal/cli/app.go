// Package cli implements doppel's commands.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
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
	accessible  bool // run forms as plain line-by-line prompts ($ACCESSIBLE, and tests)
	browsable   bool // stdin and stdout are terminals, so the browser can open

	generate func(path, comment string) error // creates a key, asking for its passphrase
	copy     func(text string) error          // puts text on the clipboard

	unlock func() // releases the write lock while this command holds it

	// githubHosts remembers which hosts are GitHub, and the host gh talks to
	// for each, since finding out may run gh. The browser forgets it each
	// time it opens, so signing in to gh meanwhile counts.
	githubHosts map[string]githubHost
}

type githubHost struct {
	api string
	ok  bool
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
	yes := false
	err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&yes),
	)))
	if err != nil {
		return err
	}
	if !yes {
		return errCancelled
	}
	return nil
}

// runForm runs a Huh form in doppel's theme. In accessible mode, used by
// tests, it reads answers line by line from stdin instead of drawing the form.
func (a *app) runForm(form *huh.Form) error {
	form = form.WithTheme(ui.HuhTheme()).WithKeyMap(formKeyMap()).WithShowHelp(true)
	if a.accessible {
		form = form.WithAccessible(true).WithInput(lineReader{a.stdin}).WithOutput(a.stdout)
	}
	err := form.Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return errCancelled
	}
	return err
}

// lineReader hands over input one line per Read. Huh's accessible mode
// scans each answer with a new scanner, which would otherwise swallow the
// answers after it.
type lineReader struct{ r *bufio.Reader }

func (l lineReader) Read(p []byte) (int, error) {
	line, err := l.r.ReadString('\n')
	if len(line) > len(p) {
		line = line[:len(p)]
	}
	n := copy(p, line)
	if n > 0 {
		return n, nil
	}
	return 0, err
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
		stdout:      ui.Writer(os.Stdout),
		stderr:      ui.Writer(os.Stderr),
		interactive: term.IsTerminal(os.Stdin.Fd()),
		browsable:   term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()),
		// ACCESSIBLE turns forms into plain prompts for screen readers, as in other Charm tools.
		accessible: os.Getenv("ACCESSIBLE") != "",
		generate: func(path, comment string) error {
			return keys.Generate(path, comment, nil, os.Stdin, os.Stdout, os.Stderr)
		},
		copy: copyToClipboard,
	}, nil
}

// run dispatches the command line and returns the process exit status.
func (a *app) run(args []string) int {
	defer a.unlockWrites()
	if len(args) == 0 && a.browsable {
		if err := git.Check(); err != nil {
			return a.fail(err)
		}
		return a.browse()
	}
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
	case "update", "upgrade": // doesn't need Git, so it can run even when Git is the problem
		return a.cmdUpdate(args)
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
		"export":    a.cmdExport,
		"upload":    a.cmdUpload,
		"doctor":    a.cmdDoctor,
		"test":      a.cmdTest,
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
  doppel                                 Browse accounts (lists them when not in a terminal)
  doppel ls                              List accounts
  doppel add [<id>]                      Add an account, asking for each setting
  doppel add <id> --name <name> --email <email> [--host <host>]...
             [--github-user <user>] [--folder <folder>]... [--default]
             [key flags]                 Add an account without questions
  doppel edit <id>                       Change an account, asking for each setting
  doppel edit <id> [same flags as add]   Change only what the flags say (--host and
                                         --folder replace the current list)
  doppel rm <id>                         Delete an account (key files are kept)
  doppel rename <id> <new-id>            Change an account's ID
  doppel bind <id> <folder>...           Use an account for repos in these folders
  doppel unbind <folder>...              Remove folder rules
  doppel default [<id> | --none]         Show or set the default account
  doppel whoami [path] [--offline]       Show which account applies, and why
  doppel test [<id>]                     Log in to each host and sign a test message
  doppel export <id> [--auth|--signing]
             [--no-copy]                 Print and copy a public key, with where to add it
  doppel upload <id> [--auth|--signing]  Add the account's keys to its GitHub user (with gh)
  doppel doctor [--fix]                  Check every account for problems
  doppel update [--check] [--force]      Install the latest signed release (--check only
                                         looks, --force reinstalls the current one)
  doppel uninstall                       Take doppel's blocks out of your Git config and
                                         allowed_signers (accounts and keys are kept)
  doppel version                         Show the version

ls, rm and update also answer to list, remove or delete, and upgrade.

Key flags (add and edit):
  --auth-key <key>         SSH key for fetching and pushing ("" for ssh's own keys)
  --generate-auth-key      generate ~/.ssh/id_ed25519_<id> (asks for a passphrase)
  --signing-key <key>      SSH key for signing commits and tags
  --generate-signing-key   generate ~/.ssh/id_ed25519_<id>_signing
  --sign-with-auth-key     sign with the auth key
  --no-signing             stop signing
  --sign-commits=false, --sign-tags=false
                           sign only tags, or only commits

Commands that change files accept:
  --dry-run   show the changes without writing them
  --yes       answer yes to confirmations

Set ACCESSIBLE=1 for plain prompts instead of interactive forms.

Accounts live in ~/.config/doppel/accounts/<id>.gitconfig. Repos inside a
bound folder use that folder's account; everything else uses the default.
`)
}
