package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// stringList is a repeatable flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("doppel "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags parses flags that may come before, between or after the
// positional arguments, and returns the positional ones.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// flagWasSet reports whether a flag appeared on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// writeFlags are shared by every command that changes files.
type writeFlags struct {
	dryRun bool
	yes    bool
}

func (w *writeFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&w.dryRun, "dry-run", false, "show the changes without writing them")
	fs.BoolVar(&w.yes, "yes", false, "answer yes to confirmations")
}

// assumeYes is true when confirmations needn't be asked: with --yes, or
// with --dry-run, which changes nothing.
func (w *writeFlags) assumeYes() bool { return w.yes || w.dryRun }

// accountFlags are the account fields shared by add and edit.
type accountFlags struct {
	name, email, githubUser string
	hosts, folders          stringList
	makeDefault             bool
}

func (f *accountFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.name, "name", "", "commit author name")
	fs.StringVar(&f.email, "email", "", "commit author email")
	fs.StringVar(&f.githubUser, "github-user", "", "GitHub username")
	fs.Var(&f.hosts, "host", "Git host (repeatable)")
	fs.Var(&f.folders, "folder", "folder the account applies to (repeatable)")
	fs.BoolVar(&f.makeDefault, "default", false, "make this the default account")
}

// parseCommand parses a command's flags, printing its usage on errors and
// for --help. ok is false when the command should stop with code.
func (a *app) parseCommand(fs *flag.FlagSet, args []string, usage string) (positional []string, code int, ok bool) {
	positional, err := parseFlags(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		a.printf("Usage: %s\n", usage)
		return nil, 0, false
	}
	if err != nil {
		_, _ = fmt.Fprintln(a.stderr, styleError.Render("✗")+" "+err.Error())
		return nil, a.usageError(usage), false
	}
	return positional, 0, true
}

// save writes accounts as the complete set of accounts, or with --dry-run
// shows what would change.
func (a *app) save(accounts []*Account, w writeFlags, done string) int {
	if err := validateAccounts(a.env, accounts); err != nil {
		return a.fail(err)
	}
	plan, err := newPlan()
	if err != nil {
		return a.fail(err)
	}
	defer plan.Close()
	if err := stageAccounts(a.env, plan, accounts); err != nil {
		return a.fail(err)
	}
	return a.finish(plan, w, done)
}

// finish applies a plan, or prints it for --dry-run.
func (a *app) finish(plan *Plan, w writeFlags, done string) int {
	if w.dryRun {
		changes, err := plan.Changes()
		if err != nil {
			return a.fail(err)
		}
		if len(changes) == 0 {
			a.notef("Dry run: nothing would change.")
			return 0
		}
		a.notef("Dry run: nothing was written. These changes would be made:")
		for _, c := range changes {
			a.printf("\n")
			writeDiff(a.stdout, a.env.Shorten(c.Path), c)
		}
		return 0
	}
	changes, err := plan.Apply()
	if err != nil {
		return a.fail(err)
	}
	a.successf("%s", done)
	for _, c := range changes {
		verb := "updated"
		switch {
		case !c.Existed:
			verb = "created"
		case !c.Exists:
			verb = "removed"
		}
		a.notef("  %s %s", verb, a.env.Shorten(c.Path))
	}
	return 0
}

// bindFolders adds folders to acc. A folder bound to another account moves
// to acc after confirmation.
func (a *app) bindFolders(accounts []*Account, acc *Account, inputs []string, w writeFlags) error {
	for _, input := range inputs {
		folder, exists, err := a.env.NormalizeFolder(input, a.cwd)
		if err != nil {
			return err
		}
		if !exists {
			a.warnf("%s doesn't exist yet; the rule applies to repos created there later", folder)
		}
		owner := folderOwner(a.env, accounts, folder)
		if owner == acc {
			continue
		}
		if owner != nil {
			question := fmt.Sprintf("%s is bound to %s. Move it to %s?", folder, owner.ID, acc.ID)
			if err := a.confirm(question, w.assumeYes()); err != nil {
				return err
			}
			owner.removeFolder(a.env, folder)
		}
		acc.Folders = append(acc.Folders, folder)
	}
	return nil
}

const lsUsage = "doppel ls"

