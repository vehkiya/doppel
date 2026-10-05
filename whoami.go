package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const whoamiUsage = "doppel whoami [path]"

// Settings whoami checks come from the account in effect.
var identityKeys = []string{keyName, keyEmail, keySigningKey, keyGPGFormat, keyCommitSign, keyTagSign, keySSHCommand}

func (a *app) cmdWhoami(args []string) int {
	positional, code, ok := a.parseCommand(newFlagSet("whoami"), args, whoamiUsage)
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
	accounts, err := loadAccounts(a.env)
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
			return a.whoamiOutsideRepo(accounts, path)
		}
	}
	gitDir, err := runGit(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return a.whoamiOutsideRepo(accounts, path)
	}
	return a.whoamiInRepo(accounts, dir, strings.TrimSpace(gitDir))
}

// whoamiInRepo asks Git which account applies, so the answer always matches
// what Git does, then explains it.
func (a *app) whoamiInRepo(accounts []*Account, path, gitDir string) int {
	out, err := runGit(path, "config", "--show-scope", "--show-origin", "--list", "--null")
	if err != nil {
		return a.fail(err)
	}
	effective := map[string]configEntry{}
	for _, e := range parseConfigList(out, true) {
		effective[e.Key] = e // the last value wins, as in Git
	}
	value := func(key string) string { return effective[strings.ToLower(key)].Value }

	top, err := runGit(path, "rev-parse", "--show-toplevel")
	repo := strings.TrimSpace(top)
	if err != nil || repo == "" {
		repo = filepath.Dir(gitDir) // a bare repo has no working tree
	}
	a.row("Repo", a.env.Shorten(repo))

	id := value(keyAccount)
	acc := findAccount(accounts, id)
	switch {
	case id == "":
		a.row("Account", styleWarn.Render("none"))
	case acc == nil:
		a.row("Account", styleWarn.Render(id+" (no such account file)"))
	default:
		real := gitDir
		if resolved, err := filepath.EvalSymlinks(gitDir); err == nil {
			real = resolved
		}
		a.row("Account", styleAccent.Render(acc.ID)+styleDim.Render(" ("+a.ruleFor(acc, real)+")"))
	}
	a.row("Name", value(keyName))
	a.row("Email", value(keyEmail))
	if ssh := value(keySSHCommand); ssh == "ssh" {
		a.row("SSH", "your default SSH keys")
	} else {
		a.row("SSH", ssh)
	}
	if parseBool(value(keyCommitSign)) || parseBool(value(keyTagSign)) {
		a.row("Signing", fmt.Sprintf("%s · %s", signingScope(value(keyCommitSign), value(keyTagSign)), value(keySigningKey)))
	} else {
		a.row("Signing", "off")
	}

	// path may be a plain folder inside an enclosing repo, such as a home
	// directory managed with yadm. A repo created there gets its own
	// account, so say so when that differs from the enclosing repo's.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if top, err := filepath.EvalSymlinks(repo); err == nil && real != top {
			if newID, rule := a.folderRule(accounts, real); newID != id && newID != "" {
				a.row("New repos", styleAccent.Render(newID)+styleDim.Render(" ("+rule+")"))
			}
		}
	}

	if id == "" {
		a.printf("\n")
		if defaultAccount(accounts) == nil {
			a.notef("No doppel account applies here. Bind this repo's folder with `doppel bind <id> <folder>`, or set a default with `doppel default <id>`.")
		}
		return 0
	}
	a.warnOverrides(effective)
	return 0
}

// ruleFor explains why acc applies to a repo: the most specific of its
// folders that holds the repo, or being the default account.
func (a *app) ruleFor(acc *Account, gitDir string) string {
	if folder, ok := a.deepestFolder(acc.Folders, gitDir); ok {
		return "folder " + folder
	}
	switch {
	case acc.Default:
		return "default account"
	}
	return "matched by Git"
}

// deepestFolder returns the most specific of folders that holds path.
func (a *app) deepestFolder(folders []string, path string) (string, bool) {
	best, found := "", false
	for _, f := range folders {
		if a.env.FolderContains(f, path) && (!found || a.env.folderDepth(f) > a.env.folderDepth(best)) {
			best, found = f, true
		}
	}
	return best, found
}

// warnOverrides points out identity settings that don't come from a doppel
// account file, such as a repo's own `git config --local` values or a global
// setting placed after doppel's include.
func (a *app) warnOverrides(effective map[string]configEntry) {
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
func (a *app) whoamiOutsideRepo(accounts []*Account, path string) int {
	real, _ := resolveExisting(filepath.Clean(path))
	note := " (not a Git repository)"
	if _, err := os.Stat(path); err != nil { //nolint:gosec // the user's own path argument; only its existence is read
		note = " (doesn't exist yet)"
	}
	a.row("Path", a.env.Shorten(real)+styleDim.Render(note))
	if id, rule := a.folderRule(accounts, real); id != "" {
		a.row("New repos", styleAccent.Render(id)+styleDim.Render(" ("+rule+")"))
	} else {
		a.row("New repos", styleWarn.Render("no doppel account")+styleDim.Render(" (your global Git config applies)"))
	}
	return 0
}

// folderRule returns the account a new repo at path (a real path) would get,
// and the rule that picks it: the most specific bound folder, or else the
// default account. It returns "" when no account applies.
func (a *app) folderRule(accounts []*Account, path string) (id, rule string) {
	var match *Account
	best := ""
	for _, acc := range accounts {
		if f, ok := a.deepestFolder(acc.Folders, path); ok && (match == nil || a.env.folderDepth(f) > a.env.folderDepth(best)) {
			match, best = acc, f
		}
	}
	if match != nil {
		return match.ID, "folder " + best
	}
	if def := defaultAccount(accounts); def != nil {
		return def.ID, "default account"
	}
	return "", ""
}

func (a *app) row(label, value string) {
	a.printf("%s %s\n", styleLabel.Render(fmt.Sprintf("%-9s", label)), value)
}

func signingScope(commits, tags string) string {
	switch {
	case parseBool(commits) && parseBool(tags):
		return "commits and tags"
	case parseBool(commits):
		return "commits"
	}
	return "tags"
}
