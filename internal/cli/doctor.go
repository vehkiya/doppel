package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/plan"
	"github.com/vehkiya/doppel/internal/store"
	"github.com/vehkiya/doppel/internal/ui"
)

const doctorUsage = "doppel doctor [--fix]"

// report collects doctor's findings. A problem means Git may use the wrong
// account or fail; a warning is worth knowing but works.
type report struct {
	a                  *app
	problems, warnings int
}

func (r *report) section(title string) { r.a.printf("\n%s\n", ui.Title.Render(title)) }

func (r *report) ok(format string, args ...any) {
	r.a.printf("  %s %s\n", ui.OK.Render("✓"), fmt.Sprintf(format, args...))
}

func (r *report) note(format string, args ...any) {
	r.a.printf("  %s %s\n", ui.Dim.Render("–"), fmt.Sprintf(format, args...))
}

func (r *report) warn(fix, format string, args ...any) {
	r.warnings++
	r.line(ui.Warn.Render("⚠"), fix, format, args...)
}

func (r *report) problem(fix, format string, args ...any) {
	r.problems++
	r.line(ui.Error.Render("✗"), fix, format, args...)
}

func (r *report) line(mark, fix, format string, args ...any) {
	r.a.printf("  %s %s\n", mark, fmt.Sprintf(format, args...))
	if fix != "" {
		r.a.printf("    %s\n", ui.Dim.Render("↳ "+fix))
	}
}

// cmdDoctor checks everything that could make Git use the wrong account,
// and says how to fix each problem. --fix redoes doppel's own files.
func (a *app) cmdDoctor(args []string) int {
	fs := newFlagSet("doctor")
	var fix bool
	fs.BoolVar(&fix, "fix", false, "bring doppel's files up to date")
	positional, code, ok := a.parseCommand(fs, args, doctorUsage)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		return a.usageError(doctorUsage)
	}
	if fix {
		if err := a.lockWrites(); err != nil {
			return a.fail(err)
		}
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	r := &report{a: a}

	r.section("Git and SSH")
	r.ok("Git %s", git.Version())
	switch major, minor, ok := keys.OpenSSHVersion(); {
	case !ok:
		r.warn("", "Couldn't tell which OpenSSH is installed; SSH signing needs 8.2 or newer")
	case major < 8 || (major == 8 && minor < 2):
		r.problem("Update OpenSSH", "OpenSSH %d.%d can't verify SSH signatures; Git needs 8.2 or newer", major, minor)
	default:
		r.ok("OpenSSH %d.%d", major, minor)
	}
	overridden := false
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if v := os.Getenv(name); v != "" {
			r.problem("Unset it in your shell profile", "%s=%s is set, so every account uses it instead of its own key", name, v)
			overridden = true
		}
	}
	if !overridden {
		r.ok("GIT_SSH_COMMAND and GIT_SSH aren't set")
	}
	if a.env.GOOS == "darwin" {
		if a.macKeychain() {
			r.ok("ssh is Apple's, so it can keep passphrases in the Keychain")
		} else {
			ssh, _ := exec.LookPath("ssh")
			r.warn("Put /usr/bin ahead of it on your PATH, or keep your keys in an agent such as 1Password",
				"ssh (%s) isn't Apple's, so it can't keep passphrases in the Keychain; macOS asks for them in the terminal", a.env.Shorten(ssh))
		}
	}

	r.section("Git config")
	if len(list) == 0 {
		r.note("No accounts yet. Add one with `doppel add`.")
	} else {
		a.doctorFiles(r, list, fix)
		if accounts.Default(list) == nil {
			r.note("No default account: repos outside every folder use your global Git identity")
		}
	}

	for _, acc := range list {
		r.section("Account " + acc.ID)
		a.doctorAccount(r, acc, list)
	}

	a.printf("\n")
	switch {
	case r.problems == 0 && r.warnings == 0:
		a.successf("No problems found")
	default:
		a.printf("%s, %s\n", plural(r.problems, "problem"), plural(r.warnings, "warning"))
	}
	if r.problems > 0 {
		return 1
	}
	return 0
}