func (a *app) cmdLs(args []string) int {
	if _, code, ok := a.parseCommand(newFlagSet("ls"), args, lsUsage); !ok {
		return code
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	if len(accounts) == 0 {
		a.printf("No accounts yet. Add one with:\n  doppel add <id> --name \"Your Name\" --email you@example.com\n")
		return 0
	}

	rows := [][]string{{"", "ACCOUNT", "EMAIL", "FOLDERS"}}
	for _, acc := range accounts {
		marker := ""
		if acc.Default {
			marker = styleStar.Render("★")
		}
		folders := "—"
		if len(acc.Folders) > 0 {
			folders = strings.Join(acc.Folders, ", ")
		}
		rows = append(rows, []string{marker, styleAccent.Render(acc.ID), acc.Email, folders})
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	for r, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			if r == 0 {
				cell = styleLabel.Render(cell)
			}
			line.WriteString(cell)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(cell)+2))
			}
		}
		a.printf("%s\n", strings.TrimRight(line.String(), " "))
	}
	a.printf("\n")
	if defaultAccount(accounts) != nil {
		a.notef("★ default account, used for repos outside every folder")
	} else {
		a.notef("No default account: repos outside every folder use your global Git config.")
	}
	return 0
}

const addUsage = `doppel add <id> --name <name> --email <email> [--host <host>]... [--github-user <user>] [--folder <folder>]... [--default] [--dry-run] [--yes]`

func (a *app) cmdAdd(args []string) int {
	fs := newFlagSet("add")
	var f accountFlags
	var w writeFlags
	f.register(fs)
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, addUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return a.usageError(addUsage)
	}
	id := positional[0]
	if err := validateID(id); err != nil {
		return a.fail(err)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	if findAccount(accounts, id) != nil {
		return a.fail(fmt.Errorf("account %s already exists; change it with `doppel edit %s`", id, id))
	}

	acc := &Account{ID: id, Name: f.name, Email: f.email, GitHubUser: f.githubUser, Hosts: f.hosts}
	if len(acc.Hosts) == 0 {
		acc.Hosts = []string{defaultHost}
	}
	if err := acc.validate(); err != nil {
		return a.fail(err)
	}
	if err := a.bindFolders(accounts, acc, f.folders, w); err != nil {
		return a.fail(err)
	}
	accounts = append(accounts, acc)
	if len(accounts) == 1 || f.makeDefault {
		setDefault(accounts, acc)
	}
	return a.save(accounts, w, fmt.Sprintf("Added account %s", id))
}

const editUsage = `doppel edit <id> [--name <name>] [--email <email>] [--host <host>]... [--github-user <user>] [--folder <folder>]... [--default[=false]] [--dry-run] [--yes]
  --host and --folder replace the account's current list.`

func (a *app) cmdEdit(args []string) int {
	fs := newFlagSet("edit")
	var f accountFlags
	var w writeFlags
	f.register(fs)
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, editUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return a.usageError(editUsage)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := findAccount(accounts, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}

	changed := false
	if flagWasSet(fs, "name") {
		acc.Name, changed = f.name, true
	}
	if flagWasSet(fs, "email") {
		acc.Email, changed = f.email, true
	}
	if flagWasSet(fs, "github-user") {
		acc.GitHubUser, changed = f.githubUser, true
	}
	if flagWasSet(fs, "host") {
		acc.Hosts, changed = f.hosts, true
	}
	if flagWasSet(fs, "folder") {
		acc.Folders = nil
		if err := a.bindFolders(accounts, acc, f.folders, w); err != nil {
			return a.fail(err)
		}
		changed = true
	}
	if flagWasSet(fs, "default") {
		if f.makeDefault {
			setDefault(accounts, acc)
		} else {
			acc.Default = false
		}
		changed = true
	}
	if !changed {
		return a.usageError(editUsage)
	}
	return a.save(accounts, w, fmt.Sprintf("Updated account %s", acc.ID))
}

const rmUsage = "doppel rm <id> [--dry-run] [--yes]"

func (a *app) cmdRm(args []string) int {
	fs := newFlagSet("rm")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, rmUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return a.usageError(rmUsage)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := findAccount(accounts, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	question := fmt.Sprintf("Delete account %s and its folder rules? Key files are kept.", acc.ID)
	if err := a.confirm(question, w.assumeYes()); err != nil {
		return a.fail(err)
	}

	var rest []*Account
	for _, other := range accounts {
		if other != acc {
			rest = append(rest, other)
		}
	}
	code = a.save(rest, w, fmt.Sprintf("Deleted account %s", acc.ID))
	if code == 0 && acc.Default && len(rest) > 0 && !w.dryRun {
		a.notef("There's no default account now. Choose one with: doppel default <id>")
	}
	return code
}

const renameUsage = "doppel rename <id> <new-id> [--dry-run]"

func (a *app) cmdRename(args []string) int {
	fs := newFlagSet("rename")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, renameUsage)
	if !ok {
		return code
	}
	if len(positional) != 2 {
		return a.usageError(renameUsage)
	}
	oldID, newID := positional[0], positional[1]
	if err := validateID(newID); err != nil {
		return a.fail(err)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := findAccount(accounts, oldID)
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", oldID))
	}
	if findAccount(accounts, newID) != nil {
		return a.fail(fmt.Errorf("account %s already exists", newID))
	}
	acc.ID = newID
	return a.save(accounts, w, fmt.Sprintf("Renamed account %s to %s", oldID, newID))
}

