package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/ui"
)

const whoamiUsage = "doppel whoami [path] [--offline]"

func (a *app) cmdWhoami(args []string) int {
	fs := newFlagSet("whoami")
	var offline bool
	fs.BoolVar(&offline, "offline", false, "don't try logging in to the repo's host")
	positional, code, ok := a.parseCommand(fs, args, whoamiUsage)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		return a.usageError(whoamiUsage)
	}
	path := a.cwd
	if len(positional) == 1 {
		path = a.env.Expand(positional[0])
		if !filepath.IsAbs(path) {
			path = filepath.Join(a.cwd, path)
		}
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	ctx := a.opsContext(writeFlags{})
	id, err := ops.Whoami(ctx, list, path)
	if err != nil {
		return a.fail(err)
	}
	if !id.InRepo {
		note := " (not a Git repository)"
		if !id.Exists {
			note = " (doesn't exist yet)"
		}
		a.row("Path", a.env.Shorten(id.Path)+ui.Dim.Render(note))
		if id.NewRepoID != "" {
			a.row("New repos", ui.Accent.Render(id.NewRepoID)+ui.Dim.Render(" ("+id.NewRepoRule+")"))
		} else {
			a.row("New repos", ui.Warn.Render("no doppel account")+ui.Dim.Render(" (your global Git config applies)"))
		}
		return 0
	}

	a.row("Repo", a.env.Shorten(id.Repo))
	switch {
	case id.AccountID == "":
		a.row("Account", ui.Warn.Render("none"))
	case id.Account == nil:
		a.row("Account", ui.Warn.Render(id.AccountID+" (no such account file)"))
	default:
		a.row("Account", ui.Accent.Render(id.Account.ID)+ui.Dim.Render(" ("+id.Rule+")"))
	}
	a.row("Name", id.Name)
	a.row("Email", id.Email)
	switch {
	case id.UsesAccountKey:
		key := id.Account.AuthKey
		a.row("Auth key", key.Display()+ui.Dim.Render(" ("+strings.Join(ops.KeyStatus(ctx, key), ", ")+")"))
	case id.SSHCommand == "ssh" || id.SSHCommand == "":
		a.row("Auth key", "your default SSH keys")
	default:
		a.row("SSH", id.SSHCommand)
	}
	if id.SignCommits || id.SignTags {
		a.row("Signing", fmt.Sprintf("%s · %s", accounts.SigningScope(id.SignCommits, id.SignTags), id.SigningKey))
	} else {
		a.row("Signing", "off")
	}

	if id.Account != nil && !offline {
		if login, ok := ops.RemoteLogin(ctx, id.Account, id.Path); ok {
			switch {
			case login.Skipped:
				a.row("Login", ui.Dim.Render(login.Detail))
				if login.Fix != "" {
					a.row("", ui.Dim.Render("↳ switch it to SSH: ")+login.Fix)
				}
			case login.OK:
				a.row("Login", ui.OK.Render("✓")+" "+login.Label+": "+login.Detail)
			default:
				a.row("Login", ui.Error.Render("✗")+" "+login.Label+": "+login.Detail+ui.Dim.Render(" (try `doppel test "+id.Account.ID+"`)"))
			}
		}
	}
	if id.NewRepoID != "" {
		a.row("New repos", ui.Accent.Render(id.NewRepoID)+ui.Dim.Render(" ("+id.NewRepoRule+")"))
	}

	if id.AccountID == "" {
		a.printf("\n")
		if accounts.Default(list) == nil {
			a.notef("No doppel account applies here. Bind this repo's folder with `doppel bind <id> <folder>`, or set a default with `doppel default <id>`.")
		}
		return 0
	}
	if len(id.Overrides) > 0 {
		a.printf("\n")
		for _, line := range id.Overrides {
			a.warnf("%s", line)
		}
	}
	return 0
}

func (a *app) row(label, value string) {
	a.printf("%s %s\n", ui.Label.Render(fmt.Sprintf("%-9s", label)), value)
}
