// Package paths knows where doppel's and Git's files live, and how folders
// are normalized and matched.
package paths

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
	Home      string // the user's home directory, as $HOME names it
	RealHome  string // Home with symlinks resolved
	ConfigDir string // $XDG_CONFIG_HOME, or ~/.config
	GOOS      string
}

// Load reads the environment: $HOME and $XDG_CONFIG_HOME.
func Load() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory: %w", err)
	}
	home = strings.TrimSuffix(home, "/")
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configDir) {
		configDir = filepath.Join(home, ".config")
	}
	realHome := home
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		realHome = resolved
	}
	return &Env{Home: home, RealHome: realHome, ConfigDir: configDir, GOOS: runtime.GOOS}, nil
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
		return p // used exactly as Git uses it, without expanding "~"
	}
	home := filepath.Join(e.Home, ".gitconfig")
	if FileExists(home) {
		return home
	}
	xdg := filepath.Join(e.ConfigDir, "git", "config")
	if FileExists(xdg) {
		return xdg
	}
	return home
}

// GlobalConfigFiles lists every global config file Git reads, in the order
// it reads them. doppel's include belongs only in GlobalConfigPath, but an
// earlier one can be left in another file, for instance when ~/.gitconfig
// is created after doppel added its include to ~/.config/git/config.
func (e *Env) GlobalConfigFiles() []string {
	if p := os.Getenv("GIT_CONFIG_GLOBAL"); p != "" {
		return []string{p}
	}
	return []string{filepath.Join(e.ConfigDir, "git", "config"), filepath.Join(e.Home, ".gitconfig")}
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

// Shorten rewrites a path inside the home directory to the "~/" form Git
// expands. A path under the home directory's real location is shortened
// too: Git matches "~/" folder rules through a symlinked home directory.
func (e *Env) Shorten(path string) string {
	for _, home := range []string{e.Home, e.RealHome} {
		if home == "" {
			continue
		}
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+"/") {
			return "~" + path[len(home):]
		}
	}
	return path
}

// real expands "~/" and resolves symlinks in as much of path as exists, so
// paths written through different symlinks compare equal.
func (e *Env) real(path string) string {
	resolved, _ := ResolveExisting(filepath.Clean(e.Expand(path)))
	return resolved
}

// SamePath reports whether two paths (either may use "~/") name the same location.
func (e *Env) SamePath(a, b string) bool {
	a, b = e.real(a), e.real(b)
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
	real, exists := ResolveExisting(filepath.Clean(p))
	if exists {
		if info, err := os.Stat(real); err == nil && !info.IsDir() { //nolint:gosec // the user's own folder argument; only its type is read
			return "", false, fmt.Errorf("%s is a file, not a folder", input)
		}
	}
	folder = e.Shorten(real)
	if !strings.HasSuffix(folder, "/") {
		folder += "/"
	}
	if err := ValidateFolder(folder); err != nil {
		return "", false, fmt.Errorf("%s: %w", input, err)
	}
	return folder, exists, nil
}

// ValidateFolder checks a folder in stored form before it's written into a
// folder rule. Hand-edited account files go through it too: a bad value
// would make the generated index unreadable to Git, or match folders
// anywhere on disk.
func ValidateFolder(folder string) error {
	switch {
	case !strings.HasPrefix(folder, "/") && !strings.HasPrefix(folder, "~/"):
		return errors.New("folders must be absolute paths or start with ~/; Git would match a relative one anywhere on disk")
	case !strings.HasSuffix(folder, "/"):
		return errors.New("folders must end with /")
	case strings.ContainsAny(folder, "*?[\\\"\n"):
		return errors.New("folder paths can't contain *, ?, [, \\, \" or line breaks, because Git would read them as a pattern")
	}
	return nil
}

// ResolveExisting resolves symlinks in p. When p doesn't exist, it resolves
// the nearest existing parent and appends the rest.
func ResolveExisting(p string) (string, bool) {
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
// folder is in stored form (ending in "/"); path is absolute. Symlinks are
// resolved on both sides, so they compare as Git compares them (on macOS,
// even /home is a symlink).
func (e *Env) FolderContains(folder, path string) bool {
	f := strings.TrimSuffix(e.real(folder), "/") + "/"
	p := strings.TrimSuffix(e.real(path), "/") + "/"
	if e.CaseInsensitive() {
		f, p = strings.ToLower(f), strings.ToLower(p)
	}
	return strings.HasPrefix(p, f)
}

// FolderDepth orders folder rules from broad to specific.
func (e *Env) FolderDepth(folder string) int {
	return strings.Count(strings.TrimSuffix(e.Expand(folder), "/"), "/")
}

// FileExists reports whether path exists, following symlinks.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
