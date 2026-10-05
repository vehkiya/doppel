// Package store writes accounts to disk: each account file, the generated
// index of folder rules, and the include in the global Git config.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/plan"
)

// Stage plans every file change needed for list to be the whole set of
// accounts: each account file, removal of files whose account is gone,
// the generated index, and the include in the global Git config. Changes are
// staged in that order, so Apply writes the global include last.
func Stage(env *paths.Env, p *plan.Plan, list []*accounts.Account) error {
	keep := map[string]bool{}
	for _, a := range list {
		path := env.AccountPath(a.ID)
		keep[path] = true
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

	existing, err := filepath.Glob(filepath.Join(env.AccountsDir(), "*.gitconfig"))
	if err != nil {
		return err
	}
	for _, path := range existing {
		if !keep[path] && !strings.HasPrefix(filepath.Base(path), ".") {
			if err := p.Remove(path); err != nil {
				return err
			}
		}
	}

	if err := p.SetContent(env.IndexPath(), RenderIndex(env, list)); err != nil {
		return err
	}
	return EnsureInclude(env, p)
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
