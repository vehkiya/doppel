// Package github adds SSH keys to GitHub through the gh CLI. It uses the
// token gh keeps for a specific user, so a key reaches the right account
// without switching gh's active account. It works with github.com (where
// GitHub Enterprise Cloud lives too), GHE.com data-residency hosts, and
// GitHub Enterprise Server hosts gh is signed in to.
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/vehkiya/doppel/internal/proc"
)

// DotCom is GitHub's own host, which GitHub Enterprise Cloud shares.
const DotCom = "github.com"

// knownHost recognizes GitHub hosts by name: github.com, its port-443 SSH
// endpoint ssh.github.com, and GHE.com subdomains (GitHub Enterprise Cloud
// with data residency). apiHost is the host gh talks to for it.
func knownHost(host string) (apiHost string, ok bool) {
	h := strings.ToLower(host)
	switch {
	case h == DotCom || h == "ssh."+DotCom:
		return DotCom, true
	case strings.HasSuffix(h, ".ghe.com"):
		return h, true
	}
	return "", false
}

// APIHost tells whether host is GitHub, and which host gh talks to for it.
// Beyond the names knownHost recognizes, a host gh is signed in to counts:
// gh only signs in to GitHub, so that's how GitHub Enterprise Server is
// found. It asks `gh auth status`, which never hands over a token, and
// nothing connects. Each call may run gh, so callers remember the answer.
func APIHost(host string) (string, bool) {
	if api, ok := knownHost(host); ok {
		return api, true
	}
	if !Available() {
		return "", false
	}
	h := strings.ToLower(host)
	if accounts, err := Accounts(h); err == nil {
		if len(accounts) == 0 {
			return "", false
		}
		return h, true
	}
	// A gh too old for `auth status --json` exits with an error when it
	// isn't signed in to the host.
	if _, err := run(h, "", "auth", "status", "--hostname", h); err != nil {
		return "", false
	}
	return h, true
}

// Kind is how GitHub files a key.
type Kind string

// The two kinds of SSH key GitHub keeps.
const (
	Authentication Kind = "authentication"
	Signing        Kind = "signing"
)

// scopes lists the token scopes that let gh add a kind of key; any one will do.
var scopes = map[Kind][]string{
	Authentication: {"admin:public_key", "write:public_key"},
	Signing:        {"admin:ssh_signing_key", "write:ssh_signing_key"},
}

// refreshScope is the scope to ask for when a token can't add a kind of key.
func refreshScope(k Kind) string { return scopes[k][0] }

// Available reports whether gh is installed.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// NotSignedInError means gh has no token for a user on a host.
type NotSignedInError struct {
	Host, User string
	// Reason is what gh said when it refused to give a token, if it did.
	Reason string
	// Known lists the accounts gh is signed in to on Host. It is nil when gh
	// couldn't say, and empty when gh has none.
	Known []string
}

func (e *NotSignedInError) Error() string {
	msg := fmt.Sprintf("gh isn't signed in to %s as %s", e.Host, e.User)
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	switch {
	case e.Known == nil:
	case len(e.Known) == 0:
		msg += "; gh has no accounts on " + e.Host
	default:
		msg += "; gh has " + strings.Join(e.Known, ", ") + " on " + e.Host
	}
	return msg
}

// Account is one account gh is signed in to on a host.
type Account struct {
	Login  string
	Active bool
}

