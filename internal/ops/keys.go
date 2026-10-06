package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
)

// KeyChanges says how to change an account's keys. Flags and the wizard
// both describe their choices this way.
type KeyChanges struct {
	Auth            *string // the new auth key as given; "" for ssh's own keys, nil to keep it
	GenerateAuth    bool
	Signing         *string // the new signing key as given; "" to stop signing, nil to keep it
	GenerateSigning bool
	SignWithAuth    bool
	SignCommits     *bool // nil keeps the current setting
	SignTags        *bool
}

// Any reports whether ch changes anything.
func (ch KeyChanges) Any() bool {
	return ch.Auth != nil || ch.GenerateAuth || ch.Signing != nil || ch.GenerateSigning || ch.SignWithAuth ||
		ch.SignCommits != nil || ch.SignTags != nil
}

// NewKey is a key to generate before saving.
type NewKey struct {
	Path    string // absolute
	Comment string
	Purpose string // "auth key" or "signing key"
}

// applyKeys sets acc's keys. Keys to generate are returned rather than
// created, so a dry run can show them without writing.
func applyKeys(ctx Context, acc *accounts.Account, ch KeyChanges) ([]NewKey, error) {
	var newKeys []NewKey
	sshDir := filepath.Join(ctx.Env.Home, ".ssh")
	switch {
	case ch.Auth != nil:
		key, err := KeyPath(ctx, *ch.Auth, false)
		if err != nil {
			return nil, err
		}
		acc.AuthKey = key
	case ch.GenerateAuth:
		path, err := newKeyPath(sshDir, acc.ID, false, "--auth-key")
		if err != nil {
			return nil, err
		}
		acc.AuthKey = keys.Ref(ctx.Env.Shorten(path))
		newKeys = append(newKeys, NewKey{path, acc.Email, "auth key"})
	}

	wasSigning := acc.SigningKey != ""
	switch {
	case ch.Signing != nil:
		key, err := KeyPath(ctx, *ch.Signing, true)
		if err != nil {
			return nil, err
		}
		acc.SigningKey = key
	case ch.GenerateSigning:
		path, err := newKeyPath(sshDir, acc.ID, true, "--signing-key")
		if err != nil {
			return nil, err
		}
		acc.SigningKey = keys.Ref(ctx.Env.Shorten(path)).Public()
		newKeys = append(newKeys, NewKey{path, acc.Email, "signing key"})
	case ch.SignWithAuth:
		if acc.AuthKey == "" {
			return nil, errors.New("--sign-with-auth-key needs an auth key; add one with --auth-key or --generate-auth-key")
		}
		acc.SigningKey = acc.AuthKey.Public()
	}
	if acc.SigningKey != "" && !wasSigning {
		acc.SignCommits, acc.SignTags = true, true
	}
	if ch.SignCommits != nil {
		acc.SignCommits = *ch.SignCommits
	}
	if ch.SignTags != nil {
		acc.SignTags = *ch.SignTags
	}
	return newKeys, nil
}

// newKeyPath is where a generated key goes. It refuses a path that's taken:
// doppel never overwrites keys.
func newKeyPath(sshDir, id string, signing bool, useFlag string) (string, error) {
	path := keys.DefaultPath(sshDir, id, signing)
	for _, p := range []string{path, keys.Ref(path).PublicPath()} {
		if _, err := os.Lstat(p); err == nil {
			return "", fmt.Errorf("%s already exists; use it with %s %s", p, useFlag, path)
		}
	}
	return path, nil
}

// KeyPath checks a key the user named and returns its stored form,
// "~/"-shortened. An auth key is a private key, or a .pub whose private
// half lives in an agent. A signing key is stored as its .pub, which Git
// hands to ssh-keygen. "" means no key.
func KeyPath(ctx Context, input string, signing bool) (keys.Ref, error) {
	if input == "" {
		return "", nil
	}
	path := ctx.Env.Expand(input)
	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.Cwd, path)
	}
	key := keys.Ref(filepath.Clean(path))
	if signing {
		key = key.Public()
	}
	short := func(p string) string { return ctx.Env.Shorten(p) }
	if key.IsPublic() {
		if _, err := keys.ReadPublic(key); err != nil {
			if os.IsNotExist(err) && signing {
				return "", fmt.Errorf("no public key at %s; create it with: ssh-keygen -y -f %s > %s",
					short(key.PublicPath()), short(key.PrivatePath()), short(key.PublicPath()))
			}
			return "", fmt.Errorf("%s: %w", short(key.PublicPath()), err)
		}
	} else if info, err := os.Stat(string(key)); err != nil || info.IsDir() {
		return "", fmt.Errorf("no key file at %s", short(string(key)))
	}
	return key.Map(ctx.Env.Shorten), nil
}

// sharedKeyWarnings points out accounts that share a host and an auth key:
// GitHub lets a key belong to one account only, so one of them would log
// in as the other.
func sharedKeyWarnings(ctx Context, list []*accounts.Account) []string {
	var warnings []string
	for i, x := range list {
		for _, y := range list[i+1:] {
			if host := accounts.SharedAuthKey(ctx.Env, x, y); host != "" {
				warnings = append(warnings, fmt.Sprintf("%s and %s use the same auth key on %s. A host like GitHub lets a key belong to one account only, so one of them will log in as the other.", x.ID, y.ID, host))
			}
		}
	}
	return warnings
}
