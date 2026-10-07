// Package store writes accounts to disk: each account file, the generated
// index of folder rules, and the include in the global Git config.
package store

import (
	"fmt"
	"os"
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

// Options adjusts how Save writes accounts.
type Options struct {
	// PublicKey reads a signing key's public half. It defaults to reading the
	// key file; a dry run substitutes placeholders for keys it would generate.
	PublicKey func(key keys.Ref) (string, error)

	// Now is when signers that are no longer used are retired; it
	// defaults to time.Now.
	Now func() time.Time

	// Removed lists the accounts the command deletes. Save deletes the file
	// of no other account, apart from the old file of one that was renamed,
	// so an account added meanwhile is never swept away.
	Removed []*accounts.Account

	// Cleanups lists includeIf directives to remove from Git config files.
	Cleanups []accounts.IncludeCleanup
}

// Save plans every file change needed for list to be the whole set of
// accounts: each account file, removal of the files of Options.Removed and of
// renamed accounts, doppel's block in allowed_signers, the generated index,
// and the include in the global Git config. Changes are staged in that
// order, so Apply writes the global include last.
//
// It is the only way to stage accounts, so every write is validated first,
// whether it comes from a command or from `doctor --fix`: a folder that
// would make Git misread or reject the generated index never reaches a file.
// It also fails when an account file changed since the command loaded it.
// Callers hold the write lock (Lock) from before the first load they can't
// afford to be stale until Apply returns.
func Save(env *paths.Env, p *plan.Plan, list []*accounts.Account, opts Options) error {
	if err := accounts.ValidateAll(env, list); err != nil {
		return err
	}
	if err := checkFresh(env, list, opts.Removed); err != nil {
		return err
	}
	return stage(env, p, list, opts)
}

// checkFresh fails when an account file isn't as the command loaded it: one
// was edited or deleted, or one it never saw was added.
func checkFresh(env *paths.Env, list, removed []*accounts.Account) error {
	known := map[string]bool{}
	for _, a := range append(slices.Clone(list), removed...) {
		if a.File == "" {
			continue
		}
		known[a.File] = true
		if a.Checksum == "" {
			continue
		}
		sum, err := accounts.FileChecksum(a.File)
		if err != nil {
			return err
		}
		if sum != a.Checksum {
			return &plan.StaleError{Path: a.File}
		}
	}
	existing, err := filepath.Glob(filepath.Join(env.AccountsDir(), "*.gitconfig"))
	if err != nil {
		return err
	}
	for _, path := range existing {
		if !known[path] && !strings.HasPrefix(filepath.Base(path), ".") {
			return &plan.StaleError{Path: path}
		}
	}
	return nil
}

func stage(env *paths.Env, p *plan.Plan, list []*accounts.Account, opts Options) error {
	if opts.PublicKey == nil {
		opts.PublicKey = defaultPublicKey
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	// Signers the accounts no longer use are kept, time-limited, in their
	// account files, so they're worked out before the files are staged. The
	// accounts are copied, so the caller's are left as they were.
	signersPath, configured, err := SignersFile(env)
	if err != nil {
		return err
	}
	current, err := currentSigners(env, list, opts.PublicKey)
	if err != nil {
		return err
	}
	text, _, err := p.Content(signersPath)
	if err != nil {
		return err
	}
	retired := retire(env, list, opts.Removed, current, trustedSigners(string(text)), opts.PublicKey, opts.Now())
	staged := make([]*accounts.Account, len(list))
	for i, a := range list {
		c := *a
		c.Retired = retired[i]
		staged[i] = &c
	}
	list = staged

	targets := map[string]bool{}
	for _, a := range list {
		targets[env.AccountPath(a.ID)] = true
	}
	for _, a := range list {
		path := env.AccountPath(a.ID)
		if a.File != "" && a.File != path {
			// Renamed: start from the old file so the settings doppel
			// doesn't manage come along.
			data, err := os.ReadFile(filepath.Clean(a.File)) //nolint:gosec // account file inside doppel's directory
			if err != nil {
				return err
			}
			if err := p.SetContent(path, data); err != nil {
				return err
			}
		}
		staged, err := p.Stage(path)
		if err != nil {
			return err
		}
		if err := reconcile(staged, a.Settings()); err != nil {
			return fmt.Errorf("updating account %s: %w", a.ID, err)
		}
	}

	gone := slices.Clone(opts.Removed)
	for _, a := range list {
		if a.File != "" && a.File != env.AccountPath(a.ID) {
			gone = append(gone, a)
		}
	}
	for _, a := range gone {
		if a.File != "" && !targets[a.File] {
			if err := p.Remove(a.File); err != nil {
				return err
			}
		}
	}

	if len(opts.Cleanups) > 0 {
		if err := CleanIncludes(env, p, opts.Cleanups); err != nil {
			return err
		}
	}

	signers, err := stageSigners(p, list, current, signersPath, configured)
	if err != nil {
		return err
	}
	if err := p.SetContent(env.IndexPath(), renderIndex(env, list, signers)); err != nil {
		return err
	}
	return ensureInclude(env, p)
}

// reconcile brings the managed keys in a Git config file to the given
// values, leaving every other key as it is. Keys that already hold the right
// values aren't touched, so an unchanged account file stays byte-for-byte
// the same.
func reconcile(path string, settings []accounts.Setting) error {
	current, err := git.ReadConfigFile(path)
	if err != nil {
		return err
	}
	for _, s := range settings {
		have := current[strings.ToLower(s.Key)]
		if slices.Equal(have, s.Values) {
			continue
		}
		if len(s.Values) == 1 {
			if err := git.ConfigFile(path, "--replace-all", s.Key, s.Values[0]); err != nil {
				return err
			}
			continue
		}
		if len(have) > 0 {
			// Exit status 5 means the key was already absent.
			if err := git.ConfigFile(path, "--unset-all", s.Key); err != nil && git.ExitCode(err) != 5 {
				return err
			}
		}
		for _, v := range s.Values {
			if err := git.ConfigFile(path, "--add", s.Key, v); err != nil {
				return err
			}
		}
	}
	return nil
}
