// Package accounts defines doppel's accounts: the settings each account file
// holds, and loading and validating them.
package accounts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/paths"
)

// Account is one Git identity and the folders where it applies.
type Account struct {
	ID          string
	Name        string
	Email       string
	Hosts       []string
	GitHubUser  string
	Folders     []string // stored form: real path, "~/"-shortened, ending in "/"
	Default     bool
	AuthKey     string // private key (or agent-held .pub) for SSH; "" for ssh's own defaults
	SigningKey  string // public key used for signing; "" when the account doesn't sign
	SignCommits bool
	SignTags    bool

	// File is the account file this account was loaded from, "" for a new
	// account. It differs from the account's path after a rename.
	File string
}

// Keys doppel manages in an account file. Everything else in the file is
// left alone, so an account can carry extra settings of its own.
const (
	KeyAccount    = "doppel.account"
	KeyDefault    = "doppel.default"
	KeyHost       = "doppel.host"
	KeyGitHubUser = "doppel.githubUser"
	KeyFolder     = "doppel.folder"
	KeyAuthKey    = "doppel.authKey"
	KeyName       = "user.name"
	KeyEmail      = "user.email"
	KeySigningKey = "user.signingkey"
	KeyGPGFormat  = "gpg.format"
	KeyCommitSign = "commit.gpgsign"
	KeyTagSign    = "tag.gpgsign"
	KeySSHCommand = "core.sshCommand"
)

// DefaultHost is an account's host unless it names others.
const DefaultHost = "github.com"

// Setting is one managed key and the values an account file should hold
// for it. No values means the key should be absent.
type Setting struct {
	Key    string
	Values []string
}

// Settings lists every key doppel manages in an account file, in file order.
// The Git keys are always written, even when they only reset a value: Git
// applies the default account first and the folder account on top, so a key
// the folder account left out would leak in from the default account.
func (a *Account) Settings() []Setting {
	optional := func(v string) []string {
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return []Setting{
		{KeyAccount, []string{a.ID}},
		{KeyDefault, []string{strconv.FormatBool(a.Default)}},
		{KeyHost, a.Hosts},
		{KeyGitHubUser, optional(a.GitHubUser)},
		{KeyFolder, a.Folders},
		{KeyAuthKey, optional(a.AuthKey)},
		{KeyName, []string{a.Name}},
		{KeyEmail, []string{a.Email}},
		{KeySigningKey, []string{a.SigningKey}},
		{KeyGPGFormat, []string{"ssh"}},
		{KeyCommitSign, []string{strconv.FormatBool(a.SigningKey != "" && a.SignCommits)}},
		{KeyTagSign, []string{strconv.FormatBool(a.SigningKey != "" && a.SignTags)}},
		{KeySSHCommand, []string{SSHCommand(a.AuthKey)}},
	}
}

// SSHCommand is the core.sshCommand for an auth key. IdentitiesOnly stops
// ssh-agent from offering another account's key first, since GitHub logs in
// as whichever account owns the first key that works. With no key it's plain
// ssh, so the account never inherits another account's key.
func SSHCommand(key string) string {
	if key == "" {
		return "ssh"
	}
	return "ssh -i " + shellQuote(key) + " -o IdentitiesOnly=yes"
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./~@%+=:,-]+$`)

// shellQuote leaves plain paths as they are, so the shell can expand a
// leading "~/", and single-quotes anything else (ssh expands "~" in -i itself).
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ParseBool reads a Git boolean.
func ParseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// fromConfig builds an account from the values in its file. Keys
// with a one-to-one Git setting are read back, so hand edits to them stick;
// core.sshCommand is always regenerated from doppel.authKey.
func fromConfig(id, file string, values map[string][]string) *Account {
	last := func(key string) string {
		v := values[strings.ToLower(key)]
		if len(v) == 0 {
			return ""
		}
		return v[len(v)-1]
	}
	a := &Account{
		ID:          id,
		Name:        last(KeyName),
		Email:       last(KeyEmail),
		Hosts:       values[strings.ToLower(KeyHost)],
		GitHubUser:  last(KeyGitHubUser),
		Default:     ParseBool(last(KeyDefault)),
		AuthKey:     last(KeyAuthKey),
		SigningKey:  last(KeySigningKey),
		SignCommits: ParseBool(last(KeyCommitSign)),
		SignTags:    ParseBool(last(KeyTagSign)),
		File:        file,
	}
	for _, f := range values[strings.ToLower(KeyFolder)] {
		if !strings.HasSuffix(f, "/") {
			f += "/"
		}
		a.Folders = append(a.Folders, f)
	}
	return a
}

// Load reads every account file, sorted by ID.
func Load(env *paths.Env) ([]*Account, error) {
	entries, err := os.ReadDir(env.AccountsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var accounts []*Account
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".gitconfig") {
			continue
		}
		id := strings.TrimSuffix(name, ".gitconfig")
		path := filepath.Join(env.AccountsDir(), name)
		if err := ValidateID(id); err != nil {
			return nil, fmt.Errorf("%s: %w; rename the file", env.Shorten(path), err)
		}
		values, err := git.ReadConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", env.Shorten(path), err)
		}
		accounts = append(accounts, fromConfig(id, path, values))
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts, nil
}

// Find returns the account named id, or nil.
func Find(accounts []*Account, id string) *Account {
	for _, a := range accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// Default returns the default account, or nil.
func Default(accounts []*Account) *Account {
	for _, a := range accounts {
		if a.Default {
			return a
		}
	}
	return nil
}

// SetDefault makes acc the only default account; nil leaves none.
func SetDefault(accounts []*Account, acc *Account) {
	for _, a := range accounts {
		a.Default = a == acc
	}
}

// FolderOwner returns the account a folder is bound to.
func FolderOwner(env *paths.Env, accounts []*Account, folder string) *Account {
	for _, a := range accounts {
		for _, f := range a.Folders {
			if env.SamePath(f, folder) {
				return a
			}
		}
	}
	return nil
}

// RemoveFolder unbinds folder from the account.
func (a *Account) RemoveFolder(env *paths.Env, folder string) {
	a.Folders = slices.DeleteFunc(a.Folders, func(f string) bool { return env.SamePath(f, folder) })
}

var (
	idPattern         = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	hostPattern       = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	githubUserPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,38})$`)
)

