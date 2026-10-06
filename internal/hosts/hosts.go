// Package hosts knows about the Git hosts accounts push to: which of them are
// GitHub, which host a repo's remote points at, and how to add a key on each
// kind of host by hand.
package hosts

import (
	"regexp"
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

// Remote is one of a repo's remotes, with its URLs as Git uses them
// (url.insteadOf applied).
type Remote struct {
	Name     string
	FetchURL string
	PushURL  string
}

// Remotes lists a repo's remotes, origin first, then the others in the
// order Git lists them.
func Remotes(repo string) []Remote {
	out, err := git.Run(repo, "remote", "-v")
	if err != nil {
		return nil
	}
	var list []Remote
	find := func(name string) *Remote {
		for i := range list {
			if list[i].Name == name {
				return &list[i]
			}
		}
		list = append(list, Remote{Name: name})
		return &list[len(list)-1]
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		r := find(fields[0])
		switch fields[2] {
		case "(fetch)":
			r.FetchURL = fields[1]
		case "(push)":
			r.PushURL = fields[1]
		}
	}
	slices.SortStableFunc(list, func(a, b Remote) int {
		switch {
		case a.Name == b.Name:
			return 0
		case a.Name == "origin":
			return -1
		case b.Name == "origin":
			return 1
		}
		return 0
	})
	return list
}

// MainRemote returns the repo's origin remote, or its first remote when
// there's no origin. ok is false when the repo has no remote.
func MainRemote(repo string) (r Remote, ok bool) {
	list := Remotes(repo)
	if len(list) == 0 {
		return Remote{}, false
	}
	return list[0], true
}

// IsHTTP reports whether url is an HTTPS (or plain HTTP) remote, which SSH
// keys don't cover.
func IsHTTP(url string) bool {
	return strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://")
}

// SSHURL returns the SSH form of an HTTPS remote, git@host:path, as GitHub,
// GitLab, Gitea and most other hosts take it. Any user name or token in
// the HTTPS URL is dropped. ok is false for a URL that isn't HTTPS, or that
// names a port, since the host's SSH port can't be told from it.
func SSHURL(url string) (ssh string, ok bool) {
	rest, found := strings.CutPrefix(url, "https://")
	if !found {
		if rest, found = strings.CutPrefix(url, "http://"); !found {
			return "", false
		}
	}
	hostPart, path, _ := strings.Cut(rest, "/")
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	path = strings.Trim(path, "/")
	if hostPart == "" || path == "" || strings.ContainsAny(hostPart, ":[") || strings.ContainsAny(path, "?#") {
		return "", false
	}
	return "git@" + hostPart + ":" + path, true
}

// SwitchToSSH returns the command that switches a remote's HTTPS URLs to
// SSH, or "" when there's nothing it can switch. dir is the repo the
// command runs in, as the user would type it; "" leaves out -C, for a
// command run inside the repo.
func SwitchToSSH(r Remote, dir string) string {
	git := "git"
	if dir != "" {
		git += " -C " + shellArg(dir)
	}
	var commands []string
	if ssh, ok := SSHURL(r.FetchURL); ok {
		commands = append(commands, git+" remote set-url "+shellArg(r.Name)+" "+ssh)
	}
	if r.PushURL != r.FetchURL {
		if ssh, ok := SSHURL(r.PushURL); ok {
			commands = append(commands, git+" remote set-url --push "+shellArg(r.Name)+" "+ssh)
		}
	}
	return strings.Join(commands, " && ")
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./~@%+=:,-]+$`)

// shellArg quotes s for a shell when it needs it. A leading "~/" stays
// outside the quotes, so the shell still expands it.
func shellArg(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	prefix := ""
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		prefix, s = "~/", rest
	}
	return prefix + "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
