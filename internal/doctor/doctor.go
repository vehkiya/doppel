// Package doctor checks everything that could make Git use the wrong
// account, and says how to fix each finding. It returns findings rather
// than printing them, so cli decides how they look.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/hosts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/plan"
	"github.com/vehkiya/doppel/internal/store"
)

// Severity is how much a finding matters.
type Severity int

const (
	OK      Severity = iota // a check that passed
	Note                    // worth knowing; nothing to fix
	Warning                 // works, but worth fixing
	Problem                 // Git may use the wrong account, or fail
)

// Finding is one result of a check.
type Finding struct {
	Severity Severity
	Area     string // what was checked: "Git and SSH", "Git config", or "Account <id>"
	Message  string
	Fix      string // one line saying how to fix it; "" when there's nothing to do
}

// Count returns how many findings have severity s.
func Count(findings []Finding, s Severity) int {
	n := 0
	for _, f := range findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// Options says what to check, and how.
type Options struct {
	Env      *paths.Env
	Accounts []*accounts.Account
	GitHub   *hosts.GitHub
	// Keychain is true on a Mac whose ssh is Apple's, which can keep
	// passphrases in the Keychain (keys.KeychainSupported).
	Keychain bool
	// Fix brings doppel's files up to date with the accounts before they're
	// checked, through store.Save like any other write. The caller holds
	// the write lock from before it loaded the accounts.
	Fix bool
}

// checker collects findings, each in the area being checked.
type checker struct {
	Options
	area     string
	findings []Finding
}

func (c *checker) add(s Severity, fix, format string, args ...any) {
	c.findings = append(c.findings, Finding{Severity: s, Area: c.area, Message: fmt.Sprintf(format, args...), Fix: fix})
}

func (c *checker) ok(format string, args ...any)   { c.add(OK, "", format, args...) }
func (c *checker) note(format string, args ...any) { c.add(Note, "", format, args...) }
func (c *checker) warn(fix, format string, args ...any) {
	c.add(Warning, fix, format, args...)
}
func (c *checker) problem(fix, format string, args ...any) {
	c.add(Problem, fix, format, args...)
}

// Check runs every check and returns the findings, in the order of their
// areas: Git and SSH, Git config, then each account. Without Options.Fix it
// writes nothing.
func Check(opts Options) []Finding {
	c := &checker{Options: opts}
	env := c.Env

	c.area = "Git and SSH"
	c.ok("Git %s", git.Version())
	switch major, minor, ok := keys.OpenSSHVersion(); {
	case !ok:
		c.warn("", "Couldn't tell which OpenSSH is installed; SSH signing needs 8.2 or newer")
	case major < 8 || (major == 8 && minor < 2):
		c.problem("Update OpenSSH", "OpenSSH %d.%d can't verify SSH signatures; Git needs 8.2 or newer", major, minor)
	default:
		c.ok("OpenSSH %d.%d", major, minor)
	}
	overridden := false
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if v := os.Getenv(name); v != "" {
			c.problem("Unset it in your shell profile", "%s=%s is set, so every account uses it instead of its own key", name, v)
			overridden = true
		}
	}
	if !overridden {
		c.ok("GIT_SSH_COMMAND and GIT_SSH aren't set")
	}
	if env.GOOS == "darwin" {
		if c.Keychain {
			c.ok("ssh is Apple's, so it can keep passphrases in the Keychain")
		} else {
			ssh, _ := exec.LookPath("ssh")
			c.warn("Put /usr/bin ahead of it on your PATH, or keep your keys in an agent such as 1Password",
				"ssh (%s) isn't Apple's, so it can't keep passphrases in the Keychain; macOS asks for them in the terminal", env.Shorten(ssh))
		}
	}

	c.area = "Git config"
	if len(c.Accounts) == 0 {
		c.note("No accounts yet. Add one with `doppel add`.")
	} else {
		c.files()
		if accounts.Default(c.Accounts) == nil {
			c.note("No default account: repos outside every folder use your global Git identity")
		}
	}

	for _, acc := range c.Accounts {
		c.area = "Account " + acc.ID
		c.account(acc)
	}
	return c.findings
}