// Accounts lists the accounts gh is signed in to on apiHost, with the
// spelling of each login that gh matches exactly. It reads
// `gh auth status --json hosts`, which prints no tokens, and fails for a gh
// too old to have that flag.
func Accounts(apiHost string) ([]Account, error) {
	out, err := run(apiHost, "", "auth", "status", "--hostname", apiHost, "--json", "hosts")
	if err != nil {
		return nil, err
	}
	var status struct {
		Hosts map[string][]struct {
			Login  string `json:"login"`
			Active bool   `json:"active"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return nil, fmt.Errorf("unexpected reply from gh auth status: %w", err)
	}
	accounts := []Account{}
	for host, list := range status.Hosts {
		if strings.EqualFold(host, apiHost) {
			for _, a := range list {
				accounts = append(accounts, Account{Login: a.Login, Active: a.Active})
			}
		}
	}
	return accounts, nil
}

// Client talks to one GitHub host as one user.
type Client struct {
	Host string
	// User is the login as gh spells it, which can differ in capitals from
	// the name ForUser was asked for.
	User  string
	token string
}

// MinVersion is the oldest gh that can hand over the token of an account
// other than the active one (`gh auth token --user`).
const MinVersion = "2.40"

// TooOldError means the installed gh can't pick an account's token.
type TooOldError struct{ Have string }

func (e *TooOldError) Error() string {
	return fmt.Sprintf("doppel needs gh %s or newer to use the account's own token, and this gh is %s; update gh", MinVersion, e.Have)
}

var ghVersion = regexp.MustCompile(`gh version (\d+)\.(\d+)\.(\d+)`)

// version returns the installed gh's version, such as "2.40.1", and whether
// it's new enough. A version gh doesn't report counts as new enough: gh will
// say so itself if it isn't.
func version() (string, bool) {
	out, err := run("", "", "--version")
	m := ghVersion.FindStringSubmatch(out)
	if err != nil || m == nil {
		return "", true
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return m[1] + "." + m[2] + "." + m[3], major > 2 || (major == 2 && minor >= 40)
}

// ForUser returns a client using the token gh stores for user on apiHost.
// gh matches the user name exactly, so the name is first looked up, ignoring
// case, among the accounts gh knows. A gh too old to list them is given the
// name as it is.
func ForUser(apiHost, user string) (*Client, error) {
	if have, ok := version(); !ok {
		return nil, &TooOldError{Have: have}
	}
	login := user
	var known []string
	if accounts, err := Accounts(apiHost); err == nil {
		known = []string{}
		found := false
		for _, a := range accounts {
			known = append(known, a.Login)
			if !found && strings.EqualFold(a.Login, user) {
				login, found = a.Login, true
			}
		}
		if !found {
			return nil, &NotSignedInError{Host: apiHost, User: user, Known: known}
		}
	}
	out, err := run(apiHost, "", "auth", "token", "--hostname", apiHost, "--user", login)
	token := strings.TrimSpace(out)
	if err != nil || token == "" {
		reason := ""
		if err != nil {
			reason = strings.TrimPrefix(err.Error(), "gh: ")
		}
		return nil, &NotSignedInError{Host: apiHost, User: user, Reason: reason, Known: known}
	}
	return &Client{Host: apiHost, User: login, token: token}, nil
}

// Info returns the user the token belongs to and the scopes it grants.
func (c *Client) Info() (login string, granted []string, err error) {
	out, err := run(c.Host, c.token, "api", "-i", "user")
	if err != nil {
		return "", nil, err
	}
	head, body, _ := strings.Cut(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n")
	for _, line := range strings.Split(head, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "X-Oauth-Scopes") {
			for _, s := range strings.Split(value, ",") {
				if s = strings.TrimSpace(s); s != "" {
					granted = append(granted, s)
				}
			}
		}
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &user); err != nil {
		return "", nil, fmt.Errorf("unexpected reply from GitHub: %w", err)
	}
	return user.Login, granted, nil
}

// MissingScopes returns the scopes to add before the token can add keys of
// the given kinds.
func MissingScopes(granted []string, kinds ...Kind) []string {
	var missing []string
	for _, k := range kinds {
		ok := slices.ContainsFunc(scopes[k], func(s string) bool { return slices.Contains(granted, s) })
		if !ok && !slices.Contains(missing, refreshScope(k)) {
			missing = append(missing, refreshScope(k))
		}
	}
	return missing
}

// Keys lists the user's keys of a kind, as "<type> <base64>".
func (c *Client) Keys(k Kind) ([]string, error) {
	endpoint := "user/keys"
	if k == Signing {
		endpoint = "user/ssh_signing_keys"
	}
	out, err := run(c.Host, c.token, "api", "--paginate", endpoint, "--jq", ".[].key")
	if err != nil {
		return nil, err
	}
	var list []string
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 {
			list = append(list, fields[0]+" "+fields[1])
		}
	}
	return list, nil
}

// Add adds the public key at pubPath to the user's keys of kind k.
func (c *Client) Add(k Kind, pubPath, title string) error {
	_, err := run(c.Host, c.token, "ssh-key", "add", pubPath, "--title", title, "--type", string(k))
	return err
}

// ActiveUser returns the account gh uses by default on apiHost.
func ActiveUser(apiHost string) (string, error) {
	out, err := run(apiHost, "", "api", "user", "--jq", ".login")
	return strings.TrimSpace(out), err
}

// run runs gh against apiHost as token's user, or with gh's own active
// account when token is "". Host and token settings from the environment
// are dropped, so they can't send keys anywhere doppel didn't ask for.
func run(apiHost, token string, args ...string) (string, error) {
	cmd, finish := proc.Command(proc.Network, "gh", args...)
	for _, v := range os.Environ() {
		name, _, _ := strings.Cut(v, "=")
		switch name {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST":
			continue
		}
		cmd.Env = append(cmd.Env, v)
	}
	if apiHost != "" {
		cmd.Env = append(cmd.Env, "GH_HOST="+apiHost)
	}
	if token != "" {
		cmd.Env = append(cmd.Env, "GH_TOKEN="+token)
		if apiHost != DotCom {
			cmd.Env = append(cmd.Env, "GH_ENTERPRISE_TOKEN="+token)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := finish(cmd.Run()); err != nil {
		var timeout *proc.TimeoutError
		if errors.As(err, &timeout) {
			return stdout.String(), fmt.Errorf("gh %s: %w", args[0], err)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), fmt.Errorf("gh %s: %w", args[0], err)
		}
		return stdout.String(), errors.New("gh: " + msg)
	}
	return stdout.String(), nil
}
