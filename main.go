package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

func main() {
	a, err := newApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(a.run(os.Args[1:]))
}

func newApp() (*app, error) {
	env, err := loadEnv()
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
		a.printf("doppel %s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
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
	if err := checkGit(); err != nil {
		return a.fail(err)
	}
	return handler(args)
}

func (a *app) printUsage() {
	a.printf("%s\n\n", styleTitle.Render("doppel — Git accounts per folder"))
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