// files checks doppel's place in the global Git config and that its files
// say what the accounts say. With Fix, it rewrites them first.
func (c *checker) files() {
	env := c.Env
	p := plan.New(env.StagingDir())
	defer p.Close()
	// Save validates the accounts first, so a hand-edited folder that would
	// break Git's config is reported here rather than written by Fix.
	var outdated []string
	upToDate := false
	if err := store.Save(env, p, c.Accounts, store.Options{}); err != nil {
		fix := ""
		if strings.Contains(err.Error(), "doppel default <id>") {
			fix = "Pick one with `doppel default <id>`"
		}
		c.problem(fix, "Can't bring doppel's files up to date: %v", err)
	} else if changes, err := p.Changes(); err != nil {
		c.problem("", "%v", err)
	} else {
		upToDate = len(changes) == 0
		if len(changes) > 0 && c.Fix {
			if _, err := p.Apply(); err != nil {
				c.problem("", "Couldn't update doppel's files: %v", err)
			} else {
				for _, ch := range changes {
					c.ok("Updated %s", env.Shorten(ch.Path))
				}
				upToDate = true
			}
		}
		if !upToDate {
			for _, ch := range changes {
				outdated = append(outdated, env.Shorten(ch.Path))
			}
		}
	}

	path, found, overriding, err := store.Overrides(env, accounts.IdentityKeys())
	short := env.Shorten(path)
	switch {
	case err != nil:
		c.problem("Fix the file so Git can read it", "Can't read %s: %v", short, err)
	case !found:
		c.problem("Run `doppel doctor --fix`", "%s doesn't include doppel's accounts, so Git doesn't use them", short)
	default:
		c.ok("%s includes doppel's accounts", short)
	}
	for _, key := range overriding {
		c.problem("Move it above doppel's include, or remove it: the accounts set it", "%s sets %s after doppel's include, overriding every account", short, key)
	}

	// doppel's own Git calls ignore the user's config, so only Git run
	// as the user can say whether it can read what doppel wrote.
	if paths.FileExists(env.GlobalConfigPath()) {
		if _, err := git.Run("", "config", "--global", "--includes", "--list"); err != nil {
			c.problem("Fix the file the error names; until then every Git command fails", "Git can't read your global config: %v", err)
		} else {
			c.ok("Git reads your global config and everything it includes")
		}
	}

	if len(outdated) > 0 {
		c.warn("Run `doppel doctor --fix`", "doppel's files don't match the accounts: %s", strings.Join(outdated, ", "))
	} else if upToDate && err == nil {
		c.ok("doppel's files match the accounts")
	}
}

