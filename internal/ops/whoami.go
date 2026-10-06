package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/hosts"
	"github.com/vehkiya/doppel/internal/paths"
)

// Identity is which account applies at a path, and why.
type Identity struct {
	// InRepo is true when the path is in a repo, so Git answered. Outside a
	// repo only Path, Exists and the New* fields are set.
	InRepo bool
	// Repo is the repo's top folder (its .git folder for a bare repo).
	Repo string
	// Path is the folder asked about: in a repo, the folder Git was asked
	// from; outside one, the path with symlinks resolved as far as it exists.
	Path   string
	Exists bool

	// AccountID is the account Git applies (doppel.account), "" for none.
	AccountID string
	// Account is that account, nil when no account file has that ID.
	Account *accounts.Account
	// Rule says why Git applied it: "folder <folder>", "default account",
	// or "matched by Git" for anything else, such as an index that doesn't
	// match the account files yet.
	Rule string

	// The values in effect, as Git reads them.
	Name, Email           string
	SSHCommand            string
	SigningKey            string
	SignCommits, SignTags bool
	// UsesAccountKey is true when core.sshCommand is the account's own, so
	// its auth key is the one in use.
	UsesAccountKey bool

	// Overrides are values in effect that don't come from a doppel account
	// file, such as a repo's own `git config --local` settings, a global
	// setting placed after doppel's include, or GIT_SSH_COMMAND.
	Overrides []string

	// NewRepoID is the account a repo created at the path would get, and
	// NewRepoRule why. Outside a repo it's always set (with "" for none);
	// in a repo, only for a folder inside an enclosing repo whose account
	// differs.
	NewRepoID, NewRepoRule string
}

// Whoami says which account applies at path. In a repo it asks Git, so the
// answer always matches what Git does. Elsewhere, such as a clone target
// that doesn't exist yet, it applies the folder rules itself: Git can only
// answer for an existing repo.
func Whoami(ctx Context, list []*accounts.Account, path string) (*Identity, error) {
	// Asking Git from an existing parent of a path that doesn't exist would
	// describe whatever repo encloses it, not the new repo.
	dir := filepath.Clean(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() { //nolint:gosec // the user's own path argument; only its type is read
		if err != nil {
			return outsideRepo(ctx, list, path), nil
		}
		dir = filepath.Dir(dir) // a file: describe the repo it's in
	}
	gitDir, err := git.Run(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return outsideRepo(ctx, list, path), nil
	}
	return inRepo(ctx, list, dir, strings.TrimSpace(gitDir))
}

func outsideRepo(ctx Context, list []*accounts.Account, path string) *Identity {
	real, _ := paths.ResolveExisting(filepath.Clean(path))
	_, err := os.Stat(path) //nolint:gosec // the user's own path argument; only its existence is read
	id := &Identity{Path: real, Exists: err == nil}
	id.NewRepoID, id.NewRepoRule = folderRule(ctx, list, real)
	return id
}

func inRepo(ctx Context, list []*accounts.Account, path, gitDir string) (*Identity, error) {
	out, err := git.Run(path, "config", "--show-scope", "--show-origin", "--list", "--null")
	if err != nil {
		return nil, err
	}
	effective := map[string]git.ConfigEntry{}
	for _, e := range git.ParseConfigList(out, true) {
		effective[e.Key] = e // the last value wins, as in Git
	}
	value := func(key string) string { return effective[strings.ToLower(key)].Value }

	id := &Identity{InRepo: true, Path: path, Exists: true}
	top, err := git.Run(path, "rev-parse", "--show-toplevel")
	id.Repo = strings.TrimSpace(top)
	if err != nil || id.Repo == "" {
		id.Repo = filepath.Dir(gitDir) // a bare repo has no working tree
	}

	id.AccountID = value(accounts.KeyAccount)
	id.Account = accounts.Find(list, id.AccountID)
	if id.Account != nil {
		real := gitDir
		if resolved, err := filepath.EvalSymlinks(gitDir); err == nil {
			real = resolved
		}
		id.Rule = ruleFor(ctx, list, id.Account, real)
	}
	id.Name, id.Email = value(accounts.KeyName), value(accounts.KeyEmail)
	id.SSHCommand = value(accounts.KeySSHCommand)
	id.UsesAccountKey = id.Account != nil && id.Account.AuthKey != "" && id.SSHCommand == accounts.SSHCommand(id.Account.AuthKey)
	id.SigningKey = value(accounts.KeySigningKey)
	id.SignCommits, id.SignTags = accounts.ParseBool(value(accounts.KeyCommitSign)), accounts.ParseBool(value(accounts.KeyTagSign))

	// path may be a plain folder inside an enclosing repo, such as a home
	// directory managed with yadm. A repo created there gets its own
	// account, so say so when that differs from the enclosing repo's.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if top, err := filepath.EvalSymlinks(id.Repo); err == nil && real != top {
			if newID, rule := folderRule(ctx, list, real); newID != id.AccountID && newID != "" {
				id.NewRepoID, id.NewRepoRule = newID, rule
			}
		}
	}

	if id.AccountID != "" {
		id.Overrides = overrides(ctx, effective)
	}
	return id, nil
}

