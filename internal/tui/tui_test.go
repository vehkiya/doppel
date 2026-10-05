package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/paths"
)

func testAccounts() []*accounts.Account {
	return []*accounts.Account{
		{ID: "personal", Name: "Jane Doe", Email: "jane@personal.dev", Default: true, Hosts: []string{"github.com"},
			AuthKey: "~/.ssh/id_ed25519_personal", SigningKey: "~/.ssh/id_ed25519_personal.pub", SignCommits: true, SignTags: true},
		{ID: "work", Name: "Jane Doe", Email: "jane@acme.com", Hosts: []string{"github.com", "gitlab.acme.com"},
			Folders: []string{"~/projects/work/", "~/clients/"}, AuthKey: "~/.ssh/id_ed25519_work"},
	}
}

func newModel(t *testing.T, list []*accounts.Account, width, height int) tea.Model {
	t.Helper()
	env := &paths.Env{Home: "/home/jane", ConfigDir: "/home/jane/.config"}
	info := map[string]KeyInfo{"personal": {Auth: []string{"passphrase", "in agent"}}, "work": {Auth: []string{"no passphrase"}}}
	var m tea.Model = New(Options{Env: env, Accounts: list, KeyInfo: info, Selected: "work"})
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func press(m tea.Model, keys ...string) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEscape}
		}
		m, cmd = m.Update(msg)
	}
	return m, cmd
}

func TestKeysPickActions(t *testing.T) {
	cases := map[string]ActionKind{
		"enter": Edit, "e": Edit, "a": Add, "b": Bind, "*": SetDefault, "x": Export, "u": Upload, "t": Test,
	}
	for k, want := range cases {
		m, cmd := press(newModel(t, testAccounts(), 120, 30), k)
		if got := m.(Model).Action(); got.Kind != want || got.ID != "work" {
			t.Errorf("%s: action = %+v, want %s on work", k, got, want)
		}
		if cmd == nil {
			t.Errorf("%s: the browser didn't quit to run the action", k)
		}
	}
}

func TestDeleteAsksFirst(t *testing.T) {
	m, _ := press(newModel(t, testAccounts(), 120, 30), "d")
	if !strings.Contains(m.View(), "Delete account work?") {
		t.Fatalf("no confirmation shown:\n%s", m.View())
	}
	m, _ = press(m, "n")
	if got := m.(Model).Action(); got.Kind != Quit {
		t.Errorf("answering n picked %+v", got)
	}
	m, _ = press(m, "d", "y")
	if got := m.(Model).Action(); got.Kind != Delete || got.ID != "work" {
		t.Errorf("answering y picked %+v", got)
	}
}

func TestFilteringOwnsTheKeys(t *testing.T) {
	m, _ := press(newModel(t, testAccounts(), 120, 30), "/", "a", "x")
	if got := m.(Model).Action(); got.Kind != Quit {
		t.Errorf("typing in the filter picked %+v", got)
	}
}

func TestQuit(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m, cmd := press(newModel(t, testAccounts(), 120, 30), k)
		if m.(Model).Action().Kind != Quit || cmd == nil {
			t.Errorf("%s didn't quit", k)
		}
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 15}, {80, 20}, {120, 30}} {
		for _, list := range [][]*accounts.Account{testAccounts(), nil} {
			view := newModel(t, list, size[0], size[1]).View()
			for _, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > size[0] {
					t.Errorf("%dx%d (%d accounts): a line is %d wide:\n%s", size[0], size[1], len(list), w, line)
					break
				}
			}
		}
	}
}

func TestDetails(t *testing.T) {
	view := newModel(t, testAccounts(), 120, 30).View()
	for _, want := range []string{"jane@acme.com", "github.com, gitlab.acme.com", "~/projects/work/", "~/clients/",
		"~/.ssh/id_ed25519_work", "no passphrase", "Signing    off", "~/.config/doppel/accounts/work.gitconfig"} {
		if !strings.Contains(view, want) {
			t.Errorf("details are missing %q:\n%s", want, view)
		}
	}
	m, _ := press(newModel(t, testAccounts(), 120, 30), "k") // up, to the default account
	view = m.View()
	for _, want := range []string{"Signing    commits and tags", "~/.ssh/id_ed25519_personal.pub", "passphrase", "in agent", "outside every folder"} {
		if !strings.Contains(view, want) {
			t.Errorf("default account details are missing %q:\n%s", want, view)
		}
	}
	if !regexp.MustCompile(`👤 personal +DEFAULT`).MatchString(view) {
		t.Errorf("the DEFAULT badge isn't beside the name:\n%s", view)
	}
}