// account checks one account's keys, folders and SSH config.
func (c *checker) account(acc *accounts.Account) {
	env := c.Env
	if acc.Protocol == accounts.ProtocolHTTPS {
		c.ok("Uses HTTPS for Git remotes")
	} else if acc.AuthKey == "" {
		if others, host := keylessNeighbours(acc, c.Accounts); len(others) > 0 {
			c.problem("Give each account a key of its own: doppel edit <id> --generate-auth-key",
				"%s and %s both log in to %s with ssh's own keys, so they log in as the same user", acc.ID, strings.Join(others, ", "), host)
		} else {
			c.note("Logs in with ssh's own keys")
		}
	} else {
		c.key("Auth key", acc.AuthKey)
		c.keychain(acc)
		if other, host := c.sharedAuthKey(acc); other != "" {
			c.problem("Give each account a key of its own: doppel edit <id> --generate-auth-key",
				"%s and %s use the same auth key on %s, so one of them logs in as the other", acc.ID, other, host)
		}
	}

	switch {
	case acc.SigningKey == "":
		c.note("Doesn't sign")
	case acc.SigningKey.SameKey(acc.AuthKey):
		c.ok("Signs %s with the auth key", accounts.SigningScope(acc.SignCommits, acc.SignTags))
	default:
		if _, err := keys.ReadPublic(acc.SigningKey.Map(env.Expand)); err != nil {
			c.problem("doppel edit "+acc.ID+" --signing-key <key>, or --no-signing", "Signing key %s: %v", acc.SigningKey.Display(), err)
		} else {
			c.key("Signing key", acc.SigningKey)
		}
	}
	if acc.SigningKey != "" {
		c.signingAgent(acc.SigningKey)
	}

	for _, f := range acc.Folders {
		if !paths.FileExists(env.Expand(f)) {
			c.warn("", "Folder %s doesn't exist yet; its rule applies once it does", f)
		}
	}
	if len(acc.Folders) == 0 && !acc.Default {
		c.note("No folders, and it isn't the default, so no repo uses it yet")
	}

	if cfg := filepath.Join(env.Home, ".ssh", "config"); paths.FileExists(cfg) {
		own := acc.AuthKey.Map(env.Expand)
		for _, h := range acc.Hosts {
			var extra []string
			for _, f := range keys.HostIdentityFiles(cfg, h) {
				if !keys.Ref(f).Map(env.Expand).SameKey(own) {
					extra = append(extra, env.Shorten(env.Expand(f)))
				}
			}
			if len(extra) > 0 {
				c.warn("Remove IdentityFile from the "+h+" entry in ~/.ssh/config; doppel's accounts pick the key",
					"~/.ssh/config also offers %s for %s. If the account's key were rejected, ssh would try that and could log in as someone else", strings.Join(extra, ", "), h)
			}
		}
	}

	if !acc.AllowsHTTPS() {
		repos := httpsRepos(env, acc.Folders)
		for i, r := range repos {
			if i == maxHTTPSRepos {
				c.warn("Switch these first, then run doctor again to see the rest", "…and %s in its folders use HTTPS", Plural(len(repos)-i, "more repo"))
				break
			}
			fix := "Switch it to SSH: " + r.fix
			if r.fix == "" {
				fix = "Switch it to SSH: git -C " + r.dir + " remote set-url <remote> git@<host>:<owner>/<repo>.git"
			}
			c.warn(fix, "%s fetches over HTTPS, which doppel's keys don't cover", r.dir)
		}
	} else if !acc.AllowsSSH() {
		repos := sshRepos(env, acc.Folders)
		for i, r := range repos {
			if i == maxHTTPSRepos {
				c.warn("Switch these first, then run doctor again to see the rest", "…and %s in its folders use SSH", Plural(len(repos)-i, "more repo"))
				break
			}
			fix := "Switch it to HTTPS: " + r.fix
			if r.fix == "" {
				fix = "Switch it to HTTPS: git -C " + r.dir + " remote set-url <remote> https://<host>/<owner>/<repo>.git"
			}
			c.warn(fix, "%s fetches over SSH, but %s is configured for HTTPS only", r.dir, acc.ID)
		}
	}

	if acc.AllowsHTTPS() && acc.EffectiveHTTPSUser() == "" {
		c.note("Set its HTTPS user (doppel edit %s --https-user <user>) so Git credential helper knows which user to use", acc.ID)
	}

	if acc.GitHubUser == "" && c.GitHub.Any(acc.Hosts) {
		c.note("Set its GitHub user (doppel edit %s --github-user <user>) so test can check logins and upload can add keys", acc.ID)
	}
}

