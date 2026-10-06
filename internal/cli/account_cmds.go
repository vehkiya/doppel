package cli

import (
	"flag"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
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
		a.printf("No accounts yet. Add one with `doppel add`, which asks for each setting, or for scripts:\n  doppel add <id> --name \"Your Name\" --email you@example.com\n")
		return 0
	}

	rows := [][]string{{"", "ACCOUNT", "EMAIL", "KEYS", "FOLDERS"}}
	for _, acc := range list {
		marker := ""
		if acc.Default {
			marker = ui.Star.Render("★")
		}
		folders := "—"
		if len(acc.Folders) > 0 {
			folders = strings.Join(acc.Folders, ", ")
		}
		rows = append(rows, []string{marker, ui.Accent.Render(acc.ID), acc.Email, keySummary(acc), folders})
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

const addUsage = `doppel add <id> --name <name> --email <email> [--host <host>]... [--github-user <user>] [--folder <folder>]... [--default] [--dry-run] [--yes]
  Keys: [--auth-key <key> | --generate-auth-key]
        [--signing-key <key> | --generate-signing-key | --sign-with-auth-key | --no-signing]
        [--sign-commits=false] [--sign-tags=false]`

func (a *app) cmdAdd(args []string) int {
	fs := newFlagSet("add")
	var f accountFlags
	var k keyFlags
	var w writeFlags
	f.register(fs)
	k.register(fs)
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, addUsage)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		return a.usageError(addUsage)
	}
	if a.interactive && onlyWriteFlags(fs) {
		id := ""
		if len(positional) == 1 {
			id = positional[0]
		}
		return a.exitStatus(a.addWithWizard(id, w))
	}
	if len(positional) != 1 {
		return a.usageError(addUsage)
	}
	keyChanges, err := keyChangesFromFlags(fs, k)
	if err != nil {
		return a.fail(err)
	}
	req := ops.AddRequest{
		Account: &accounts.Account{ID: positional[0], Name: f.name, Email: f.email, GitHubUser: f.githubUser, Hosts: f.hosts},
		Folders: f.folders, Default: f.makeDefault, Keys: keyChanges,
	}
	return a.exitStatus(a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Add(ctx, list, req)
	}))
}

const editUsage = `doppel edit <id> [--name <name>] [--email <email>] [--host <host>]... [--github-user <user>] [--folder <folder>]... [--default[=false]] [--dry-run] [--yes]
  Keys: [--auth-key <key> | --generate-auth-key]
        [--signing-key <key> | --generate-signing-key | --sign-with-auth-key | --no-signing]
        [--sign-commits=false] [--sign-tags=false]
  --host and --folder replace the account's current list; --auth-key "" goes back to ssh's own keys.`

func (a *app) cmdEdit(args []string) int {
	fs := newFlagSet("edit")
	var f accountFlags
	var k keyFlags
	var w writeFlags
	f.register(fs)
	k.register(fs)
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, editUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return a.usageError(editUsage)
	}
	id := positional[0]
	if a.interactive && onlyWriteFlags(fs) {
		return a.exitStatus(a.editWithWizard(id, w))
	}
	req, err := editRequestFromFlags(fs, id, f, k)
	if err != nil {
		return a.fail(err)
	}
	if req.Empty() {
		return a.usageError(editUsage)
	}
	return a.exitStatus(a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Edit(ctx, list, req)
	}))
}

// editRequestFromFlags reads the changes edit's flags ask for. A flag that
// isn't given keeps its setting.
func editRequestFromFlags(fs *flag.FlagSet, id string, f accountFlags, k keyFlags) (ops.EditRequest, error) {
	req := ops.EditRequest{ID: id}
	if flagWasSet(fs, "name") {
		req.Name = &f.name
	}
	if flagWasSet(fs, "email") {
		req.Email = &f.email
	}
	if flagWasSet(fs, "github-user") {
		req.GitHubUser = &f.githubUser
	}
	if flagWasSet(fs, "host") {
		req.Hosts = f.hosts
	}
	if flagWasSet(fs, "folder") {
		folders := []string(f.folders)
		req.Folders = &folders
	}
	if flagWasSet(fs, "default") {
		req.Default = &f.makeDefault
	}
	var err error
	req.Keys, err = keyChangesFromFlags(fs, k)
	return req, err
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
	return a.exitStatus(a.remove(positional[0], w))
}

// remove deletes an account. In a terminal it asks which account becomes
// the default when the default one goes.
func (a *app) remove(id string, w writeFlags) (*ops.Result, error) {
	req := ops.RemoveRequest{ID: id}
	if a.interactive {
		req.NewDefault = a.chooseNewDefault
	}
	return a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Remove(ctx, list, req)
	})
}

// chooseNewDefault asks which account takes over as the default when the
// default one is deleted.
func (a *app) chooseNewDefault(rest []*accounts.Account) (*accounts.Account, error) {
	options := []huh.Option[string]{}
	for _, other := range rest {
		options = append(options, huh.NewOption(other.ID+" ("+other.Email+")", other.ID))
	}
	options = append(options, huh.NewOption("No default account", ""))
	choice := rest[0].ID
	if err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Which account should be the default now?").
			Description("The default account is used for repos outside every folder").
			Options(options...).Value(&choice),
	))); err != nil {
		return nil, err
	}
	return accounts.Find(rest, choice), nil
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
	return a.exitStatus(a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Rename(ctx, list, oldID, newID)
	}))
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
	if len(positional) == 0 && !none {
		list, err := accounts.Load(a.env)
		if err != nil {
			return a.fail(err)
		}
		if def := accounts.Default(list); def != nil {
			a.printf("%s\n", def.ID)
		} else {
			a.notef("No default account: repos outside every folder use your global Git config.")
		}
		return 0
	}
	id := ""
	if !none {
		id = positional[0]
	}
	return a.exitStatus(a.setDefault(id, w))
}

// setDefault makes the account named id the default, or with "" leaves none.
func (a *app) setDefault(id string, w writeFlags) (*ops.Result, error) {
	return a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.SetDefault(ctx, list, id)
	})
}
