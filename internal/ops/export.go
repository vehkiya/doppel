package ops

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/github"
	"github.com/vehkiya/doppel/internal/hosts"
	"github.com/vehkiya/doppel/internal/keys"
)

// ExportKey is a public key to add to an account's hosts, and what it's for.
type ExportKey struct {
	Key  keys.Ref // expanded
	Line string   // the public key as its .pub file holds it
	Use  hosts.Use
}

// Purpose names what the key is for: "Auth key", "Signing key", or both.
func (k ExportKey) Purpose() string {
	switch {
	case k.Use.Auth && k.Use.Signing:
		return "Auth and signing key"
	case k.Use.Auth:
		return "Auth key"
	}
	return "Signing key"
}

// kinds lists how GitHub files the key: one kind for each use.
func (k ExportKey) kinds() []github.Kind {
	var kinds []github.Kind
	if k.Use.Auth {
		kinds = append(kinds, github.Authentication)
	}
	if k.Use.Signing {
		kinds = append(kinds, github.Signing)
	}
	return kinds
}

// PublicName names a key's public half in messages: its .pub file,
// "~/"-shortened, or an inline key.
func PublicName(ctx Context, key keys.Ref) string {
	return key.Public().Map(ctx.Env.Shorten).Display()
}

// ExportKeys picks the public keys to add to acc's hosts and reads them:
// both keys, or with onlyAuth or onlySigning just one. A key used both to
// log in and to sign comes once, for both.
func ExportKeys(ctx Context, acc *accounts.Account, onlyAuth, onlySigning bool) ([]ExportKey, error) {
	auth, signing := acc.AuthKey.Map(ctx.Env.Expand), acc.SigningKey.Map(ctx.Env.Expand)
	switch {
	case onlyAuth && auth == "":
		return nil, fmt.Errorf("account %s has no auth key", acc.ID)
	case onlySigning && signing == "":
		return nil, fmt.Errorf("account %s doesn't sign", acc.ID)
	case auth == "" && signing == "":
		return nil, fmt.Errorf("account %s has no keys yet; add one with `doppel edit %s --generate-auth-key --sign-with-auth-key`", acc.ID, acc.ID)
	}
	same := auth.SameKey(signing)
	var out []ExportKey
	if auth != "" && !onlySigning {
		out = append(out, ExportKey{Key: auth, Use: hosts.Use{Auth: true, Signing: same && !onlyAuth}})
	}
	if signing != "" && !onlyAuth && (!same || len(out) == 0) {
		out = append(out, ExportKey{Key: signing, Use: hosts.Use{Signing: true}})
	}
	for i, k := range out {
		line, err := keys.ReadPublicLine(k.Key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", PublicName(ctx, k.Key), err)
		}
		out[i].Line = line
	}
	return out, nil
}

// KeyTitle names a key on a host's settings page: the account and this machine.
func KeyTitle(acc *accounts.Account) string {
	host, _ := os.Hostname()
	return fmt.Sprintf("doppel: %s (%s)", acc.ID, strings.TrimSuffix(host, ".local"))
}

// Uploaded is a key upload added to a GitHub user, or found already there.
type Uploaded struct {
	Key     string // the key's public file, or the inline key, for messages
	Where   string // the GitHub user, and the host unless it's github.com
	Kind    github.Kind
	Already bool // GitHub already had it
}

// UploadResult is what Upload did.
type UploadResult struct {
	Uploaded []Uploaded
	// ByHand is set when gh can't add the keys, saying why: they have to be
	// added by hand, as export shows.
	ByHand string
	// Spelling is how GitHub spells the account's user, when that differs
	// from the account's only in capitals.
	Spelling string
}

// Upload adds keys to acc's user on each GitHub host acc uses, through gh.
// It stops at the first host where gh can't add them (ByHand). On an error
// the result still lists what was already added.
func Upload(ctx Context, acc *accounts.Account, exported []ExportKey) (*UploadResult, error) {
	apiHosts := ctx.GitHub.APIHosts(acc.Hosts)
	if len(apiHosts) == 0 {
		return nil, fmt.Errorf("none of account %s's hosts (%s) is GitHub as far as doppel can tell. If one runs GitHub Enterprise Server, sign gh in to it with `gh auth login -h <host>` and run this again; for other hosts, add the keys by hand with `doppel export %s`",
			acc.ID, strings.Join(acc.Hosts, ", "), acc.ID)
	}
	if acc.GitHubUser == "" {
		return nil, fmt.Errorf("doppel needs to know account %s's GitHub user: doppel edit %s --github-user <user>", acc.ID, acc.ID)
	}
	res := &UploadResult{}
	if !github.Available() {
		res.ByHand = "gh isn't installed"
		return res, nil
	}
	for _, apiHost := range apiHosts {
		if err := uploadTo(ctx, res, apiHost, acc, exported); err != nil || res.ByHand != "" {
			return res, err
		}
	}
	return res, nil
}

// uploadTo adds keys to the account's user on one GitHub host.
func uploadTo(ctx Context, res *UploadResult, apiHost string, acc *accounts.Account, exported []ExportKey) error {
	client, err := github.ForUser(apiHost, acc.GitHubUser)
	if err != nil {
		var notSignedIn *github.NotSignedInError
		var tooOld *github.TooOldError
		switch {
		case errors.As(err, &notSignedIn):
			res.ByHand = fmt.Sprintf("%v. Sign in with `gh auth login -h %s` and pick that account, then run this again", notSignedIn, apiHost)
			return nil
		case errors.As(err, &tooOld):
			res.ByHand = tooOld.Error()
			return nil
		}
		return err
	}
	if client.User != acc.GitHubUser {
		res.Spelling = client.User
	}
	login, granted, err := client.Info()
	if err != nil {
		return err
	}
	if !strings.EqualFold(login, client.User) {
		return fmt.Errorf("gh's token for %s on %s belongs to %s", client.User, apiHost, login)
	}

	var kinds []github.Kind
	for _, k := range exported {
		kinds = append(kinds, k.kinds()...)
	}
	if missing := github.MissingScopes(granted, kinds...); len(missing) > 0 {
		return fmt.Errorf("gh's token for %s on %s can't add SSH keys yet. Give it the %s scope with:\n  %s",
			client.User, apiHost, strings.Join(missing, " and "), refreshCommand(apiHost, client.User, missing))
	}

	title := KeyTitle(acc)
	where := client.User
	if apiHost != github.DotCom {
		where += " on " + apiHost
	}
	existing := map[github.Kind][]string{}
	for _, k := range exported {
		pub, err := keys.ReadPublic(k.Key)
		if err != nil {
			return fmt.Errorf("%s: %w", PublicName(ctx, k.Key), err)
		}
		for _, kind := range k.kinds() {
			if _, ok := existing[kind]; !ok {
				if existing[kind], err = client.Keys(kind); err != nil {
					return err
				}
			}
			up := Uploaded{Key: PublicName(ctx, k.Key), Where: where, Kind: kind, Already: slices.Contains(existing[kind], pub)}
			if !up.Already {
				if err := client.Add(kind, k.Line, title); err != nil {
					return err
				}
			}
			res.Uploaded = append(res.Uploaded, up)
		}
	}
	return nil
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