// key checks how a key is kept.
func (c *checker) key(label string, key keys.Ref) {
	path := key.Map(c.Env.Expand)
	name := key.Display()
	loaded, running := keys.InAgent(path)
	switch keys.CheckProtection(path) {
	case keys.Unknown:
		c.problem("Point the account at an existing key with `doppel edit`", "%s %s doesn't exist", label, name)
	case keys.Unencrypted:
		c.warn("Add a passphrase: ssh-keygen -p -f "+key.PrivatePath(), "%s %s has no passphrase, so anyone who copies it can use it", label, name)
	case keys.HardwareKey:
		c.ok("%s %s is on a security key", label, name)
	case keys.AgentOnly:
		if running && !loaded {
			c.warn("Unlock it in your agent (1Password, Proton Pass, or ssh-add)", "%s %s lives in an agent, but the agent doesn't hold it right now", label, name)
		} else {
			c.ok("%s %s (private key in an agent)", label, name)
		}
	default:
		status := "passphrase"
		if loaded {
			status += ", in agent"
		}
		c.ok("%s %s (%s)", label, name, status)
	}
}

// signingAgent checks that a signing key with a passphrase is in the agent.
// ssh-keygen signs with the agent's copy; without one it asks for the
// passphrase on every signed commit, and never reads the macOS Keychain
// (UseKeychain is ssh's alone), so unlike logging in, nothing else can
// supply it.
func (c *checker) signingAgent(key keys.Ref) {
	path := key.Map(c.Env.Expand)
	if keys.CheckProtection(path) != keys.Encrypted {
		return
	}
	load := keys.LoadCommand(key, c.Keychain)
	switch loaded, running := keys.InAgent(path); {
	case loaded:
	case !running:
		c.warn("Start ssh-agent (eval \"$(ssh-agent)\" in your shell profile), then run `"+load+"`",
			"Signing key %s has a passphrase, and no ssh-agent is running to hold it, so Git asks for it on every signed commit", key.Display())
	default:
		fix := "Load it with `" + load + "`"
		if c.Keychain {
			fix += "; after logging in, `ssh-add --apple-load-keychain` puts it back"
		}
		c.warn(fix, "Signing key %s has a passphrase but isn't in your agent, so Git asks for it on every signed commit", key.Display())
	}
}

// keychain checks, on a Mac, that ssh keeps the passphrase of acc's auth key
// in the Keychain and loads the key into the agent, so neither Git nor
// signing asks for it in the terminal.
func (c *checker) keychain(acc *accounts.Account) {
	if !c.Keychain || keys.CheckProtection(acc.AuthKey.Map(c.Env.Expand)) != keys.Encrypted {
		return
	}
	cfg := filepath.Join(c.Env.Home, ".ssh", "config")
	for _, h := range acc.Hosts {
		keychain, agent := keys.UsesKeychain(cfg, h), keys.AddsKeysToAgent(cfg, h)
		var missing []string
		if !keychain {
			missing = append(missing, "UseKeychain yes")
		}
		if !agent {
			missing = append(missing, "AddKeysToAgent yes")
		}
		if len(missing) == 0 {
			continue
		}
		fix := fmt.Sprintf("Add %s under `Host %s` (or `Host *`) in ~/.ssh/config, then run `%s` once",
			strings.Join(missing, " and "), h, keys.LoadCommand(acc.AuthKey, true))
		if !keychain {
			c.warn(fix, "~/.ssh/config doesn't set %s for %s, so macOS asks for the passphrase of %s in the terminal",
				strings.Join(missing, " or "), h, acc.AuthKey.Display())
		} else {
			c.warn(fix, "~/.ssh/config doesn't set AddKeysToAgent for %s, so ssh doesn't load %s into the agent, and signing with it asks for its passphrase",
				h, acc.AuthKey.Display())
		}
	}
}

// keylessNeighbours lists the accounts after acc that also use ssh's own
// keys on one of its hosts, so the pair is reported once.
func keylessNeighbours(acc *accounts.Account, list []*accounts.Account) ([]string, string) {
	var others []string
	host := ""
	after := false
	for _, other := range list {
		if other == acc {
			after = true
			continue
		}
		if !after || other.AuthKey != "" || !other.AllowsSSH() {
			continue
		}
		if h := accounts.SharedHost(acc, other); h != "" {
			others, host = append(others, other.ID), h
		}
	}
	return others, host
}

