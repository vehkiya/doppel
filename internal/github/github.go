// Package github adds SSH keys to GitHub through the gh CLI. It uses the
// token gh keeps for a specific user, so a key reaches the right account
// without switching gh's active account.
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Host is the only host doppel uploads to.
const Host = "github.com"

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

// RefreshScope is the scope to ask for when a token can't add a kind of key.
func RefreshScope(k Kind) string { return scopes[k][0] }

// Available reports whether gh is installed.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// NotSignedInError means gh has no token for a user.
type NotSignedInError struct{ User string }

func (e *NotSignedInError) Error() string {
	return fmt.Sprintf("gh isn't signed in to %s as %s", Host, e.User)
}

// Client talks to GitHub as one user.
type Client struct {
	User  string
	token string
}

// ForUser returns a client using the token gh stores for user.
func ForUser(user string) (*Client, error) {
	out, err := run("", "auth", "token", "--hostname", Host, "--user", user)
	token := strings.TrimSpace(out)
	if err != nil || token == "" {
		return nil, &NotSignedInError{User: user}
	}
	return &Client{User: user, token: token}, nil
}

// Info returns the user the token belongs to and the scopes it grants.
func (c *Client) Info() (login string, granted []string, err error) {
	out, err := run(c.token, "api", "-i", "user")
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
		if !ok && !slices.Contains(missing, RefreshScope(k)) {
			missing = append(missing, RefreshScope(k))
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
	out, err := run(c.token, "api", "--paginate", endpoint, "--jq", ".[].key")
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
	_, err := run(c.token, "ssh-key", "add", pubPath, "--title", title, "--type", string(k))
	return err
}

// ActiveUser returns the account gh uses by default on GitHub.
func ActiveUser() (string, error) {
	out, err := run("", "api", "user", "--jq", ".login")
	return strings.TrimSpace(out), err
}

// run runs gh as token's user, or with gh's own active account when token
// is "". Tokens from the environment are dropped, so they can't override
// the account doppel asked for.
func run(token string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...) //nolint:gosec // fixed binary; arguments built by doppel
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GH_TOKEN=") && !strings.HasPrefix(v, "GITHUB_TOKEN=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	if token != "" {
		cmd.Env = append(cmd.Env, "GH_TOKEN="+token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), fmt.Errorf("gh %s: %w", args[0], err)
		}
		return stdout.String(), errors.New("gh: " + msg)
	}
	return stdout.String(), nil
}
