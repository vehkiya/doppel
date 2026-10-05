package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ui"
)

const lsUsage = "doppel ls"

func (a *app) cmdLs(args []string) int {
	if _, code, ok := a.parseCommand(newFlagSet("ls"), args, lsUsage); !ok {
		return code
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	if len(list) == 0 {
		a.printf("No accounts yet. Add one with:\n  doppel add <id> --name \"Your Name\" --email you@example.com\n")
		return 0
	}

	rows := [][]string{{"", "ACCOUNT", "EMAIL", "FOLDERS"}}
	for _, acc := range list {
		marker := ""
		if acc.Default {
			marker = ui.Star.Render("★")
		}
		folders := "—"
		if len(acc.Folders) > 0 {
			folders = strings.Join(acc.Folders, ", ")
		}
		rows = append(rows, []string{marker, ui.Accent.Render(acc.ID), acc.Email, folders})
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
				cell = ui.Label.Render(cell)
			}
			line.WriteString(cell)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(cell)+2))
			}
		}
		a.printf("%s\n", strings.TrimRight(line.String(), " "))
	}
	a.printf("\n")
	if accounts.Default(list) != nil {
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
	if err := accounts.ValidateID(id); err != nil {
		return a.fail(err)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	if accounts.Find(list, id) != nil {
		return a.fail(fmt.Errorf("account %s already exists; change it with `doppel edit %s`", id, id))
	}

	acc := &accounts.Account{ID: id, Name: f.name, Email: f.email, GitHubUser: f.githubUser, Hosts: f.hosts}
	if len(acc.Hosts) == 0 {
		acc.Hosts = []string{accounts.DefaultHost}
	}
	if err := acc.Validate(); err != nil {
		return a.fail(err)
	}
	if err := a.bindFolders(list, acc, f.folders, w); err != nil {
		return a.fail(err)
	}
	list = append(list, acc)
	if len(list) == 1 || f.makeDefault {
		accounts.SetDefault(list, acc)
	}
	return a.save(list, w, fmt.Sprintf("Added account %s", id))
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
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := accounts.Find(list, positional[0])
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
		if err := a.bindFolders(list, acc, f.folders, w); err != nil {
			return a.fail(err)
		}
		changed = true
	}
	if flagWasSet(fs, "default") {
		if f.makeDefault {
			accounts.SetDefault(list, acc)
		} else {
			acc.Default = false
		}
		changed = true
	}
	if !changed {
		return a.usageError(editUsage)
	}
	return a.save(list, w, fmt.Sprintf("Updated account %s", acc.ID))
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
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := accounts.Find(list, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	question := fmt.Sprintf("Delete account %s and its folder rules? Key files are kept.", acc.ID)
	if err := a.confirm(question, w.assumeYes()); err != nil {
		return a.fail(err)
	}

	var rest []*accounts.Account
	for _, other := range list {
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
	if err := accounts.ValidateID(newID); err != nil {
		return a.fail(err)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := accounts.Find(list, oldID)
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", oldID))
	}
	if accounts.Find(list, newID) != nil {
		return a.fail(fmt.Errorf("account %s already exists", newID))
	}
	acc.ID = newID
	return a.save(list, w, fmt.Sprintf("Renamed account %s to %s", oldID, newID))
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
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}

	if len(positional) == 0 && !none {
		if def := accounts.Default(list); def != nil {
			a.printf("%s\n", def.ID)
		} else {
			a.notef("No default account: repos outside every folder use your global Git config.")
		}
		return 0
	}
	if none {
		accounts.SetDefault(list, nil)
		return a.save(list, w, "Cleared the default account")
	}
	acc := accounts.Find(list, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	accounts.SetDefault(list, acc)
	return a.save(list, w, fmt.Sprintf("%s is now the default account", acc.ID))
}