// sharedAuthKey finds an account after acc using the same auth key on a
// shared host, so the pair is reported once.
func (c *checker) sharedAuthKey(acc *accounts.Account) (string, string) {
	after := false
	for _, other := range c.Accounts {
		if other == acc {
			after = true
			continue
		}
		if !after || !other.AllowsSSH() {
			continue
		}
		if host := accounts.SharedAuthKey(c.Env, acc, other); host != "" {
			return other.ID, host
		}
	}
	return "", ""
}

// maxHTTPSRepos is how many HTTPS repos doctor names in each account's
// folders before it only counts the rest.
const maxHTTPSRepos = 5

// httpsRepo is a repo with a remote over HTTPS, and the command that
// switches its remotes to SSH ("" when doppel can't work it out).
type httpsRepo struct{ dir, fix string }

// httpsRepos finds repos in folders with a remote over HTTPS. It looks three
// levels deep and skips hidden and dependency folders, so big trees stay quick.
func httpsRepos(env *paths.Env, folders []string) []httpsRepo {
	var found []httpsRepo
	for _, f := range folders {
		visited := 0
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			if visited >= 2000 || depth > 3 {
				return
			}
			visited++
			if paths.FileExists(filepath.Join(dir, ".git")) {
				if r, ok := httpsRemotes(env, dir); ok {
					found = append(found, r)
				}
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() && !strings.HasPrefix(name, ".") && name != "node_modules" && name != "vendor" {
					walk(filepath.Join(dir, name), depth+1)
				}
			}
		}
		walk(strings.TrimSuffix(env.Expand(f), "/"), 0)
	}
	return found
}

// httpsRemotes checks a repo's remotes. ok is true when one uses HTTPS.
func httpsRemotes(env *paths.Env, dir string) (repo httpsRepo, ok bool) {
	repo.dir = env.Shorten(dir)
	var fixes []string
	for _, r := range hosts.Remotes(dir) {
		if !hosts.IsHTTP(r.FetchURL) && !hosts.IsHTTP(r.PushURL) {
			continue
		}
		ok = true
		if fix := hosts.SwitchToSSH(r, repo.dir); fix != "" {
			fixes = append(fixes, fix)
		}
	}
	repo.fix = strings.Join(fixes, " && ")
	return repo, ok
}

// sshRepo is a repo with a remote over SSH, and the command that
// switches its remotes to HTTPS ("" when doppel can't work it out).
type sshRepo struct{ dir, fix string }

// sshRepos finds repos in folders with a remote over SSH. It looks three
// levels deep and skips hidden and dependency folders, so big trees stay quick.
func sshRepos(env *paths.Env, folders []string) []sshRepo {
	var found []sshRepo
	for _, f := range folders {
		visited := 0
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			if visited >= 2000 || depth > 3 {
				return
			}
			visited++
			if paths.FileExists(filepath.Join(dir, ".git")) {
				if r, ok := sshRemotes(env, dir); ok {
					found = append(found, r)
				}
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() && !strings.HasPrefix(name, ".") && name != "node_modules" && name != "vendor" {
					walk(filepath.Join(dir, name), depth+1)
				}
			}
		}
		walk(strings.TrimSuffix(env.Expand(f), "/"), 0)
	}
	return found
}

// sshRemotes checks a repo's remotes. ok is true when one uses SSH.
func sshRemotes(env *paths.Env, dir string) (repo sshRepo, ok bool) {
	repo.dir = env.Shorten(dir)
	var fixes []string
	for _, r := range hosts.Remotes(dir) {
		if hosts.IsHTTP(r.FetchURL) && hosts.IsHTTP(r.PushURL) {
			continue
		}
		if hosts.SSHHost(r.FetchURL) == "" && hosts.SSHHost(r.PushURL) == "" {
			continue
		}
		ok = true
		if fix := hosts.SwitchToHTTPS(r, repo.dir); fix != "" {
			fixes = append(fixes, fix)
		}
	}
	repo.fix = strings.Join(fixes, " && ")
	return repo, ok
}

// Plural counts n of a word: "1 problem", "2 problems".
func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
