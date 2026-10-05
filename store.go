package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// stageAccounts plans every file change needed for accounts to be the whole
// set of accounts: each account file, removal of files whose account is gone,
// the generated index, and the include in the global Git config. Changes are
// staged in that order, so Apply writes the global include last.
func stageAccounts(env *Env, plan *Plan, accounts []*Account) error {
	keep := map[string]bool{}
	for _, a := range accounts {
		path := env.AccountPath(a.ID)
		keep[path] = true
		if a.file != "" && a.file != path {
			// Renamed: start from the old file so the settings doppel
			// doesn't manage come along.
			data, err := os.ReadFile(filepath.Clean(a.file)) //nolint:gosec // account file inside doppel's directory
			if err != nil {
				return err
			}
			if err := plan.SetContent(path, data); err != nil {
				return err
			}
		}
		staged, err := plan.Stage(path)
		if err != nil {
			return err
		}
		if err := reconcile(staged, a.settings()); err != nil {
			return fmt.Errorf("updating account %s: %w", a.ID, err)
		}
	}

	existing, err := filepath.Glob(filepath.Join(env.AccountsDir(), "*.gitconfig"))
	if err != nil {
		return err
	}
	for _, path := range existing {
		if !keep[path] && !strings.HasPrefix(filepath.Base(path), ".") {
			if err := plan.Remove(path); err != nil {
				return err
			}
		}
	}

	if err := plan.SetContent(env.IndexPath(), renderIndex(env, accounts)); err != nil {
		return err
	}
	return ensureInclude(env, plan)
}

// reconcile brings the managed keys in a Git config file to the given
// values, leaving every other key as it is. Keys that already hold the right
// values aren't touched, so an unchanged account file stays byte-for-byte
// the same.
func reconcile(path string, settings []setting) error {
	current, err := readConfigFile(path)
	if err != nil {
		return err
	}
	for _, s := range settings {
		have := current[strings.ToLower(s.key)]
		if slices.Equal(have, s.values) {
			continue
		}
		if len(s.values) == 1 {
			if err := configFile(path, "--replace-all", s.key, s.values[0]); err != nil {
				return err
			}
			continue
		}
		if len(have) > 0 {
			// Exit status 5 means the key was already absent.
			if err := configFile(path, "--unset-all", s.key); err != nil && gitExitCode(err) != 5 {
				return err
			}
		}
		for _, v := range s.values {
			if err := configFile(path, "--add", s.key, v); err != nil {
				return err
			}
		}
	}
	return nil
}
