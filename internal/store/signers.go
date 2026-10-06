package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/plan"
)

// The block doppel owns in allowed_signers. Lines outside it, such as
// teammates' keys, are never touched.
const (
	signersBegin = "# >>> doppel: managed block; changes here are overwritten >>>"
	signersEnd   = "# <<< doppel <<<"
)

// SignersFile returns the allowed_signers file Git verifies signatures
// with: the one the user configured in gpg.ssh.allowedSignersFile, or else
// ~/.ssh/allowed_signers. configured reports whether the user set it; if
// not, the index has to point Git at the default file.
//
// The global config files are read with the files they include, as Git
// reads them, so a setting kept in an included file counts. doppel's own
// files are skipped: the index sets the value when the user hasn't.
func SignersFile(env *paths.Env) (path string, configured bool, err error) {
	for _, f := range env.GlobalConfigFiles() {
		entries, err := git.ReadConfigFileIncludes(f, env.InDoppelDir)
		if err != nil {
			return "", false, err
		}
		for _, e := range entries {
			if e.Key == "gpg.ssh.allowedsignersfile" {
				path, configured = e.Value, e.Value != ""
			}
		}
	}
	if configured {
		return env.Expand(path), true, nil
	}
	return filepath.Join(env.Home, ".ssh", "allowed_signers"), false, nil
}

// signer is an email and the public key allowed_signers trusts for it.
type signer struct{ email, key string }

func (s signer) line() string { return s.email + ` namespaces="git" ` + s.key }

// retiredLine trusts a retired signer only for signatures made up to when it
// was retired. Git hands ssh-keygen the commit's time (-Overify-time), so
// older commits keep verifying. OpenSSH older than 8.8 can't read the
// option and skips the line, trusting it for nothing.
func retiredLine(r accounts.RetiredSigner) string {
	return r.Email + ` namespaces="git",valid-before="` + r.Until + `" ` + r.Key
}

// currentSigners returns the signer each account signs as now, in list
// order; the zero signer for one that doesn't sign.
func currentSigners(env *paths.Env, list []*accounts.Account, publicKey func(keys.Ref) (string, error)) ([]signer, error) {
	current := make([]signer, len(list))
	for i, a := range list {
		if a.SigningKey == "" {
			continue
		}
		pub, err := publicKey(a.SigningKey.Map(env.Expand))
		if err != nil {
			return nil, fmt.Errorf("account %s: signing key %s: %w (change it with `doppel edit %s --signing-key <key>` or `--no-signing`)",
				a.ID, a.SigningKey, err, a.ID)
		}
		current[i] = signer{a.Email, pub}
	}
	return current, nil
}

// trustedSigners reads the signers doppel's block in an allowed_signers
// file trusts without a time limit: the ones it wrote for the accounts as
// they were the last time it saved them.
func trustedSigners(text string) []signer {
	var list []signer
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == signersBegin:
			in = true
		case line == signersEnd:
			in = false
		case in:
			f := strings.Fields(line)
			if len(f) == 4 && f[1] == `namespaces="git"` {
				list = append(list, signer{f[0], f[2] + " " + f[3]})
			}
		}
	}
	return list
}

