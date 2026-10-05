package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
)

// keyFlags are the key settings shared by add and edit.
type keyFlags struct {
	authKey, signingKey                                    string
	generateAuth, generateSigning, signWithAuth, noSigning bool
	signCommits, signTags                                  bool
}

func (k *keyFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&k.authKey, "auth-key", "", `SSH key for logging in to Git hosts ("" for ssh's own keys)`)
	fs.BoolVar(&k.generateAuth, "generate-auth-key", false, "generate a new auth key")
	fs.StringVar(&k.signingKey, "signing-key", "", "SSH key for signing commits")
	fs.BoolVar(&k.generateSigning, "generate-signing-key", false, "generate a separate signing key")
	fs.BoolVar(&k.signWithAuth, "sign-with-auth-key", false, "sign with the auth key")
	fs.BoolVar(&k.noSigning, "no-signing", false, "don't sign")
	fs.BoolVar(&k.signCommits, "sign-commits", true, "sign commits")
	fs.BoolVar(&k.signTags, "sign-tags", true, "sign tags")
}

// pendingKey is a key to generate before saving.
type pendingKey struct {
	path, comment, purpose string
}

// keyChanges says how to change an account's keys. Flags and the wizard
// both describe their choices this way.
type keyChanges struct {
	auth            *string // the new auth key as given; "" for ssh's own keys, nil to keep it
	generateAuth    bool
	signing         *string // the new signing key as given; "" to stop signing, nil to keep it
	generateSigning bool
	signWithAuth    bool
	signCommits     *bool // nil keeps the current setting
	signTags        *bool
}

// keyChangesFromFlags reads key changes from the command line.
func keyChangesFromFlags(fs *flag.FlagSet, k keyFlags) (keyChanges, error) {
	set := func(name string) bool { return flagWasSet(fs, name) }
	var ch keyChanges
	if set("auth-key") && k.generateAuth {
		return ch, errors.New("use either --auth-key or --generate-auth-key")
	}
	choices := 0
	for _, name := range []string{"signing-key", "generate-signing-key", "sign-with-auth-key", "no-signing"} {
		if set(name) {
			choices++
		}
	}
	if choices > 1 {
		return ch, errors.New("use only one of --signing-key, --generate-signing-key, --sign-with-auth-key and --no-signing")
	}
	if set("auth-key") {
		ch.auth = &k.authKey
	}
	ch.generateAuth = k.generateAuth
	switch {
	case set("signing-key"):
		ch.signing = &k.signingKey
	case k.noSigning:
		none := ""
		ch.signing = &none
	}
	ch.generateSigning, ch.signWithAuth = k.generateSigning, k.signWithAuth
	if set("sign-commits") {
		ch.signCommits = &k.signCommits
	}
	if set("sign-tags") {
		ch.signTags = &k.signTags
	}
	return ch, nil
}

// any reports whether ch changes anything.
func (ch keyChanges) any() bool {
	return ch.auth != nil || ch.generateAuth || ch.signing != nil || ch.generateSigning || ch.signWithAuth ||
		ch.signCommits != nil || ch.signTags != nil
}

// applyKeyChanges sets acc's keys. Keys to generate are returned instead of
// created, so a dry run can show them without writing.
func (a *app) applyKeyChanges(acc *accounts.Account, ch keyChanges) ([]pendingKey, error) {
	var pending []pendingKey
	sshDir := filepath.Join(a.env.Home, ".ssh")
	switch {
	case ch.auth != nil:
		key, err := a.keyPath(*ch.auth, false)
		if err != nil {
			return nil, err
		}
		acc.AuthKey = key
	case ch.generateAuth:
		path, err := newKeyPath(sshDir, acc.ID, false, "--auth-key")
		if err != nil {
			return nil, err
		}
		acc.AuthKey = a.env.Shorten(path)
		pending = append(pending, pendingKey{path, acc.Email, "auth key"})
	}

	wasSigning := acc.SigningKey != ""
	switch {
	case ch.signing != nil:
		key, err := a.keyPath(*ch.signing, true)
		if err != nil {
			return nil, err
		}
		acc.SigningKey = key
	case ch.generateSigning:
		path, err := newKeyPath(sshDir, acc.ID, true, "--signing-key")
		if err != nil {
			return nil, err
		}
		acc.SigningKey = a.env.Shorten(path) + ".pub"
		pending = append(pending, pendingKey{path, acc.Email, "signing key"})
	case ch.signWithAuth:
		if acc.AuthKey == "" {
			return nil, errors.New("--sign-with-auth-key needs an auth key; add one with --auth-key or --generate-auth-key")
		}
		acc.SigningKey = keys.PublicPath(acc.AuthKey)
	}
	if acc.SigningKey != "" && !wasSigning {
		acc.SignCommits, acc.SignTags = true, true
	}
	if ch.signCommits != nil {
		acc.SignCommits = *ch.signCommits
	}
	if ch.signTags != nil {
		acc.SignTags = *ch.signTags
	}
	return pending, nil
}

