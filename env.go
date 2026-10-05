package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Env holds the locations doppel reads and writes. Tests point HOME and
// XDG_CONFIG_HOME at a sandbox.
type Env struct {
	Home      string // the user's home directory
	ConfigDir string // $XDG_CONFIG_HOME, or ~/.config
	GOOS      string
}

func loadEnv() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory: %w", err)
	}
	home = strings.TrimSuffix(home, "/")
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configDir) {
		configDir = filepath.Join(home, ".config")
	}
	return &Env{Home: home, ConfigDir: configDir, GOOS: runtime.GOOS}, nil
}

// DoppelDir is where doppel keeps its own files.
func (e *Env) DoppelDir() string { return filepath.Join(e.ConfigDir, "doppel") }

// AccountsDir holds one <id>.gitconfig file per account.
func (e *Env) AccountsDir() string { return filepath.Join(e.DoppelDir(), "accounts") }

// AccountPath is the account file for id.
func (e *Env) AccountPath(id string) string {
	return filepath.Join(e.AccountsDir(), id+".gitconfig")
}

// IndexPath is the generated file holding the default account and folder rules.
func (e *Env) IndexPath() string { return filepath.Join(e.DoppelDir(), "index.gitconfig") }

// GlobalConfigPath is the global Git config file that gets doppel's include:
// the file `git config --global` writes to, which Git also reads last.
func (e *Env) GlobalConfigPath() string {
	if p := os.Getenv("GIT_CONFIG_GLOBAL"); p != "" {
		return e.Expand(p)
	}
	home := filepath.Join(e.Home, ".gitconfig")
	if fileExists(home) {
		return home
	}
	xdg := filepath.Join(e.ConfigDir, "git", "config")
	if fileExists(xdg) {
		return xdg
	}
	return home
}

// CaseInsensitive reports whether folder rules should ignore case, as macOS
// filesystems do.
func (e *Env) CaseInsensitive() bool { return e.GOOS == "darwin" }

// Expand turns a leading "~/" into the home directory, keeping any trailing "/".
func (e *Env) Expand(path string) string {
	if path == "~" {
		return e.Home
	}
	if strings.HasPrefix(path, "~/") {
		return e.Home + path[1:]
	}
	return path
}

// Shorten rewrites a path inside the home directory to the "~/" form Git expands.
func (e *Env) Shorten(path string) string {
	if path == e.Home {
		return "~"
	}
	if strings.HasPrefix(path, e.Home+"/") {
		return "~" + path[len(e.Home):]
	}
	return path
}

// SamePath reports whether two paths (either may use "~/") name the same location.
func (e *Env) SamePath(a, b string) bool {
	a, b = filepath.Clean(e.Expand(a)), filepath.Clean(e.Expand(b))
	if e.CaseInsensitive() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// NormalizeFolder turns a folder given on the command line into its stored
// form: the real absolute location (symlinks resolved, because Git matches a
// repo by its real path), shortened to "~/" inside the home directory and
// ending in "/" so ~/projects/work never matches ~/projects/workshop. exists
// reports whether the folder exists yet; for one that doesn't, its nearest
// existing parent is resolved instead.
func (e *Env) NormalizeFolder(input, cwd string) (folder string, exists bool, err error) {
	p := strings.TrimSpace(input)
	if p == "" {
		return "", false, errors.New("folder path is empty")
	}
	p = e.Expand(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	real, exists := resolveExisting(filepath.Clean(p))
	if exists {
		if info, err := os.Stat(real); err == nil && !info.IsDir() { //nolint:gosec // the user's own folder argument; only its type is read
			return "", false, fmt.Errorf("%s is a file, not a folder", input)
		}
	}
	if strings.ContainsAny(real, "*?[\\\"\n") {
		return "", false, fmt.Errorf("%s: folder paths can't contain *, ?, [, \\ or \" because Git would read them as a pattern", input)
	}
	folder = e.Shorten(real)
	if !strings.HasSuffix(folder, "/") {
		folder += "/"
	}
	return folder, exists, nil
}

// resolveExisting resolves symlinks in p. When p doesn't exist, it resolves
// the nearest existing parent and appends the rest.
func resolveExisting(p string) (string, bool) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real, true
	}
	dir, rest := p, ""
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return p, false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest), false
		}
	}
}

// FolderContains reports whether path is folder itself or lies inside it.
// folder is in stored form (ending in "/"); path is an absolute path.
func (e *Env) FolderContains(folder, path string) bool {
	f := e.Expand(folder)
	p := strings.TrimSuffix(filepath.Clean(path), "/") + "/"
	if e.CaseInsensitive() {
		f, p = strings.ToLower(f), strings.ToLower(p)
	}
	return strings.HasPrefix(p, f)
}

// folderDepth orders folder rules from broad to specific.
func (e *Env) folderDepth(folder string) int {
	return strings.Count(strings.TrimSuffix(e.Expand(folder), "/"), "/")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