// retire keeps trusting, for signatures made until now, each signer the
// block trusts that the accounts no longer do: an account changed its
// signing key or email, or stopped signing. It returns every account's
// retired signers, in list order, with those added.
//
// A signer goes to the account that has its email or key now, or had them in
// the file it was loaded from, which covers changing both at once. One that
// belongs to a removed account, or to no account, is dropped: deleting an
// account stops trusting its keys.
func retire(env *paths.Env, list, removed []*accounts.Account, current []signer, trusted []signer,
	publicKey func(keys.Ref) (string, error), now time.Time) [][]accounts.RetiredSigner {
	// What each account signed as when it was loaded, read back from its file.
	loaded := func(a *accounts.Account) signer {
		if a.File == "" {
			return signer{}
		}
		values, err := git.ReadConfigFile(a.File)
		if err != nil {
			return signer{}
		}
		last := func(key string) string {
			v := values[strings.ToLower(key)]
			if len(v) == 0 {
				return ""
			}
			return v[len(v)-1]
		}
		s := signer{email: last(accounts.KeyEmail)}
		if ref := keys.Ref(last(accounts.KeySigningKey)); ref != "" {
			s.key, _ = publicKey(ref.Map(env.Expand))
		}
		return s
	}
	keep := map[signer]bool{}
	for _, s := range current {
		keep[s] = true
	}
	for _, a := range removed {
		keep[loaded(a)] = true // not retired, but dropped
	}

	retired := make([][]accounts.RetiredSigner, len(list))
	was := make([]signer, len(list))
	for i, a := range list {
		retired[i] = slices.Clone(a.Retired)
		was[i] = loaded(a)
	}
	until := now.Format(accounts.RetiredFormat)
	for _, t := range trusted {
		if keep[t] {
			continue
		}
		for i := range list {
			matches := func(s signer) bool { return s != signer{} && (s.email == t.email || (s.key != "" && s.key == t.key)) }
			if !matches(current[i]) && !matches(was[i]) && t.email != list[i].Email {
				continue
			}
			retired[i] = slices.DeleteFunc(retired[i], func(r accounts.RetiredSigner) bool { return r.Email == t.email && r.Key == t.key })
			retired[i] = append(retired[i], accounts.RetiredSigner{Until: until, Email: t.email, Key: t.key})
			break
		}
	}
	return retired
}

// stageSigners writes doppel's block in the allowed_signers file at path:
// each account's current signer, then the ones it retired. It returns the
// file to set in the index, or "" when the user configured their own
// (configured) or there's nothing in it: retired signers alone still need
// it, for older commits to verify.
func stageSigners(p *plan.Plan, list []*accounts.Account, current []signer, path string, configured bool) (string, error) {
	var entries []string
	for i, a := range list {
		if current[i] != (signer{}) {
			entries = append(entries, current[i].line())
		}
		for _, r := range a.Retired {
			entries = append(entries, retiredLine(r))
		}
	}
	data, exists, err := p.Content(path)
	if err != nil {
		return "", err
	}
	if !exists && len(entries) == 0 {
		return "", nil
	}
	if updated := renderSigners(string(data), entries); updated != string(data) {
		if err := p.SetContent(path, []byte(updated)); err != nil {
			return "", err
		}
	}
	if configured || len(entries) == 0 {
		return "", nil
	}
	return path, nil
}

// RemoveSigners takes doppel's block out of the allowed_signers file.
func RemoveSigners(env *paths.Env, p *plan.Plan) (bool, error) {
	path, _, err := SignersFile(env)
	if err != nil {
		return false, err
	}
	data, exists, err := p.Content(path)
	if err != nil || !exists {
		return false, err
	}
	updated := renderSigners(string(data), nil)
	if updated == string(data) {
		return false, nil
	}
	return true, p.SetContent(path, []byte(updated))
}

// renderSigners replaces doppel's block in an allowed_signers file with
// entries, in place when the block exists and at the end otherwise. With no
// entries the block, and the blank line doppel added before it, goes away.
func renderSigners(text string, entries []string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	start, end := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case signersBegin:
			if start < 0 {
				start = i
			}
		case signersEnd:
			if start >= 0 && end < 0 {
				end = i
			}
		}
	}

	var block []string
	if len(entries) > 0 {
		block = append(append([]string{signersBegin}, entries...), signersEnd)
	}
	switch {
	case start >= 0 && end >= 0:
		if len(block) == 0 && start > 0 && strings.TrimSpace(lines[start-1]) == "" {
			start--
		}
		lines = append(lines[:start], append(block, lines[end+1:]...)...)
	case len(block) > 0:
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, block...)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// defaultPublicKey reads signing keys from disk.
var defaultPublicKey = keys.ReadPublic