// doctorFiles checks doppel's place in the global Git config and that its
// files say what the accounts say. With fix, it rewrites them first.
func (a *app) doctorFiles(r *report, list []*accounts.Account, fix bool) {
	p := plan.New(a.env.StagingDir())
	defer p.Close()
	// Save validates the accounts first, so a hand-edited folder that would
	// break Git's config is reported here rather than written by --fix.
	var outdated []string
	upToDate := false
	if err := store.Save(a.env, p, list, store.Options{}); err != nil {
		r.problem("", "Can't bring doppel's files up to date: %v", err)
	} else if changes, err := p.Changes(); err != nil {
		r.problem("", "%v", err)
	} else {
		upToDate = len(changes) == 0
		if len(changes) > 0 && fix {
			if _, err := p.Apply(); err != nil {
				r.problem("", "Couldn't update doppel's files: %v", err)
			} else {
				for _, c := range changes {
					r.ok("Updated %s", a.env.Shorten(c.Path))
				}
				upToDate = true
			}
		}
		if !upToDate {
			for _, c := range changes {
				outdated = append(outdated, a.env.Shorten(c.Path))
			}
		}
	}

	path, found, overriding, err := store.Overrides(a.env, accounts.IdentityKeys())
	short := a.env.Shorten(path)
	switch {
	case err != nil:
		r.problem("Fix the file so Git can read it", "Can't read %s: %v", short, err)
	case !found:
		r.problem("Run `doppel doctor --fix`", "%s doesn't include doppel's accounts, so Git doesn't use them", short)
	default:
		r.ok("%s includes doppel's accounts", short)
	}
	for _, key := range overriding {
		r.problem("Move it above doppel's include, or remove it: the accounts set it", "%s sets %s after doppel's include, overriding every account", short, key)
	}

	// doppel's own Git calls ignore the user's config, so only Git run
	// as the user can say whether it can read what doppel wrote.
	if paths.FileExists(a.env.GlobalConfigPath()) {
		if _, err := git.Run("", "config", "--global", "--includes", "--list"); err != nil {
			r.problem("Fix the file the error names; until then every Git command fails", "Git can't read your global config: %v", err)
		} else {
			r.ok("Git reads your global config and everything it includes")
		}
	}

	if len(outdated) > 0 {
		r.warn("Run `doppel doctor --fix`", "doppel's files don't match the accounts: %s", strings.Join(outdated, ", "))
	} else if upToDate && err == nil {
		r.ok("doppel's files match the accounts")
	}
}

// doctorAccount checks one account's keys, folders and SSH config.
func (a *app) doctorAccount(r *report, acc *accounts.Account, list []*accounts.Account) {
	if acc.AuthKey == "" {
		if others, host := keylessNeighbours(acc, list); len(others) > 0 {
			r.problem("Give each account a key of its own: doppel edit <id> --generate-auth-key",
				"%s and %s both log in to %s with ssh's own keys, so they log in as the same user", acc.ID, strings.Join(others, ", "), host)
		} else {
			r.note("Logs in with ssh's own keys")
		}
	} else {
		a.doctorKey(r, "Auth key", acc.AuthKey)
		a.doctorKeychain(r, acc)
		if other, host := a.sharedAuthKey(acc, list); other != "" {
			r.problem("Give each account a key of its own: doppel edit <id> --generate-auth-key",
				"%s and %s use the same auth key on %s, so one of them logs in as the other", acc.ID, other, host)
		}
	}

	switch {
	case acc.SigningKey == "":
		r.note("Doesn't sign")
	case acc.SigningKey.SameKey(acc.AuthKey):
		r.ok("Signs %s with the auth key", signingScope(fmt.Sprint(acc.SignCommits), fmt.Sprint(acc.SignTags)))
	default:
		if _, err := keys.ReadPublic(acc.SigningKey.Map(a.env.Expand)); err != nil {
			r.problem("doppel edit "+acc.ID+" --signing-key <key>, or --no-signing", "Signing key %s: %v", acc.SigningKey.Display(), err)
		} else {
			a.doctorKey(r, "Signing key", acc.SigningKey)
		}
	}

	for _, f := range acc.Folders {
		if !paths.FileExists(a.env.Expand(f)) {
			r.warn("", "Folder %s doesn't exist yet; its rule applies once it does", f)
		}
	}
	if len(acc.Folders) == 0 && !acc.Default {
		r.note("No folders, and it isn't the default, so no repo uses it yet")
	}

	if cfg := filepath.Join(a.env.Home, ".ssh", "config"); paths.FileExists(cfg) {
		own := acc.AuthKey.Map(a.env.Expand)
		for _, h := range acc.Hosts {
			var extra []string
			for _, f := range keys.HostIdentityFiles(cfg, h) {
				if !keys.Ref(f).Map(a.env.Expand).SameKey(own) {
					extra = append(extra, a.env.Shorten(a.env.Expand(f)))
				}
			}
			if len(extra) > 0 {
				r.warn("Remove IdentityFile from the "+h+" entry in ~/.ssh/config; doppel's accounts pick the key",
					"~/.ssh/config also offers %s for %s. If the account's key were rejected, ssh would try that and could log in as someone else", strings.Join(extra, ", "), h)
			}
		}
	}

	if repos := a.httpsRepos(acc.Folders); len(repos) > 0 {
		shown := repos
		if len(shown) > 3 {
			shown = append(shown[:3:3], "…")
		}
		r.warn("Switch them to SSH, for example: git remote set-url origin git@github.com:<owner>/<repo>.git",
			"%s in its folders %s over HTTPS, which doppel's keys don't cover: %s",
			plural(len(repos), "repo"), map[bool]string{true: "fetches", false: "fetch"}[len(repos) == 1], strings.Join(shown, ", "))
	}

	if slices.ContainsFunc(acc.Hosts, a.isGitHub) && acc.GitHubUser == "" {
		r.note("Set its GitHub user (doppel edit %s --github-user <user>) so test can check logins and upload can add keys", acc.ID)
	}
}