// newKeyPath is where a generated key goes. It refuses a path that's taken:
// doppel never overwrites keys.
func newKeyPath(sshDir, id string, signing bool, useFlag string) (string, error) {
	path := keys.DefaultPath(sshDir, id, signing)
	for _, p := range []string{path, path + ".pub"} {
		if _, err := os.Lstat(p); err == nil {
			return "", fmt.Errorf("%s already exists; use it with %s %s", p, useFlag, path)
		}
	}
	return path, nil
}

// keyPath checks a key given on the command line and returns its stored
// form, "~/"-shortened. An auth key is a private key, or a .pub whose
// private half lives in an agent. A signing key is stored as its .pub,
// which Git hands to ssh-keygen. "" means no key.
func (a *app) keyPath(input string, signing bool) (string, error) {
	if input == "" {
		return "", nil
	}
	path := a.env.Expand(input)
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.cwd, path)
	}
	path = filepath.Clean(path)
	if signing {
		path = keys.PublicPath(path)
	}
	if strings.HasSuffix(path, ".pub") {
		if _, err := keys.ReadPublic(path); err != nil {
			if os.IsNotExist(err) && signing {
				private := strings.TrimSuffix(path, ".pub")
				return "", fmt.Errorf("no public key at %s; create it with: ssh-keygen -y -f %s > %s", a.env.Shorten(path), a.env.Shorten(private), a.env.Shorten(path))
			}
			return "", fmt.Errorf("%s: %w", a.env.Shorten(path), err)
		}
	} else if info, err := os.Stat(path); err != nil || info.IsDir() { //nolint:gosec // a key path the user gave
		return "", fmt.Errorf("no key file at %s", a.env.Shorten(path))
	}
	return a.env.Shorten(path), nil
}

// generateKeys creates pending keys. ssh-keygen asks for each passphrase on
// the terminal, so this needs one.
func (a *app) generateKeys(pending []pendingKey) error {
	if len(pending) == 0 {
		return nil
	}
	if !a.interactive {
		return errors.New("generating a key needs a terminal, so ssh-keygen can ask for a passphrase. Generate it with ssh-keygen yourself and pass it with --auth-key or --signing-key")
	}
	for _, k := range pending {
		a.printf("Generating the %s %s. Choose a passphrase to protect it.\n", k.purpose, a.env.Shorten(k.path))
		if err := a.generate(k.path, k.comment); err != nil {
			return err
		}
		if keys.CheckProtection(k.path) == keys.Unencrypted {
			a.warnf("%s has no passphrase: anyone who copies it can use it. Add one with: ssh-keygen -p -f %s",
				a.env.Shorten(k.path), a.env.Shorten(k.path))
		}
	}
	return nil
}

// dryRunKeys stands in for keys a dry run would generate, so the
// allowed_signers diff can show where they'd go.
func (a *app) dryRunKeys(pending []pendingKey) func(string) (string, error) {
	return func(key string) (string, error) {
		for _, k := range pending {
			if key == k.path+".pub" {
				return "ssh-ed25519 <the key generated at " + a.env.Shorten(k.path) + ">", nil
			}
		}
		return keys.ReadPublic(key)
	}
}

// warnSharedKeys points out accounts that share a host and an auth key:
// GitHub lets a key belong to one account only, so one of them would log
// in as the other.
func (a *app) warnSharedKeys(list []*accounts.Account) {
	for i, x := range list {
		for _, y := range list[i+1:] {
			if x.AuthKey == "" || y.AuthKey == "" {
				continue
			}
			host := sharedHost(x, y)
			if host == "" {
				continue
			}
			fx, errX := keys.Fingerprint(a.env.Expand(x.AuthKey))
			fy, errY := keys.Fingerprint(a.env.Expand(y.AuthKey))
			if errX == nil && errY == nil && fx == fy {
				a.warnf("%s and %s use the same auth key on %s. A host like GitHub lets a key belong to one account only, so one of them will log in as the other.", x.ID, y.ID, host)
			}
		}
	}
}

func sharedHost(x, y *accounts.Account) string {
	for _, h := range x.Hosts {
		for _, g := range y.Hosts {
			if strings.EqualFold(h, g) {
				return h
			}
		}
	}
	return ""
}

// keySummary describes an account's keys in a few words, for ls.
func keySummary(acc *accounts.Account) string {
	var parts []string
	if acc.AuthKey != "" {
		parts = append(parts, "auth")
	}
	if acc.SigningKey != "" {
		parts = append(parts, "signing")
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}
