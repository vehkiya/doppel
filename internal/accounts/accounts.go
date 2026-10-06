// Package accounts defines doppel's accounts: the settings each account file
// holds, and loading and validating them.
package accounts

import (
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/vehkiya/doppel/internal/keys"
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
	AuthKey     keys.Ref // private key (or agent-held .pub) for SSH; "" for ssh's own defaults
	SigningKey  keys.Ref // public half of the signing key; "" when the account doesn't sign
	SignCommits bool
	SignTags    bool

	// File is the account file this account was loaded from, "" for a new
	// account. It differs from the account's path after a rename.
	File string
	// Checksum identifies the content File had when it was loaded, so a
	// command can tell before writing that someone changed the file since.
	Checksum string
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

// ManagedKey is one key doppel manages in every account file. Managed lists
// them all, so adding a managed key means adding it there and nowhere else.
type ManagedKey struct {
	Key string
	// Identity marks a Git setting that decides who commits, signs or logs
	// in. whoami and doctor point out a value for it that comes from outside
	// doppel's account files, and FromGlobal reads them for a first account.
	Identity bool
	// value is what an account file holds for the key: no values means the
	// key is absent. A Git key always has a value, even one that only resets
	// it, so it can't leak in from the default account.
	value func(a *Account) []string
	// read sets the account from the values in its file, so hand edits stick.
	// It's nil for keys that are always derived from others, such as
	// core.sshCommand, and for doppel.account, which the file name gives.
	read func(a *Account, values []string)
}

// ReadBack reports whether the key is read back from the account file, rather
// than derived from other keys and overwritten on every save.
func (k ManagedKey) ReadBack() bool { return k.read != nil }

// Managed lists every key doppel manages in an account file, in file order.
var Managed = []ManagedKey{
	{Key: KeyAccount, value: func(a *Account) []string { return []string{a.ID} }},
	{Key: KeyDefault, value: func(a *Account) []string { return []string{strconv.FormatBool(a.Default)} },
		read: func(a *Account, v []string) { a.Default = ParseBool(last(v)) }},
	{Key: KeyHost, value: func(a *Account) []string { return a.Hosts },
		read: func(a *Account, v []string) { a.Hosts = v }},
	{Key: KeyGitHubUser, value: func(a *Account) []string { return optional(a.GitHubUser) },
		read: func(a *Account, v []string) { a.GitHubUser = last(v) }},
	{Key: KeyFolder, value: func(a *Account) []string { return a.Folders },
		read: func(a *Account, v []string) {
			for _, f := range v {
				if !strings.HasSuffix(f, "/") {
					f += "/"
				}
				a.Folders = append(a.Folders, f)
			}
		}},
	{Key: KeyAuthKey, value: func(a *Account) []string { return optional(string(a.AuthKey)) },
		read: func(a *Account, v []string) { a.AuthKey = keys.Ref(last(v)) }},
	{Key: KeyName, Identity: true, value: func(a *Account) []string { return []string{a.Name} },
		read: func(a *Account, v []string) { a.Name = last(v) }},
	{Key: KeyEmail, Identity: true, value: func(a *Account) []string { return []string{a.Email} },
		read: func(a *Account, v []string) { a.Email = last(v) }},
	{Key: KeySigningKey, Identity: true, value: func(a *Account) []string { return []string{string(a.SigningKey)} },
		read: func(a *Account, v []string) { a.SigningKey = keys.Ref(last(v)) }},
	{Key: KeyGPGFormat, Identity: true, value: func(*Account) []string { return []string{"ssh"} }},
	{Key: KeyCommitSign, Identity: true, value: func(a *Account) []string {
		return []string{strconv.FormatBool(a.SigningKey != "" && a.SignCommits)}
	}, read: func(a *Account, v []string) { a.SignCommits = ParseBool(last(v)) }},
	{Key: KeyTagSign, Identity: true, value: func(a *Account) []string {
		return []string{strconv.FormatBool(a.SigningKey != "" && a.SignTags)}
	}, read: func(a *Account, v []string) { a.SignTags = ParseBool(last(v)) }},
	{Key: KeySSHCommand, Identity: true, value: func(a *Account) []string { return []string{SSHCommand(a.AuthKey)} }},
}

// IdentityKeys lists the managed Git settings that decide who commits, signs
// or logs in (ManagedKey.Identity), in file order.
func IdentityKeys() []string {
	var list []string
	for _, k := range Managed {
		if k.Identity {
			list = append(list, k.Key)
		}
	}
	return list
}

// Settings lists the values an account file should hold for every managed
// key, in file order.
func (a *Account) Settings() []Setting {
	settings := make([]Setting, len(Managed))
	for i, k := range Managed {
		settings[i] = Setting{k.Key, k.value(a)}
	}
	return settings
}

func optional(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

// last is the value Git uses for a single-valued key: the last one.
func last(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

// SSHCommand is the core.sshCommand for an auth key. IdentitiesOnly stops
// ssh-agent from offering another account's key first, since GitHub logs in
// as whichever account owns the first key that works. With no key it's plain
// ssh, so the account never inherits another account's key.
func SSHCommand(key keys.Ref) string {
	if key == "" {
		return "ssh"
	}
	return "ssh -i " + shellQuote(string(key)) + " -o IdentitiesOnly=yes"
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

// fromConfig builds an account from the values in its file, through the
// keys Managed reads back.
func fromConfig(id, file string, values map[string][]string) *Account {
	a := &Account{ID: id, File: file}
	for _, k := range Managed {
		if k.read != nil {
			k.read(a, values[strings.ToLower(k.Key)])
		}
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
		// Read the bytes before parsing them: a change in between then
		// shows up as a stale checksum rather than going unnoticed.
		sum, err := FileChecksum(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", env.Shorten(path), err)
		}
		values, err := git.ReadConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", env.Shorten(path), err)
		}
		acc := fromConfig(id, path, values)
		acc.Checksum = sum
		accounts = append(accounts, acc)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts, nil
}

// FileChecksum identifies a file's content. A file that doesn't exist has
// the checksum "".
func FileChecksum(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // an account file inside doppel's directory
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
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
			where := ""
			if a.File != "" {
				where = " in " + a.File
			}
			return fmt.Errorf("account %s: doppel.folder %q%s: %w (fix it in the account file, or run `doppel unbind`)", a.ID, f, where, err)
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
