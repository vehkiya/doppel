package main

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

	// file is the account file this account was loaded from, "" for a new
	// account. It differs from the account's path after a rename.
	file string
}

// Keys doppel manages in an account file. Everything else in the file is
// left alone, so an account can carry extra settings of its own.
const (
	keyAccount    = "doppel.account"
	keyDefault    = "doppel.default"
	keyHost       = "doppel.host"
	keyGitHubUser = "doppel.githubUser"
	keyFolder     = "doppel.folder"
	keyAuthKey    = "doppel.authKey"
	keyName       = "user.name"
	keyEmail      = "user.email"
	keySigningKey = "user.signingkey"
	keyGPGFormat  = "gpg.format"
	keyCommitSign = "commit.gpgsign"
	keyTagSign    = "tag.gpgsign"
	keySSHCommand = "core.sshCommand"
)

const defaultHost = "github.com"

// setting is one managed key and the values an account file should hold
// for it. No values means the key should be absent.
type setting struct {
	key    string
	values []string
}

// settings lists every key doppel manages in an account file, in file order.
// The Git keys are always written, even when they only reset a value: Git
// applies the default account first and the folder account on top, so a key
// the folder account left out would leak in from the default account.
func (a *Account) settings() []setting {
	optional := func(v string) []string {
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return []setting{
		{keyAccount, []string{a.ID}},
		{keyDefault, []string{strconv.FormatBool(a.Default)}},
		{keyHost, a.Hosts},
		{keyGitHubUser, optional(a.GitHubUser)},
		{keyFolder, a.Folders},
		{keyAuthKey, optional(a.AuthKey)},
		{keyName, []string{a.Name}},
		{keyEmail, []string{a.Email}},
		{keySigningKey, []string{a.SigningKey}},
		{keyGPGFormat, []string{"ssh"}},
		{keyCommitSign, []string{strconv.FormatBool(a.SigningKey != "" && a.SignCommits)}},
		{keyTagSign, []string{strconv.FormatBool(a.SigningKey != "" && a.SignTags)}},
		{keySSHCommand, []string{sshCommand(a.AuthKey)}},
	}
}

// sshCommand is the core.sshCommand for an auth key. IdentitiesOnly stops
// ssh-agent from offering another account's key first, since GitHub logs in
// as whichever account owns the first key that works. With no key it's plain
// ssh, so the account never inherits another account's key.
func sshCommand(key string) string {
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

// parseBool reads a Git boolean.
func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// accountFromConfig builds an account from the values in its file. Keys
// with a one-to-one Git setting are read back, so hand edits to them stick;
// core.sshCommand is always regenerated from doppel.authKey.
func accountFromConfig(id, file string, values map[string][]string) *Account {
	last := func(key string) string {
		v := values[strings.ToLower(key)]
		if len(v) == 0 {
			return ""
		}
		return v[len(v)-1]
	}
	a := &Account{
		ID:          id,
		Name:        last(keyName),
		Email:       last(keyEmail),
		Hosts:       values[strings.ToLower(keyHost)],
		GitHubUser:  last(keyGitHubUser),
		Default:     parseBool(last(keyDefault)),
		AuthKey:     last(keyAuthKey),
		SigningKey:  last(keySigningKey),
		SignCommits: parseBool(last(keyCommitSign)),
		SignTags:    parseBool(last(keyTagSign)),
		file:        file,
	}
	for _, f := range values[strings.ToLower(keyFolder)] {
		if !strings.HasSuffix(f, "/") {
			f += "/"
		}
		a.Folders = append(a.Folders, f)
	}
	return a
}

// loadAccounts reads every account file, sorted by ID.
func loadAccounts(env *Env) ([]*Account, error) {
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
		if err := validateID(id); err != nil {
			return nil, fmt.Errorf("%s: %w; rename the file", env.Shorten(path), err)
		}
		values, err := readConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", env.Shorten(path), err)
		}
		accounts = append(accounts, accountFromConfig(id, path, values))
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts, nil
}

func findAccount(accounts []*Account, id string) *Account {
	for _, a := range accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func defaultAccount(accounts []*Account) *Account {
	for _, a := range accounts {
		if a.Default {
			return a
		}
	}
	return nil
}

// setDefault makes acc the only default account; nil leaves none.
func setDefault(accounts []*Account, acc *Account) {
	for _, a := range accounts {
		a.Default = a == acc
	}
}

// folderOwner returns the account a folder is bound to.
func folderOwner(env *Env, accounts []*Account, folder string) *Account {
	for _, a := range accounts {
		for _, f := range a.Folders {
			if env.SamePath(f, folder) {
				return a
			}
		}
	}
	return nil
}

func (a *Account) removeFolder(env *Env, folder string) {
	a.Folders = slices.DeleteFunc(a.Folders, func(f string) bool { return env.SamePath(f, folder) })
}

var (
	idPattern         = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	hostPattern       = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	githubUserPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,38})$`)
)

func validateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("account ID %q must be 1-32 lowercase letters, digits or dashes, not starting or ending with a dash", id)
	}
	return nil
}

func (a *Account) validate() error {
	if err := validateID(a.ID); err != nil {
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
	if !strings.Contains(a.Email, "@") || strings.ContainsAny(a.Email, " \t\n<>") {
		return fmt.Errorf("account %s: %q isn't a valid email address", a.ID, a.Email)
	}
	for _, h := range a.Hosts {
		if !hostPattern.MatchString(h) {
			return fmt.Errorf("account %s: %q isn't a valid host name", a.ID, h)
		}
	}
	if a.GitHubUser != "" && !githubUserPattern.MatchString(a.GitHubUser) {
		return fmt.Errorf("account %s: %q isn't a valid GitHub username", a.ID, a.GitHubUser)
	}
	return nil
}

// validateAccounts checks each account and the rules that span accounts:
// unique IDs, at most one default, and each folder bound to one account.
func validateAccounts(env *Env, accounts []*Account) error {
	ids := map[string]bool{}
	var defaults []string
	for _, a := range accounts {
		if err := a.validate(); err != nil {
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