// ValidateID checks an account ID, which is also its file name.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("account ID %q must be 1-32 lowercase letters, digits or dashes, not starting or ending with a dash", id)
	}
	return nil
}

// ValidateEmail checks a commit email address.
func ValidateEmail(email string) error {
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\n<>") {
		return fmt.Errorf("%q isn't a valid email address", email)
	}
	return nil
}

// ValidateHost checks a Git host name.
func ValidateHost(host string) error {
	if !hostPattern.MatchString(host) {
		return fmt.Errorf("%q isn't a valid host name", host)
	}
	return nil
}

// ValidateGitHubUser checks a GitHub username; "" (none) is fine.
func ValidateGitHubUser(user string) error {
	if user != "" && !githubUserPattern.MatchString(user) {
		return fmt.Errorf("%q isn't a valid GitHub username", user)
	}
	return nil
}

// Validate checks one account's fields.
func (a *Account) Validate() error {
	if err := ValidateID(a.ID); err != nil {
		return err
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("account %s needs a name (--name)", a.ID)
	}
	if strings.ContainsAny(a.Name, "\n<>") {
		return fmt.Errorf("account %s: name can't contain line breaks, < or >", a.ID)
	}
	if a.Email == "" {
		return fmt.Errorf("account %s needs an email (--email)", a.ID)
	}
	if err := ValidateEmail(a.Email); err != nil {
		return fmt.Errorf("account %s: %w", a.ID, err)
	}
	for _, h := range a.Hosts {
		if err := ValidateHost(h); err != nil {
			return fmt.Errorf("account %s: %w", a.ID, err)
		}
	}
	if err := ValidateGitHubUser(a.GitHubUser); err != nil {
		return fmt.Errorf("account %s: %w", a.ID, err)
	}
	for _, f := range a.Folders {
		if err := paths.ValidateFolder(f); err != nil {
			return fmt.Errorf("account %s: folder %q: %w (fix doppel.folder in the account file, or run `doppel unbind`)", a.ID, f, err)
		}
	}
	return nil
}

// ValidateAll checks each account and the rules that span accounts:
// unique IDs, at most one default, and each folder bound to one account.
func ValidateAll(env *paths.Env, accounts []*Account) error {
	ids := map[string]bool{}
	var defaults []string
	for _, a := range accounts {
		if err := a.Validate(); err != nil {
			return err
		}
		if ids[a.ID] {
			return fmt.Errorf("there are two accounts named %s", a.ID)
		}
		ids[a.ID] = true
		if a.Default {
			defaults = append(defaults, a.ID)
		}
	}
	if len(defaults) > 1 {
		return fmt.Errorf("accounts %s are all marked as the default; pick one with `doppel default <id>`", strings.Join(defaults, ", "))
	}
	for i, a := range accounts {
		for j, f := range a.Folders {
			for _, b := range accounts[i:] {
				for k, g := range b.Folders {
					if (b != a || k > j) && env.SamePath(f, g) {
						if a == b {
							return fmt.Errorf("account %s lists %s twice", a.ID, f)
						}
						return fmt.Errorf("%s is bound to both %s and %s", f, a.ID, b.ID)
					}
				}
			}
		}
	}
	return nil
}