// doctorKey checks how a key is kept.
func (a *app) doctorKey(r *report, label string, key keys.Ref) {
	path := key.Map(a.env.Expand)
	loaded, running := keys.InAgent(path)
	switch keys.CheckProtection(path) {
	case keys.Unknown:
		r.problem("Point the account at an existing key with `doppel edit`", "%s %s doesn't exist", label, key.Display())
	case keys.Unencrypted:
		r.warn("Add a passphrase: ssh-keygen -p -f "+key.PrivatePath(), "%s %s has no passphrase, so anyone who copies it can use it", label, key.Display())
	case keys.HardwareKey:
		r.ok("%s %s is on a security key", label, key.Display())
	case keys.AgentOnly:
		if running && !loaded {
			r.warn("Unlock it in your agent (1Password, Proton Pass, or ssh-add)", "%s %s lives in an agent, but the agent doesn't hold it right now", label, key.Display())
		} else {
			r.ok("%s %s (private key in an agent)", label, key.Display())
		}
	default:
		status := "passphrase"
		if loaded {
			status += ", in agent"
		}
		r.ok("%s %s (%s)", label, key.Display(), status)
	}
}

// doctorKeychain checks, on a Mac, that ssh keeps the passphrase of acc's
// auth key in the Keychain and loads the key into the agent, so neither
// Git nor signing asks for it in the terminal.
func (a *app) doctorKeychain(r *report, acc *accounts.Account) {
	if !a.macKeychain() || keys.CheckProtection(acc.AuthKey.Map(a.env.Expand)) != keys.Encrypted {
		return
	}
	cfg := filepath.Join(a.env.Home, ".ssh", "config")
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
			strings.Join(missing, " and "), h, a.loadCommand(acc.AuthKey))
		if !keychain {
			r.warn(fix, "~/.ssh/config doesn't set %s for %s, so macOS asks for the passphrase of %s in the terminal",
				strings.Join(missing, " or "), h, acc.AuthKey)
		} else {
			r.warn(fix, "~/.ssh/config doesn't set AddKeysToAgent for %s, so ssh doesn't load %s into the agent, and signing with it asks for its passphrase",
				h, acc.AuthKey)
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
		if !after || other.AuthKey != "" {
			continue
		}
		if h := sharedHost(acc, other); h != "" {
			others, host = append(others, other.ID), h
		}
	}
	return others, host
}

// sharedAuthKey finds an account after acc using the same auth key on a
// shared host, so the pair is reported once.
func (a *app) sharedAuthKey(acc *accounts.Account, list []*accounts.Account) (string, string) {
	mine, err := keys.Fingerprint(acc.AuthKey.Map(a.env.Expand))
	if err != nil {
		return "", ""
	}
	after := false
	for _, other := range list {
		if other == acc {
			after = true
			continue
		}
		if !after || other.AuthKey == "" {
			continue
		}
		host := sharedHost(acc, other)
		if fp, err := keys.Fingerprint(other.AuthKey.Map(a.env.Expand)); err == nil && host != "" && fp == mine {
			return other.ID, host
		}
	}
	return "", ""
}

// httpsRepos finds repos in folders that fetch over HTTPS. It looks three
// levels deep and skips hidden and dependency folders, so big trees stay quick.
func (a *app) httpsRepos(folders []string) []string {
	var found []string
	for _, f := range folders {
		visited := 0
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			if visited >= 2000 || depth > 3 {
				return
			}
			visited++
			if paths.FileExists(filepath.Join(dir, ".git")) {
				if out, err := git.Run(dir, "remote", "-v"); err == nil && (strings.Contains(out, "https://") || strings.Contains(out, "http://")) {
					found = append(found, a.env.Shorten(dir))
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
		walk(strings.TrimSuffix(a.env.Expand(f), "/"), 0)
	}
	return found
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