func TestNarrowTerminalTogglesDetails(t *testing.T) {
	m := newModel(t, testAccounts(), 80, 20)
	if strings.Contains(m.View(), "Auth key") {
		t.Error("details shown beside the list on a narrow terminal")
	}
	m, _ = press(m, "tab")
	if !strings.Contains(m.View(), "Auth key") {
		t.Errorf("tab didn't show the details:\n%s", m.View())
	}
}

func TestEmptyBrowser(t *testing.T) {
	m := newModel(t, nil, 80, 20)
	if !strings.Contains(m.View(), "No accounts yet") {
		t.Errorf("empty view:\n%s", m.View())
	}
	m, _ = press(m, "e")
	if got := m.(Model).Action(); got.Kind != Quit {
		t.Errorf("edit with no accounts picked %+v", got)
	}
	m, _ = press(m, "a")
	if got := m.(Model).Action(); got.Kind != Add || got.ID != "" {
		t.Errorf("a picked %+v", got)
	}
}

func TestStatusClears(t *testing.T) {
	env := &paths.Env{Home: "/home/jane"}
	var m tea.Model = New(Options{Env: env, Accounts: testAccounts(), Status: "✓ Added account work"})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(m.View(), "✓ Added account work") || m.Init() == nil {
		t.Fatal("status not shown, or never cleared")
	}
	m, _ = m.Update(clearStatusMsg{})
	if strings.Contains(m.View(), "Added account") {
		t.Error("status still shown after it cleared")
	}
}

func TestUpdateNotice(t *testing.T) {
	checked := false
	env := &paths.Env{Home: "/home/jane"}
	var m tea.Model = New(Options{Env: env, Accounts: testAccounts(), CheckUpdate: func() (string, bool) {
		checked = true
		return "v9.9.9", true
	}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Before the check answers, U does nothing.
	if m, _ := press(m, "U"); m.(Model).Action().Kind != Quit {
		t.Error("U picked an action before any update was found")
	}
	// The check runs in the background, through Init's command.
	for _, msg := range runAll(m.Init()) {
		m, _ = m.Update(msg)
	}
	if !checked {
		t.Fatal("the update check didn't run")
	}
	if !strings.Contains(m.View(), "doppel v9.9.9 is available") {
		t.Errorf("no update notice:\n%s", m.View())
	}
	if m, _ := press(m, "U"); m.(Model).Action().Kind != Upgrade {
		t.Error("U didn't pick the upgrade")
	}

	// No newer release: no notice, and U stays off.
	m = New(Options{Env: env, Accounts: testAccounts(), CheckUpdate: func() (string, bool) { return "v0.1.0", false }})
	for _, msg := range runAll(m.Init()) {
		m, _ = m.Update(msg)
	}
	if strings.Contains(m.View(), "is available") {
		t.Error("an update notice without a newer release")
	}
	if m, _ := press(m, "U"); m.(Model).Action().Kind != Quit {
		t.Error("U picked an action without a newer release")
	}
}

// runAll runs a command and any batch inside it, returning their messages.
func runAll(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, runAll(c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

func TestKeyDetailsLoadAfterTheBrowserShows(t *testing.T) {
	env := &paths.Env{Home: "/home/jane", ConfigDir: "/home/jane/.config"}
	loaded := false
	load := func() map[string]KeyInfo {
		loaded = true
		return map[string]KeyInfo{"work": {Auth: []string{"no passphrase"}}}
	}
	var m tea.Model = New(Options{Env: env, Accounts: testAccounts(), LoadKeyInfo: load, Selected: "work"})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if loaded {
		t.Fatal("key details were loaded before the browser showed")
	}
	view := m.View()
	if !strings.Contains(view, "checking…") || strings.Contains(view, "no passphrase") {
		t.Errorf("before the details arrive:\n%s", view)
	}

	// Init's commands run in the background; feed their messages back.
	var msgs []tea.Msg
	collect(m.Init(), &msgs)
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	view = m.View()
	if !strings.Contains(view, "no passphrase") || strings.Contains(view, "checking…") {
		t.Errorf("after the details arrive:\n%s", view)
	}
}

// collect runs cmd and any batch inside it, gathering the messages they send.
func collect(cmd tea.Cmd, msgs *[]tea.Msg) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			collect(c, msgs)
		}
	default:
		*msgs = append(*msgs, msg)
	}
}
