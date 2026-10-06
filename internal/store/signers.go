package store

import (
	"fmt"
	"path/filepath"
	"strings"

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

// signerEntries lists one allowed_signers line per signing account, sorted
// as the accounts are.
func signerEntries(env *paths.Env, list []*accounts.Account, publicKey func(keys.Ref) (string, error)) ([]string, error) {
	var entries []string
	for _, a := range list {
		if a.SigningKey == "" {
			continue
		}
		pub, err := publicKey(a.SigningKey.Map(env.Expand))
		if err != nil {
			return nil, fmt.Errorf("account %s: signing key %s: %w (change it with `doppel edit %s --signing-key <key>` or `--no-signing`)",
				a.ID, a.SigningKey, err, a.ID)
		}
		entries = append(entries, fmt.Sprintf("%s namespaces=\"git\" %s", a.Email, pub))
	}
	return entries, nil
}

// stageSigners writes doppel's block in the allowed_signers file. It returns
// the file to set in the index, or "" when the user configured their own or
// no account signs.
func stageSigners(env *paths.Env, p *plan.Plan, list []*accounts.Account, publicKey func(keys.Ref) (string, error)) (string, error) {
	entries, err := signerEntries(env, list, publicKey)
	if err != nil {
		return "", err
	}
	path, configured, err := SignersFile(env)
	if err != nil {
		return "", err
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
