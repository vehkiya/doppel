package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/ui"
)

const whoamiUsage = "doppel whoami [path] [--offline] [--json]"

// whoamiJSON is the machine-readable representation of identity for `doppel whoami --json`.
type whoamiJSON struct {
	InRepo           bool             `json:"in_repo"`
	Repo             string           `json:"repo"`
	Path             string           `json:"path"`
	Exists           bool             `json:"exists"`
	Account          string           `json:"account"`
	Rule             string           `json:"rule"`
	Protocol         string           `json:"protocol,omitempty"`
	HTTPSUser        string           `json:"https_user,omitempty"`
	CredentialHelper string           `json:"credential_helper,omitempty"`
	Name             string           `json:"name"`
	Email            string           `json:"email"`
	AuthKey          string           `json:"auth_key"`
	SSHCommand       string           `json:"ssh_command"`
	SigningKey       string           `json:"signing_key"`
	SignCommits      bool             `json:"sign_commits"`
	SignTags         bool             `json:"sign_tags"`
	Overrides        []string         `json:"overrides"`
	NewRepoAccount   string           `json:"new_repo_account"`
	NewRepoRule      string           `json:"new_repo_rule"`
	Login            *whoamiLoginJSON `json:"login"`
	StaleIndex       bool             `json:"stale_index"`
}

type whoamiLoginJSON struct {
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix"`
}

func (a *app) cmdWhoami(args []string) int {
	fs := newFlagSet("whoami")
	var offline bool
	var asJSON bool
	fs.BoolVar(&offline, "offline", false, "don't try logging in to the repo's host")
	fs.BoolVar(&asJSON, "json", false, "output identity as JSON")
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

	if asJSON {
		var loginJSON *whoamiLoginJSON
		if id.Account != nil && !offline {
			if login, ok := ops.RemoteLogin(ctx, id.Account, id.Path); ok {
				loginJSON = &whoamiLoginJSON{
					OK:      login.OK,
					Skipped: login.Skipped,
					Label:   login.Label,
					Detail:  login.Detail,
					Fix:     login.Fix,
				}
			}
		}
		overrides := id.Overrides
		if overrides == nil {
			overrides = []string{}
		}
		var authKey string
		if id.UsesAccountKey && id.Account != nil {
			authKey = id.Account.AuthKey.String()
		}
		proto := id.Protocol
		if proto == "" && id.Account != nil {
			proto = string(id.Account.Protocol)
			if proto == "" {
				proto = string(accounts.ProtocolSSH)
			}
		}
		return a.printJSON(whoamiJSON{
			InRepo:           id.InRepo,
			Repo:             id.Repo,
			Path:             id.Path,
			Exists:           id.Exists,
			Account:          id.AccountID,
			Rule:             id.Rule,
			Protocol:         proto,
			HTTPSUser:        id.HTTPSUser,
			CredentialHelper: id.CredentialHelper,
			Name:             id.Name,
			Email:            id.Email,
			AuthKey:          authKey,
			SSHCommand:       id.SSHCommand,
			SigningKey:       id.SigningKey,
			SignCommits:      id.SignCommits,
			SignTags:         id.SignTags,
			Overrides:        overrides,
			NewRepoAccount:   id.NewRepoID,
			NewRepoRule:      id.NewRepoRule,
			Login:            loginJSON,
			StaleIndex:       id.StaleIndex,
		})
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
		if id.StaleIndex {
			a.printf("\n")
			a.warnf("doppel's index is out of date; run `doppel doctor --fix`")
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
	if id.Account != nil && id.Account.Protocol == accounts.ProtocolHTTPS {
		authDesc := "HTTPS"
		if u := id.Account.EffectiveHTTPSUser(); u != "" {
			authDesc += " (" + u + ")"
		}
		a.row("Auth", authDesc)
	} else {
		switch {
		case id.UsesAccountKey:
			key := id.Account.AuthKey
			a.row("Auth key", key.Display()+ui.Dim.Render(" ("+strings.Join(ops.KeyStatus(ctx, key), ", ")+")"))
		case id.SSHCommand == "ssh" || id.SSHCommand == "":
			a.row("Auth key", "your default SSH keys")
		default:
			a.row("SSH", id.SSHCommand)
		}
		if id.Account != nil && id.Account.Protocol == accounts.ProtocolBoth {
			if u := id.Account.EffectiveHTTPSUser(); u != "" {
				a.row("HTTPS user", u)
			}
		}
	}
	if id.Account != nil && id.Account.CredentialHelper != "" {
		a.row("Helper", id.Account.CredentialHelper)
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
					prefix := "↳ switch it to SSH: "
					if id.Account.Protocol == accounts.ProtocolHTTPS {
						prefix = "↳ switch it to HTTPS: "
					}
					a.row("", ui.Dim.Render(prefix)+login.Fix)
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
		if id.StaleIndex {
			a.warnf("doppel's index is out of date; run `doppel doctor --fix`")
		}
		return 0
	}
	if len(id.Overrides) > 0 {
		a.printf("\n")
		for _, line := range id.Overrides {
			a.warnf("%s", line)
		}
	}
	if id.StaleIndex {
		if len(id.Overrides) == 0 {
			a.printf("\n")
		}
		a.warnf("doppel's index is out of date; run `doppel doctor --fix`")
	}
	return 0
}

func (a *app) row(label, value string) {
	a.printf("%s %s\n", ui.Label.Render(fmt.Sprintf("%-9s", label)), value)
}