// ruleFor explains why Git applied acc to a repo: the folder rule that
// picks it, or being the default account.
func ruleFor(ctx Context, list []*accounts.Account, acc *accounts.Account, gitDir string) string {
	if r, ok := accounts.MatchFolder(ctx.Env, list, gitDir); ok && r.ID == acc.ID {
		return "folder " + r.Folder
	}
	if acc.Default {
		return "default account"
	}
	return "matched by Git"
}

// folderRule returns the account a new repo at path (a real path) would get,
// and the rule that picks it: the folder rule Git would apply, or else the
// default account. It returns "" when no account applies.
func folderRule(ctx Context, list []*accounts.Account, path string) (id, rule string) {
	if r, ok := accounts.MatchFolder(ctx.Env, list, path); ok {
		return r.ID, "folder " + r.Folder
	}
	if def := accounts.Default(list); def != nil {
		return def.ID, "default account"
	}
	return "", ""
}

// overrides lists identity settings in effect that don't come from a doppel
// account file, and environment variables that override them.
func overrides(ctx Context, effective map[string]git.ConfigEntry) []string {
	var lines []string
	for _, key := range accounts.IdentityKeys() {
		e, ok := effective[strings.ToLower(key)]
		if !ok || isAccountFile(ctx.Env, e.Origin) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s = %s comes from %s (%s config)", key, e.Value, strings.TrimPrefix(e.Origin, "file:"), e.Scope))
	}
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if v := os.Getenv(name); v != "" {
			lines = append(lines, fmt.Sprintf("%s=%s is set in your environment and overrides the account's SSH command", name, v))
		}
	}
	return lines
}

// isAccountFile reports whether a --show-origin value names a doppel account file.
func isAccountFile(env *paths.Env, origin string) bool {
	path, ok := strings.CutPrefix(origin, "file:")
	if !ok {
		return false
	}
	return env.SamePath(filepath.Dir(path), env.AccountsDir())
}

// RemoteLogin tries acc's auth key on the host of the repo's remote, without
// letting anything ask: a passphrase prompt in whoami would get in the way.
// ok is false when the repo has no remote. A remote that isn't SSH, which
// doppel's keys don't cover, comes back as a skipped check naming its kind,
// with the command that switches an HTTPS remote to SSH.
func RemoteLogin(ctx Context, acc *accounts.Account, repo string) (c Check, ok bool) {
	remote, ok := hosts.MainRemote(repo)
	if !ok || remote.FetchURL == "" {
		return Check{}, false
	}
	host := hosts.SSHHost(remote.FetchURL)
	if host == "" {
		return Check{
			Skipped: true,
			Detail:  "the remote uses " + hosts.Scheme(remote.FetchURL) + ", which doppel's SSH keys don't cover",
			Fix:     hosts.SwitchToSSH(remote, ""),
		}, true
	}
	return Login(ctx, acc, host, true), true
}
