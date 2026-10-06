package cli

import (
	"fmt"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/github"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/store"
	"github.com/vehkiya/doppel/internal/ui"
)

const testUsage = "doppel test [<id>]"

// cmdTest tries each account's keys for real: it logs in to every host and
// signs a test message, then verifies it as Git would.
func (a *app) cmdTest(args []string) int {
	positional, code, ok := a.parseCommand(newFlagSet("test"), args, testUsage)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		return a.usageError(testUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	if len(positional) == 1 {
		acc := accounts.Find(list, positional[0])
		if acc == nil {
			return a.fail(fmt.Errorf("no account named %s", positional[0]))
		}
		list = []*accounts.Account{acc}
	}
	if len(list) == 0 {
		a.printf("No accounts to test.\n")
		return 0
	}
	signers, _, err := store.SignersFile(a.env)
	if err != nil {
		return a.fail(err)
	}

	failed := false
	for i, acc := range list {
		if i > 0 {
			a.printf("\n")
		}
		a.printf("%s\n", ui.Accent.Render(acc.ID))
		for _, host := range acc.Hosts {
			ok, detail := a.checkLogin(acc, host, !a.interactive)
			a.checkRow(ok, host, detail)
			failed = failed || !ok
		}
		if acc.SigningKey == "" {
			a.printf("  %s %-12s %s\n", ui.Dim.Render("–"), "signing", ui.Dim.Render("off"))
		} else if reason := a.needsPassphrase(acc.SigningKey, !a.interactive); reason != "" {
			a.checkRow(false, "signing", reason)
			failed = true
		} else if err := keys.SignCheck(a.env.Expand(acc.SigningKey), acc.Email, signers); err != nil {
			a.checkRow(false, "signing", err.Error())
			failed = true
		} else {
			a.checkRow(true, "signing", "signed and verified as "+acc.Email)
		}
		// Without a terminal, the rows above already name the command.
		if a.interactive {
			a.keychainHints(acc.AuthKey, acc.SigningKey)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// checkLogin logs in to host with acc's auth key and says how it went. On
// GitHub it also checks the key logs in as the account's GitHub user.
func (a *app) checkLogin(acc *accounts.Account, host string, batch bool) (bool, string) {
	if reason := a.needsPassphrase(acc.AuthKey, batch); reason != "" {
		return false, reason
	}
	res := keys.Login(host, a.env.Expand(acc.AuthKey), batch)
	switch {
	case !res.Accepted:
		return false, res.Problem
	case acc.GitHubUser != "" && a.isGitHub(host) && !strings.EqualFold(res.User, acc.GitHubUser):
		return false, fmt.Sprintf("logged in as %s, but the account's GitHub user is %s", res.User, acc.GitHubUser)
	}
	return true, "logged in as " + res.User
}

// needsPassphrase explains why a key can't be used when nothing may ask
// for its passphrase (batch): it has one, and ssh-agent doesn't hold it.
// It returns "" when the key can be used. key may be a private key or the
// .pub next to one, as signing keys are stored.
func (a *app) needsPassphrase(key string, batch bool) string {
	if key == "" || !batch {
		return ""
	}
	path := a.env.Expand(key)
	private := strings.TrimSuffix(path, ".pub")
	if keys.CheckProtection(private) != keys.Encrypted {
		return ""
	}
	if loaded, _ := keys.InAgent(path); loaded {
		return ""
	}
	return "the key has a passphrase and isn't loaded in your agent; load it with `" + a.loadCommand(key) + "`"
}

// isGitHub reports whether host is GitHub (github.com, GHE.com, or a GitHub
// Enterprise Server gh is signed in to).
func (a *app) isGitHub(host string) bool {
	_, ok := a.apiHost(host)
	return ok
}

// apiHost is github.APIHost, asked once per host for the whole command.
func (a *app) apiHost(host string) (string, bool) {
	key := strings.ToLower(host)
	if h, ok := a.githubHosts[key]; ok {
		return h.api, h.ok
	}
	api, ok := github.APIHost(host)
	if a.githubHosts == nil {
		a.githubHosts = map[string]githubHost{}
	}
	a.githubHosts[key] = githubHost{api, ok}
	return api, ok
}

func (a *app) checkRow(ok bool, label, detail string) {
	mark := ui.OK.Render("✓")
	if !ok {
		mark = ui.Error.Render("✗")
	}
	a.printf("  %s %-12s %s\n", mark, label, detail)
}
