package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/ui"
)

const whoamiUsage = "doppel whoami [path] [--offline]"

// Settings whoami checks come from the account in effect.
var identityKeys = []string{accounts.KeyName, accounts.KeyEmail, accounts.KeySigningKey, accounts.KeyGPGFormat, accounts.KeyCommitSign, accounts.KeyTagSign, accounts.KeySSHCommand}

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

	// A path that doesn't exist yet, such as a clone target, is answered by
	// the folder rules: asking Git from an existing parent would describe
	// whatever repo encloses it, not the new repo.
	dir := filepath.Clean(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() { //nolint:gosec // the user's own path argument; only its type is read
		if err == nil {
			dir = filepath.Dir(dir) // a file: describe the repo it's in
		} else {
			return a.whoamiOutsideRepo(list, path)
		}
	}
	gitDir, err := git.Run(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return a.whoamiOutsideRepo(list, path)
	}
	return a.whoamiInRepo(list, dir, strings.TrimSpace(gitDir), offline)
}

// whoamiInRepo asks Git which account applies, so the answer always matches
// what Git does, then explains it.
func (a *app) whoamiInRepo(list []*accounts.Account, path, gitDir string, offline bool) int {
	out, err := git.Run(path, "config", "--show-scope", "--show-origin", "--list", "--null")
	if err != nil {
		return a.fail(err)
	}
	effective := map[string]git.ConfigEntry{}
	for _, e := range git.ParseConfigList(out, true) {
		effective[e.Key] = e // the last value wins, as in Git
	}
	value := func(key string) string { return effective[strings.ToLower(key)].Value }

	top, err := git.Run(path, "rev-parse", "--show-toplevel")
	repo := strings.TrimSpace(top)
	if err != nil || repo == "" {
		repo = filepath.Dir(gitDir) // a bare repo has no working tree
	}
	a.row("Repo", a.env.Shorten(repo))

	id := value(accounts.KeyAccount)
	acc := accounts.Find(list, id)
	switch {
	case id == "":
		a.row("Account", ui.Warn.Render("none"))
	case acc == nil:
		a.row("Account", ui.Warn.Render(id+" (no such account file)"))
	default:
		real := gitDir
		if resolved, err := filepath.EvalSymlinks(gitDir); err == nil {
			real = resolved
		}
		a.row("Account", ui.Accent.Render(acc.ID)+ui.Dim.Render(" ("+a.ruleFor(list, acc, real)+")"))
	}
	a.row("Name", value(accounts.KeyName))
	a.row("Email", value(accounts.KeyEmail))
	switch ssh := value(accounts.KeySSHCommand); {
	case acc != nil && acc.AuthKey != "" && ssh == accounts.SSHCommand(acc.AuthKey):
		a.row("Auth key", acc.AuthKey+ui.Dim.Render(a.keyStatus(acc.AuthKey)))
	case ssh == "ssh" || ssh == "":
		a.row("Auth key", "your default SSH keys")
	default:
		a.row("SSH", ssh)
	}
	if accounts.ParseBool(value(accounts.KeyCommitSign)) || accounts.ParseBool(value(accounts.KeyTagSign)) {
		a.row("Signing", fmt.Sprintf("%s · %s", signingScope(value(accounts.KeyCommitSign), value(accounts.KeyTagSign)), value(accounts.KeySigningKey)))
	} else {
		a.row("Signing", "off")
	}

	if acc != nil && !offline {
		a.loginRow(acc, path)
	}

	// path may be a plain folder inside an enclosing repo, such as a home
	// directory managed with yadm. A repo created there gets its own
	// account, so say so when that differs from the enclosing repo's.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if top, err := filepath.EvalSymlinks(repo); err == nil && real != top {
			if newID, rule := a.folderRule(list, real); newID != id && newID != "" {
				a.row("New repos", ui.Accent.Render(newID)+ui.Dim.Render(" ("+rule+")"))
			}
		}
	}

	if id == "" {
		a.printf("\n")
		if accounts.Default(list) == nil {
			a.notef("No doppel account applies here. Bind this repo's folder with `doppel bind <id> <folder>`, or set a default with `doppel default <id>`.")
		}
		return 0
	}
	a.warnOverrides(effective)
	return 0
}

// ruleFor explains why Git applied acc to a repo: the folder rule that
// picks it, or being the default account. "matched by Git" covers anything
// else, such as an index that doesn't match the account files yet.
func (a *app) ruleFor(list []*accounts.Account, acc *accounts.Account, gitDir string) string {
	if r, ok := accounts.MatchFolder(a.env, list, gitDir); ok && r.ID == acc.ID {
		return "folder " + r.Folder
	}
	if acc.Default {
		return "default account"
	}
	return "matched by Git"
}

