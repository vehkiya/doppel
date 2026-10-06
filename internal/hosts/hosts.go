// Package hosts knows about the Git hosts accounts push to: which of them are
// GitHub, which host a repo's remote points at, and how to add a key on each
// kind of host by hand.
package hosts

import (
	"slices"
	"strings"
	"sync"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/github"
)

// GitHub tells which hosts are GitHub, and which host gh talks to for each
// (github.APIHost). Finding out may run gh, so it asks about each host once
// and remembers the answer; a command keeps one for its whole run. The zero
// value is ready to use.
type GitHub struct {
	mu    sync.Mutex
	known map[string]apiHost
}

type apiHost struct {
	host string
	ok   bool
}

// APIHost reports whether host is GitHub, and the host gh talks to for it.
func (g *GitHub) APIHost(host string) (string, bool) {
	key := strings.ToLower(host)
	g.mu.Lock()
	defer g.mu.Unlock()
	if h, ok := g.known[key]; ok {
		return h.host, h.ok
	}
	api, ok := github.APIHost(host)
	if g.known == nil {
		g.known = map[string]apiHost{}
	}
	g.known[key] = apiHost{api, ok}
	return api, ok
}

// Forget drops every answer, so the next question asks gh again: it may
// have signed in to a host since.
func (g *GitHub) Forget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.known = nil
}

// Is reports whether host is GitHub: github.com, GHE.com, or a GitHub
// Enterprise Server gh is signed in to.
func (g *GitHub) Is(host string) bool {
	_, ok := g.APIHost(host)
	return ok
}

// Any reports whether any of hosts is GitHub.
func (g *GitHub) Any(hosts []string) bool { return slices.ContainsFunc(hosts, g.Is) }

// APIHosts lists the hosts gh talks to for the GitHub hosts among hosts,
// each once: github.com and ssh.github.com share one.
func (g *GitHub) APIHosts(hosts []string) []string {
	var list []string
	for _, h := range hosts {
		if api, ok := g.APIHost(h); ok && !slices.Contains(list, api) {
			list = append(list, api)
		}
	}
	return list
}

// Use says what a key is for.
type Use struct{ Auth, Signing bool }

// Steps says where to add a key on host by hand, and which key type to
// pick. title names the key on the host's settings page; keyFile is the
// key's public file, as the user should type it.
func (g *GitHub) Steps(host string, use Use, title, keyFile string) []string {
	if api, ok := g.APIHost(host); ok {
		steps := []string{"Open https://" + api + "/settings/ssh/new", "Title: " + title}
		switch {
		case use.Auth && use.Signing:
			steps = append(steps, "Key type: Authentication Key. Then add it a second time with Key type: Signing Key.")
		case use.Auth:
			steps = append(steps, "Key type: Authentication Key")
		default:
			steps = append(steps, "Key type: Signing Key")
		}
		return append(steps, "Paste the key and click Add SSH key.")
	}

	if strings.Contains(strings.ToLower(host), "gitlab") {
		usage := "Signing"
		switch {
		case use.Auth && use.Signing:
			usage = "Authentication & Signing"
		case use.Auth:
			usage = "Authentication"
		}
		return []string{
			"Open https://" + host + "/-/user_settings/ssh_keys",
			"Paste the key and set Title: " + title,
			"Usage type: " + usage,
			"Click Add key.",
		}
	}

	steps := []string{
		"On Gitea, Forgejo or Codeberg, open https://" + host + "/user/settings/keys; on other hosts, find the SSH keys page in your settings.",
		"Add the key under SSH Keys, named " + title + ".",
	}
	if use.Signing {
		steps = append(steps, "For signed commits to show as verified, click Verify next to the key and sign the token shown: echo -n '<token>' | ssh-keygen -Y sign -n gitea -f "+keyFile)
	}
	return steps
}

// RemoteURL returns the URL of the repo's origin remote, or of its first
// remote when there's no origin. It's "" when the repo has no remote.
func RemoteURL(repo string) string {
	out, err := git.Run(repo, "remote")
	if err != nil {
		return ""
	}
	remotes := strings.Fields(out)
	if len(remotes) == 0 {
		return ""
	}
	name := remotes[0]
	if slices.Contains(remotes, "origin") {
		name = "origin"
	}
	url, err := git.Run(repo, "remote", "get-url", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(url)
}

// SSHHost returns the host of an SSH remote URL (ssh://[user@]host[:port]/path
// or the scp-like [user@]host:path), or "" for any other kind of remote.
func SSHHost(url string) string {
	if rest, ok := strings.CutPrefix(url, "ssh://"); ok {
		hostPart, _, _ := strings.Cut(rest, "/")
		if at := strings.LastIndex(hostPart, "@"); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		if strings.HasPrefix(hostPart, "[") { // an IPv6 address
			hostPart, _, _ = strings.Cut(strings.TrimPrefix(hostPart, "["), "]")
			return hostPart
		}
		hostPart, _, _ = strings.Cut(hostPart, ":")
		return hostPart
	}
	if strings.Contains(url, "://") {
		return ""
	}
	colon := strings.Index(url, ":")
	if colon <= 0 || strings.Contains(url[:colon], "/") {
		return "" // a local path
	}
	hostPart := url[:colon]
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	return hostPart
}

// Scheme names the kind of a remote that isn't SSH, such as "HTTPS" or
// "a local path".
func Scheme(url string) string {
	if scheme, _, ok := strings.Cut(url, "://"); ok {
		return strings.ToUpper(scheme)
	}
	return "a local path"
}
