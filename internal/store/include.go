package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/plan"
)

// IncludeComment marks the block doppel appends to the global Git config.
const IncludeComment = "# Added by doppel. Keep this at the end of the file so doppel's accounts take effect."

// IncludeBlock is what doppel appends to the global Git config.
func IncludeBlock(env *paths.Env) string {
	return IncludeComment + "\n[include]\n\tpath = " + quoteValue(env.Shorten(env.IndexPath())) + "\n"
}

// IncludesIndex reports whether a Git config file includes doppel's index.
func IncludesIndex(env *paths.Env, configPath string) (bool, error) {
	values, err := git.ReadConfigFile(configPath)
	if err != nil {
		return false, err
	}
	for _, v := range values["include.path"] {
		if env.SamePath(v, env.IndexPath()) {
			return true, nil
		}
	}
	return false, nil
}

// EnsureInclude makes sure the global Git config includes doppel's index
// once, at the end of the file Git reads last. An include left in another
// global file, such as ~/.config/git/config from before ~/.gitconfig
// existed, is moved. The block is appended as text: `git config --add`
// would put it inside an existing [include] section, which may not be last.
func EnsureInclude(env *paths.Env, p *plan.Plan) error {
	path := env.GlobalConfigPath()
	for _, other := range env.GlobalConfigFiles() {
		if other == path {
			continue
		}
		if _, err := removeIncludeFrom(env, p, other); err != nil {
			return err
		}
	}
	staged, err := p.Stage(path)
	if err != nil {
		return err
	}
	if ok, err := IncludesIndex(env, staged); err != nil || ok {
		return err
	}
	data, _, err := p.Content(path)
	if err != nil {
		return err
	}
	text := string(data)
	if text != "" {
		text = strings.TrimRight(text, "\n") + "\n\n"
	}
	return p.SetContent(path, []byte(text+IncludeBlock(env)))
}

// RemoveInclude takes doppel's include out of every global Git config file.
// It reports whether anything was removed.
func RemoveInclude(env *paths.Env, p *plan.Plan) (bool, error) {
	removed := false
	for _, path := range env.GlobalConfigFiles() {
		ok, err := removeIncludeFrom(env, p, path)
		if err != nil {
			return false, err
		}
		removed = removed || ok
	}
	return removed, nil
}

// removeIncludeFrom takes doppel's include out of one Git config file: the
// block doppel appended as text when it's still intact, then, through Git,
// any include of the index that was moved, reformatted or shares its
// section with other keys.
func removeIncludeFrom(env *paths.Env, p *plan.Plan, path string) (bool, error) {
	data, exists, err := p.Content(path)
	if err != nil || !exists {
		return false, err
	}
	removed := false
	lines := strings.Split(string(data), "\n")
	for k := 0; k+2 < len(lines); k++ {
		if strings.TrimSpace(lines[k]) != IncludeComment || strings.TrimSpace(lines[k+1]) != "[include]" || !isIndexPathLine(env, lines[k+2]) {
			continue
		}
		start, end := k, k+3
		if !sectionEndsAt(lines, end) {
			// `git config --add include.path` puts new paths in the last
			// [include] section, which is doppel's. Removing the whole block
			// would leave those keys under the section above it, so only the
			// comment goes here and Git removes doppel's path below.
			start, end = k, k+1
		} else if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
			start-- // the blank line ensureInclude added before the block
		}
		lines = append(lines[:start], lines[end:]...)
		removed = true
		break
	}
	if removed {
		if err := p.SetContent(path, []byte(strings.Join(lines, "\n"))); err != nil {
			return false, err
		}
	}

	staged, err := p.Stage(path)
	if err != nil {
		return false, err
	}
	values, err := git.ReadConfigFile(staged)
	if err != nil {
		return false, err
	}
	for _, v := range values["include.path"] {
		if !env.SamePath(v, env.IndexPath()) {
			continue
		}
		if err := git.ConfigFile(staged, "--fixed-value", "--unset-all", "include.path", v); err != nil {
			return false, err
		}
		removed = true
	}
	return removed, nil
}

// sectionEndsAt reports whether the config section running up to lines[i]
// has no more keys: only blank lines and comments come before the next
// section header or the end of the file.
func sectionEndsAt(lines []string, i int) bool {
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
			continue
		case strings.HasPrefix(line, "["):
			return true
		default:
			return false
		}
	}
	return true
}

// isIndexPathLine reports whether a config line is `path = <doppel's index>`.
func isIndexPathLine(env *paths.Env, line string) bool {
	key, value, ok := strings.Cut(line, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "path") {
		return false
	}
	return env.SamePath(unquoteValue(strings.TrimSpace(value)), env.IndexPath())
}

// Overrides lists the settings doppel manages that the global Git config
// sets after doppel's include, where they override every account. found is
// false when the file doesn't include doppel's index at all.
func Overrides(env *paths.Env, managed []string) (path string, found bool, overriding []string, err error) {
	path = env.GlobalConfigPath()
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the user's global Git config
	if err != nil {
		if os.IsNotExist(err) {
			return path, false, nil, nil
		}
		return path, false, nil, err
	}
	lines := strings.Split(string(data), "\n")
	last := -1
	for i, line := range lines {
		if isIndexPathLine(env, line) {
			last = i
		}
	}
	if last < 0 {
		return path, false, nil, nil
	}
	// Read what follows the include with Git. Lines left over from the
	// [include] section get a placeholder section so Git can parse them.
	tail, err := os.CreateTemp("", "doppel-tail-*.gitconfig")
	if err != nil {
		return path, true, nil, err
	}
	defer func() { _ = os.Remove(tail.Name()) }()
	_, err = tail.WriteString("[doppel-tail]\n" + strings.Join(lines[last+1:], "\n"))
	if cerr := tail.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return path, true, nil, err
	}
	values, err := git.ReadConfigFile(tail.Name())
	if err != nil {
		return path, true, nil, err
	}
	for _, key := range managed {
		if _, ok := values[strings.ToLower(key)]; ok {
			overriding = append(overriding, key)
		}
	}
	return path, true, overriding, nil
}