// warnOverrides points out identity settings that don't come from a doppel
// account file, such as a repo's own `git config --local` values or a global
// setting placed after doppel's include.
func (a *app) warnOverrides(effective map[string]git.ConfigEntry) {
	var lines []string
	for _, key := range identityKeys {
		e, ok := effective[strings.ToLower(key)]
		if !ok || a.isAccountFile(e.Origin) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s = %s comes from %s (%s config)", key, e.Value, strings.TrimPrefix(e.Origin, "file:"), e.Scope))
	}
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if v := os.Getenv(name); v != "" {
			lines = append(lines, fmt.Sprintf("%s=%s is set in your environment and overrides the account's SSH command", name, v))
		}
	}
	if len(lines) == 0 {
		return
	}
	a.printf("\n")
	for _, line := range lines {
		a.warnf("%s", line)
	}
}

// isAccountFile reports whether a --show-origin value names a doppel account file.
func (a *app) isAccountFile(origin string) bool {
	path, ok := strings.CutPrefix(origin, "file:")
	if !ok {
		return false
	}
	return a.env.SamePath(filepath.Dir(path), a.env.AccountsDir())
}

// whoamiOutsideRepo applies the folder rules itself: Git can only answer for
// an existing repo.
func (a *app) whoamiOutsideRepo(list []*accounts.Account, path string) int {
	real, _ := paths.ResolveExisting(filepath.Clean(path))
	note := " (not a Git repository)"
	if _, err := os.Stat(path); err != nil { //nolint:gosec // the user's own path argument; only its existence is read
		note = " (doesn't exist yet)"
	}
	a.row("Path", a.env.Shorten(real)+ui.Dim.Render(note))
	if id, rule := a.folderRule(list, real); id != "" {
		a.row("New repos", ui.Accent.Render(id)+ui.Dim.Render(" ("+rule+")"))
	} else {
		a.row("New repos", ui.Warn.Render("no doppel account")+ui.Dim.Render(" (your global Git config applies)"))
	}
	return 0
}

// folderRule returns the account a new repo at path (a real path) would get,
// and the rule that picks it: the folder rule Git would apply, or else the
// default account. It returns "" when no account applies.
func (a *app) folderRule(list []*accounts.Account, path string) (id, rule string) {
	if r, ok := accounts.MatchFolder(a.env, list, path); ok {
		return r.ID, "folder " + r.Folder
	}
	if def := accounts.Default(list); def != nil {
		return def.ID, "default account"
	}
	return "", ""
}

func (a *app) row(label, value string) {
	a.printf("%s %s\n", ui.Label.Render(fmt.Sprintf("%-9s", label)), value)
}

func signingScope(commits, tags string) string {
	switch {
	case accounts.ParseBool(commits) && accounts.ParseBool(tags):
		return "commits and tags"
	case accounts.ParseBool(commits):
		return "commits"
	}
	return "tags"
}

// keyStatus describes how a key is kept, such as " (passphrase, in agent)".
func (a *app) keyStatus(key string) string {
	path := a.env.Expand(key)
	status := []string{keys.CheckProtection(path).String()}
	if loaded, running := keys.InAgent(path); running && loaded {
		status = append(status, "in agent")
	}
	return " (" + strings.Join(status, ", ") + ")"
}

// loginRow tries the account's key on the repo's remote host, without
// asking for anything: a passphrase prompt in whoami would get in the way.
func (a *app) loginRow(acc *accounts.Account, repo string) {
	url := remoteURL(repo)
	if url == "" {
		return
	}
	host := sshHost(url)
	if host == "" {
		a.row("Login", ui.Dim.Render("the remote uses "+urlScheme(url)+", which doppel's SSH keys don't cover"))
		return
	}
	if ok, detail := a.checkLogin(acc, host, true); ok {
		a.row("Login", ui.OK.Render("✓")+" "+host+": "+detail)
	} else {
		a.row("Login", ui.Error.Render("✗")+" "+host+": "+detail+ui.Dim.Render(" (try `doppel test "+acc.ID+"`)"))
	}
}

// remoteURL returns the URL of the repo's origin remote, or of its first
// remote when there's no origin.
func remoteURL(repo string) string {
	out, err := git.Run(repo, "remote")
	if err != nil {
		return ""
	}
	remotes := strings.Fields(out)
	if len(remotes) == 0 {
		return ""
	}
	name := remotes[0]
	for _, r := range remotes {
		if r == "origin" {
			name = r
		}
	}
	url, err := git.Run(repo, "remote", "get-url", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(url)
}

// sshHost returns the host of an SSH remote URL (ssh://[user@]host[:port]/path
// or the scp-like [user@]host:path), or "" for any other kind of remote.
func sshHost(url string) string {
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

func urlScheme(url string) string {
	if scheme, _, ok := strings.Cut(url, "://"); ok {
		return strings.ToUpper(scheme)
	}
	return "a local path"
}
