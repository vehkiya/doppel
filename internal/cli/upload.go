package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/github"
	"github.com/vehkiya/doppel/internal/keys"
)

const uploadUsage = "doppel upload <id> [--auth | --signing]"

// cmdUpload adds an account's keys to its user on each GitHub host it uses,
// through gh. Without gh, or when gh isn't signed in as that user, it shows
// how to add them by hand instead.
func (a *app) cmdUpload(args []string) int {
	fs := newFlagSet("upload")
	var onlyAuth, onlySigning bool
	fs.BoolVar(&onlyAuth, "auth", false, "only the auth key")
	fs.BoolVar(&onlySigning, "signing", false, "only the signing key")
	positional, code, ok := a.parseCommand(fs, args, uploadUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 || (onlyAuth && onlySigning) {
		return a.usageError(uploadUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := accounts.Find(list, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	exported, err := a.keysToExport(acc, onlyAuth, onlySigning)
	if err != nil {
		return a.fail(err)
	}
	exportArgs := []string{acc.ID}
	if onlyAuth {
		exportArgs = append(exportArgs, "--auth")
	}
	if onlySigning {
		exportArgs = append(exportArgs, "--signing")
	}

	apiHosts := githubHosts(acc)
	if len(apiHosts) == 0 {
		return a.fail(fmt.Errorf("none of account %s's hosts (%s) is GitHub as far as doppel can tell. If one runs GitHub Enterprise Server, sign gh in to it with `gh auth login -h <host>` and run this again; for other hosts, add the keys by hand with `doppel export %s`",
			acc.ID, strings.Join(acc.Hosts, ", "), acc.ID))
	}
	if acc.GitHubUser == "" {
		return a.fail(fmt.Errorf("doppel needs to know account %s's GitHub user: doppel edit %s --github-user <user>", acc.ID, acc.ID))
	}
	if !github.Available() {
		a.warnf("gh isn't installed, so here's how to add the keys by hand.\n")
		return a.cmdExport(exportArgs)
	}

	spelling := ""
	for _, apiHost := range apiHosts {
		code, fallBack := a.uploadTo(apiHost, acc, exported, &spelling)
		if fallBack {
			return a.cmdExport(exportArgs)
		}
		if code != 0 {
			return code
		}
	}
	if spelling != "" {
		a.offerGitHubUser(list, acc, spelling)
	}
	return 0
}

// offerGitHubUser offers to store the GitHub user the way GitHub and gh
// spell it, when the account has it differently in capitals. gh matches the
// name exactly, so the other spelling only works because doppel looked it up.
func (a *app) offerGitHubUser(list []*accounts.Account, acc *accounts.Account, login string) {
	if !a.interactive {
		a.notef("GitHub spells the user %s, not %s. Correct it with: doppel edit %s --github-user %s", login, acc.GitHubUser, acc.ID, login)
		return
	}
	question := fmt.Sprintf("GitHub spells the user %s, not %s. Correct account %s?", login, acc.GitHubUser, acc.ID)
	if err := a.confirm(question, false); err != nil {
		return // declined, or the question was cancelled; the upload itself worked
	}
	acc.GitHubUser = login
	a.save(list, writeFlags{}, fmt.Sprintf("Updated account %s", acc.ID))
}

// githubHosts lists the hosts gh talks to for the account's GitHub hosts.
// github.com and ssh.github.com share one.
func githubHosts(acc *accounts.Account) []string {
	var apiHosts []string
	for _, h := range acc.Hosts {
		if api, ok := github.APIHost(h); ok && !slices.Contains(apiHosts, api) {
			apiHosts = append(apiHosts, api)
		}
	}
	return apiHosts
}

// uploadTo adds keys to the account's user on one GitHub host. fallBack is
// true when gh isn't signed in as that user, so manual steps should follow.
// When gh spells the user differently in capitals than the account does,
// that spelling is left in *spelling.
func (a *app) uploadTo(apiHost string, acc *accounts.Account, exported []exportedKey, spelling *string) (code int, fallBack bool) {
	client, err := github.ForUser(apiHost, acc.GitHubUser)
	if err != nil {
		var notSignedIn *github.NotSignedInError
		if errors.As(err, &notSignedIn) {
			a.warnf("%v. Sign in with `gh auth login -h %s` and pick that account, then run this again. Meanwhile, here's how to add the keys by hand.\n", notSignedIn, apiHost)
			return 0, true
		}
		return a.fail(err), false
	}
	if client.User != acc.GitHubUser {
		*spelling = client.User
	}
	login, granted, err := client.Info()
	if err != nil {
		return a.fail(err), false
	}
	if !strings.EqualFold(login, client.User) {
		return a.fail(fmt.Errorf("gh's token for %s on %s belongs to %s", client.User, apiHost, login)), false
	}

	var kinds []github.Kind
	for _, k := range exported {
		kinds = append(kinds, kindsOf(k)...)
	}
	if missing := github.MissingScopes(granted, kinds...); len(missing) > 0 {
		return a.fail(fmt.Errorf("gh's token for %s on %s can't add SSH keys yet. Give it the %s scope with:\n  %s",
			client.User, apiHost, strings.Join(missing, " and "), refreshCommand(apiHost, client.User, missing))), false
	}

	title := keyTitle(acc)
	where := client.User
	if apiHost != github.DotCom {
		where += " on " + apiHost
	}
	existing := map[github.Kind][]string{}
	for _, k := range exported {
		pub, err := keys.ReadPublic(k.key)
		if err != nil {
			return a.fail(fmt.Errorf("%s: %w", a.env.Shorten(keys.PublicPath(k.key)), err)), false
		}
		short := a.env.Shorten(keys.PublicPath(k.key))
		for _, kind := range kindsOf(k) {
			if _, ok := existing[kind]; !ok {
				if existing[kind], err = client.Keys(kind); err != nil {
					return a.fail(err), false
				}
			}
			if slices.Contains(existing[kind], pub) {
				a.successf("%s is already one of %s's %s keys", short, where, kind)
				continue
			}
			if err := client.Add(kind, keys.PublicPath(k.key), title); err != nil {
				return a.fail(err), false
			}
			a.successf("Added %s to %s's %s keys", short, where, kind)
		}
	}
	return 0, false
}

func kindsOf(k exportedKey) []github.Kind {
	var kinds []github.Kind
	if k.auth {
		kinds = append(kinds, github.Authentication)
	}
	if k.signing {
		kinds = append(kinds, github.Signing)
	}
	return kinds
}

// refreshCommand is how to add scopes to user's gh token on apiHost. gh
// only refreshes its active account, so for another account it switches
// there and back.
func refreshCommand(apiHost, user string, scopes []string) string {
	refresh := "gh auth refresh -h " + apiHost + " -s " + strings.Join(scopes, ",")
	active, err := github.ActiveUser(apiHost)
	if err == nil && strings.EqualFold(active, user) {
		return refresh
	}
	back := "<your usual account>"
	if err == nil && active != "" {
		back = active
	}
	return fmt.Sprintf("gh auth switch -h %s -u %s && %s && gh auth switch -h %s -u %s", apiHost, user, refresh, apiHost, back)
}
