package cli

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/vehkiya/doppel/internal/keys"
)

// maxSuggestions bounds the suggestions for one path, so a huge folder
// doesn't slow typing down. Huh shows one at a time anyway.
const maxSuggestions = 100

// completion is a wizard input that suggests paths: the answer it edits, and
// the suggestions for what's typed so far.
type completion struct {
	value   *string
	suggest func(typed string) []string
}

// completePaths gives a wizard input path suggestions that follow what's
// typed, shown as ghost text that Tab accepts (completeOnTab). Accessible
// prompts stay plain.
func (a *app) completePaths(ans *answers, in *huh.Input, key string, value *string, suggest func(string) []string) *huh.Input {
	if a.accessible {
		return in
	}
	if ans.completions == nil {
		ans.completions = map[string]completion{}
	}
	ans.completions[key] = completion{value, suggest}
	// Huh runs the func again whenever the binding changes; it follows the
	// pointer, so every keystroke counts.
	return in.SuggestionsFunc(func() []string { return suggest(*value) }, value)
}

// acceptSuggestion is the key Huh accepts a suggestion with.
var acceptSuggestion = tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}

// completeOnTab makes Tab accept the suggestion a path input shows, as in a
// shell. Without one, Tab moves on as usual. Huh can't tell the two apart
// itself: Tab would both accept the suggestion and leave the field.
func (ans *answers) completeOnTab(msg tea.Msg) tea.Msg {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || k.String() != "tab" || ans.form == nil {
		return msg
	}
	field := ans.form.GetFocusedField()
	if field == nil {
		return msg
	}
	c, ok := ans.completions[field.GetKey()]
	if !ok {
		return msg
	}
	typed := *c.value
	for _, s := range c.suggest(typed) {
		if len(s) > len(typed) && strings.HasPrefix(strings.ToLower(s), strings.ToLower(typed)) {
			return acceptSuggestion
		}
	}
	return msg
}

// folderSuggestions completes the last folder in a comma-separated list,
// keeping the ones before it in front of each suggestion: Huh matches
// suggestions against the whole input.
func (a *app) folderSuggestions(typed string) []string {
	head, last := "", typed
	if i := strings.LastIndex(typed, ","); i >= 0 {
		head, last = typed[:i+1], typed[i+1:]
	}
	path := strings.TrimLeft(last, " ")
	head += last[:len(last)-len(path)]
	suggestions := a.pathSuggestions(path, false)
	for i, s := range suggestions {
		suggestions[i] = head + s
	}
	return suggestions
}

// keySuggestions completes a key file's path: SSH keys, and folders to look in.
func (a *app) keySuggestions(typed string) []string {
	return a.pathSuggestions(typed, true)
}

// pathSuggestions completes the last element of a path: each suggestion is
// typed with that element finished, so it starts with exactly what was
// typed, as Huh needs. Folders end in "/", so completing can go on inside
// them. With keyFiles, SSH keys come first; "~/.ssh/" leads in the home
// folder. Hidden entries show once a "." is typed.
func (a *app) pathSuggestions(typed string, keyFiles bool) []string {
	switch typed {
	case "":
		return nil
	case "~":
		return []string{"~/"}
	}
	dirPart, prefix := "", typed
	if i := strings.LastIndex(typed, "/"); i >= 0 {
		dirPart, prefix = typed[:i+1], typed[i+1:]
	}
	dir := a.env.Expand(dirPart)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(a.cwd, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	isKey := map[string]bool{}
	if keyFiles {
		for _, k := range keys.Discover(dir) {
			isKey[filepath.Base(string(k))] = true
		}
	}
	sshDir := keyFiles && filepath.Clean(dir) == filepath.Clean(a.env.Home)
	var first, keyList, folders []string
	for _, e := range entries {
		name := e.Name()
		if !a.startsWith(name, prefix) {
			continue
		}
		if sshDir && name == ".ssh" {
			first = append(first, dirPart+name+"/")
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		switch {
		case isKey[name]:
			keyList = append(keyList, dirPart+name)
		case isDir(filepath.Join(dir, name)):
			folders = append(folders, dirPart+name+"/")
		}
	}
	all := append(append(first, keyList...), folders...)
	if len(all) > maxSuggestions {
		all = all[:maxSuggestions]
	}
	return all
}

// startsWith compares names as the filesystem does: ignoring case on macOS.
// Huh keeps the typed text when it accepts a suggestion, so on Linux a
// suggestion that differed in case would complete to a path that isn't there.
func (a *app) startsWith(name, prefix string) bool {
	if a.env.CaseInsensitive() {
		return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
	}
	return strings.HasPrefix(name, prefix)
}

// isDir reports whether path is a folder, following a symlink.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