const bindUsage = "doppel bind <id> <folder>... [--dry-run] [--yes]"

func (a *app) cmdBind(args []string) int {
	fs := newFlagSet("bind")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, bindUsage)
	if !ok {
		return code
	}
	if len(positional) < 2 {
		return a.usageError(bindUsage)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := findAccount(accounts, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	if err := a.bindFolders(accounts, acc, positional[1:], w); err != nil {
		return a.fail(err)
	}
	return a.save(accounts, w, fmt.Sprintf("Bound to %s: %s", acc.ID, strings.Join(acc.Folders, ", ")))
}

const unbindUsage = "doppel unbind <folder>... [--dry-run]"

func (a *app) cmdUnbind(args []string) int {
	fs := newFlagSet("unbind")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, unbindUsage)
	if !ok {
		return code
	}
	if len(positional) == 0 {
		return a.usageError(unbindUsage)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}
	var removed []string
	for _, input := range positional {
		folder, _, err := a.env.NormalizeFolder(input, a.cwd)
		if err != nil {
			return a.fail(err)
		}
		owner := folderOwner(a.env, accounts, folder)
		if owner == nil {
			return a.fail(fmt.Errorf("%s isn't bound to any account", folder))
		}
		owner.removeFolder(a.env, folder)
		removed = append(removed, fmt.Sprintf("%s (was %s)", folder, owner.ID))
	}
	return a.save(accounts, w, "Unbound "+strings.Join(removed, ", "))
}

const defaultUsage = "doppel default [<id> | --none] [--dry-run]"

func (a *app) cmdDefault(args []string) int {
	fs := newFlagSet("default")
	var w writeFlags
	var none bool
	w.register(fs)
	fs.BoolVar(&none, "none", false, "leave no default account")
	positional, code, ok := a.parseCommand(fs, args, defaultUsage)
	if !ok {
		return code
	}
	if len(positional) > 1 || (none && len(positional) > 0) {
		return a.usageError(defaultUsage)
	}
	accounts, err := loadAccounts(a.env)
	if err != nil {
		return a.fail(err)
	}

	if len(positional) == 0 && !none {
		if def := defaultAccount(accounts); def != nil {
			a.printf("%s\n", def.ID)
		} else {
			a.notef("No default account: repos outside every folder use your global Git config.")
		}
		return 0
	}
	if none {
		setDefault(accounts, nil)
		return a.save(accounts, w, "Cleared the default account")
	}
	acc := findAccount(accounts, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	setDefault(accounts, acc)
	return a.save(accounts, w, fmt.Sprintf("%s is now the default account", acc.ID))
}

const uninstallUsage = "doppel uninstall [--dry-run] [--yes]"

func (a *app) cmdUninstall(args []string) int {
	fs := newFlagSet("uninstall")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, uninstallUsage)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		return a.usageError(uninstallUsage)
	}
	global := a.env.Shorten(a.env.GlobalConfigPath())
	question := fmt.Sprintf("Remove doppel's include from %s? Git will stop using doppel's accounts; account files and keys are kept.", global)
	if err := a.confirm(question, w.assumeYes()); err != nil {
		return a.fail(err)
	}
	plan, err := newPlan()
	if err != nil {
		return a.fail(err)
	}
	defer plan.Close()
	removed, err := removeInclude(a.env, plan)
	if err != nil {
		return a.fail(err)
	}
	if !removed {
		a.notef("%s doesn't include doppel's accounts; nothing to remove.", global)
		return 0
	}
	code = a.finish(plan, w, "Removed doppel's include from "+global)
	if code == 0 && !w.dryRun {
		a.notef("Account files are still in %s. Any doppel command that changes accounts adds the include back.",
			a.env.Shorten(a.env.AccountsDir()))
	}
	return code
}
