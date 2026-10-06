package cli

import (
	"errors"
	"flag"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
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

// keyChangesFromFlags reads key changes from the command line.
func keyChangesFromFlags(fs *flag.FlagSet, k keyFlags) (ops.KeyChanges, error) {
	set := func(name string) bool { return flagWasSet(fs, name) }
	var ch ops.KeyChanges
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
		ch.Auth = &k.authKey
	}
	ch.GenerateAuth = k.generateAuth
	switch {
	case set("signing-key"):
		ch.Signing = &k.signingKey
	case k.noSigning:
		none := ""
		ch.Signing = &none
	}
	ch.GenerateSigning, ch.SignWithAuth = k.generateSigning, k.signWithAuth
	if set("sign-commits") {
		ch.SignCommits = &k.signCommits
	}
	if set("sign-tags") {
		ch.SignTags = &k.signTags
	}
	return ch, nil
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
