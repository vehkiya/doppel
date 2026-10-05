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

// cmdUpload adds an account's keys to its GitHub user through gh. Without
// gh, or when gh isn't signed in as that user, it shows how to add them by
// hand instead.
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
	if !slices.ContainsFunc(acc.Hosts, func(h string) bool { return strings.EqualFold(h, github.Host) }) {
		return a.fail(fmt.Errorf("account %s doesn't use %s; add its keys by hand with `doppel export %s`", acc.ID, github.Host, acc.ID))
	}
	if acc.GitHubUser == "" {
		return a.fail(fmt.Errorf("doppel needs to know account %s's GitHub user: doppel edit %s --github-user <user>", acc.ID, acc.ID))
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
	if !github.Available() {
		a.warnf("gh isn't installed, so here's how to add the keys by hand.\n")
		return a.cmdExport(exportArgs)
	}
	client, err := github.ForUser(acc.GitHubUser)
	if err != nil {
		var notSignedIn *github.NotSignedInError
		if errors.As(err, &notSignedIn) {
			a.warnf("gh isn't signed in to GitHub as %s. Sign in with `gh auth login -h github.com` and pick that account, then run this again. Meanwhile, here's how to add the keys by hand.\n", acc.GitHubUser)
			return a.cmdExport(exportArgs)
		}
		return a.fail(err)
	}
	login, granted, err := client.Info()
	if err != nil {
		return a.fail(err)
	}
	if !strings.EqualFold(login, acc.GitHubUser) {
		return a.fail(fmt.Errorf("gh's token for %s belongs to %s", acc.GitHubUser, login))
	}

	var kinds []github.Kind
	for _, k := range exported {
		kinds = append(kinds, kindsOf(k)...)
	}
	if missing := github.MissingScopes(granted, kinds...); len(missing) > 0 {
		return a.fail(fmt.Errorf("gh's token for %s can't add SSH keys yet. Give it the %s scope with:\n  %s",
			acc.GitHubUser, strings.Join(missing, " and "), refreshCommand(acc.GitHubUser, missing)))
	}

	title := keyTitle(acc)
	existing := map[github.Kind][]string{}
	for _, k := range exported {
		pub, err := keys.ReadPublic(k.key)
		if err != nil {
			return a.fail(fmt.Errorf("%s: %w", a.env.Shorten(keys.PublicPath(k.key)), err))
		}
		for _, kind := range kindsOf(k) {
			if _, ok := existing[kind]; !ok {
				if existing[kind], err = client.Keys(kind); err != nil {
					return a.fail(err)
				}
			}
			short := a.env.Shorten(keys.PublicPath(k.key))
			if slices.Contains(existing[kind], pub) {
				a.successf("%s is already one of %s's %s keys", short, acc.GitHubUser, kind)
				continue
			}
			if err := client.Add(kind, keys.PublicPath(k.key), title); err != nil {
				return a.fail(err)
			}
			a.successf("Added %s to %s's %s keys on GitHub", short, acc.GitHubUser, kind)
		}
	}
	return 0
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

// refreshCommand is how to add scopes to user's gh token. gh only refreshes
// its active account, so for another account it switches there and back.
func refreshCommand(user string, scopes []string) string {
	refresh := "gh auth refresh -h github.com -s " + strings.Join(scopes, ",")
	active, err := github.ActiveUser()
	if err == nil && strings.EqualFold(active, user) {
		return refresh
	}
	back := "<your usual account>"
	if err == nil && active != "" {
		back = active
	}
	return fmt.Sprintf("gh auth switch -h github.com -u %s && %s && gh auth switch -h github.com -u %s", user, refresh, back)
}
