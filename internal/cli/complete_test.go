package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/vehkiya/doppel/internal/accounts"
)

func TestFolderCompletion(t *testing.T) {
	_, ans, d := newWizardAfter(t, func(s *sandbox) {
		s.Mkdir("projects/work")
		s.Mkdir("projects/workshop")
		s.Mkdir("Documents")
		s.Mkdir(".hidden")
	})
	fillIdentity(d)
	d.advanceTo("folders")

	// Tab accepts the suggestion and stays on the field, as in a shell.
	d.typeText("~/proj")
	d.press(tea.KeyTab)
	if ans.Folders != "~/projects/" || d.focused() != "folders" {
		t.Fatalf("~/proj + Tab: %q, focus on %q; want ~/projects/ and still on folders", ans.Folders, d.focused())
	}
	if view := d.view(); !strings.Contains(view, "tab complete") {
		t.Errorf("the help doesn't say Tab completes:\n%s", view)
	}
	d.typeText("w")
	d.press(tea.KeyTab)
	if ans.Folders != "~/projects/work/" {
		t.Fatalf("~/projects/w + Tab = %q, want the first match, ~/projects/work/", ans.Folders)
	}

	// After a comma, only the last folder is completed.
	d.typeText(", ~/Doc")
	d.press(tea.KeyTab)
	if ans.Folders != "~/projects/work/, ~/Documents/" {
		t.Fatalf("second folder = %q", ans.Folders)
	}

	// With nothing left to complete, Tab moves on.
	d.press(tea.KeyTab)
	if d.focused() == "folders" {
		t.Fatalf("Tab with nothing to complete stayed on folders (value %q)", ans.Folders)
	}
}

func TestKeyFileCompletion(t *testing.T) {
	_, ans, d := newWizardAfter(t, func(s *sandbox) {
		s.Key("id_work", "jane@acme.com", "")
		s.Write(".ssh/config", "Host *\n")
		s.Write(".ssh/known_hosts", "")
		s.Mkdir("projects")
	})
	fillIdentity(d)
	d.advanceTo("auth")
	d.choose(2) // Generate, ~/.ssh/id_work, Another key file…, None
	if d.focused() != "auth-path" {
		t.Fatalf("focus on %q, want the auth key file", d.focused())
	}

	// ~/.ssh comes first in the home folder, though it's hidden.
	d.typeText("~/")
	d.press(tea.KeyTab)
	if ans.AuthPath != "~/.ssh/" {
		t.Fatalf("~/ + Tab = %q, want ~/.ssh/", ans.AuthPath)
	}
	// Only keys are offered, not the other files there.
	d.press(tea.KeyTab)
	if ans.AuthPath != "~/.ssh/id_work" {
		t.Fatalf("~/.ssh/ + Tab = %q, want ~/.ssh/id_work", ans.AuthPath)
	}
	d.press(tea.KeyEnter)
	if d.focused() != "signing" {
		t.Errorf("Enter on a completed key: focus on %q, want signing", d.focused())
	}
}

// Tab still moves on from inputs that don't suggest anything.
func TestTabMovesOnWithoutSuggestions(t *testing.T) {
	_, _, d := newWizard(t)
	d.typeText("work")
	d.press(tea.KeyTab)
	if d.focused() != "name" {
		t.Errorf("Tab on the ID: focus on %q, want name", d.focused())
	}
}

func TestPathSuggestions(t *testing.T) {
	s := newSandbox(t)
	for _, dir := range []string{"projects/api", "projects/Apps", "projects/.git", "src"} {
		s.Mkdir(dir)
	}
	s.Write("projects/notes.txt", "")
	s.Key("id_work", "jane@acme.com", "")
	a := s.newApp(s.Home)
	a.env.GOOS = "linux"

	cases := []struct {
		typed    string
		keyFiles bool
		want     []string
	}{
		{"", false, nil},
		{"~", false, []string{"~/"}},
		{"~/projects/a", false, []string{"~/projects/api/"}},                    // case counts on Linux
		{"~/projects/", false, []string{"~/projects/Apps/", "~/projects/api/"}}, // no hidden folders or files
		{"~/projects/.", false, []string{"~/projects/.git/"}},
		{"proj", false, []string{"projects/"}}, // relative to the current folder
		{s.Path("sr"), false, []string{s.Path("src") + "/"}},
		{"~/nowhere/", false, nil},
		{"~/", true, []string{"~/.ssh/", "~/fake-bin/", "~/projects/", "~/src/"}}, // fake-bin holds the sandbox's gh
		{"~/.ssh/i", true, []string{"~/.ssh/id_work"}},
	}
	for _, c := range cases {
		if got := a.pathSuggestions(c.typed, c.keyFiles); !slices.Equal(got, c.want) {
			t.Errorf("pathSuggestions(%q, %v) = %q, want %q", c.typed, c.keyFiles, got, c.want)
		}
	}

	a.env.GOOS = "darwin"
	if got := a.pathSuggestions("~/projects/a", false); !slices.Equal(got, []string{"~/projects/Apps/", "~/projects/api/"}) {
		t.Errorf("on macOS, case doesn't count: %q", got)
	}

	if got := a.folderSuggestions("~/src,  ~/proj"); !slices.Equal(got, []string{"~/src,  ~/projects/"}) {
		t.Errorf("folderSuggestions keeps the folders before = %q", got)
	}
	if got := a.folderSuggestions("~/src, "); got != nil {
		t.Errorf("folderSuggestions right after a comma = %q, want none", got)
	}

	for i := range maxSuggestions + 20 {
		s.Mkdir(fmt.Sprintf("many/d%03d", i))
	}
	if got := a.pathSuggestions("~/many/d", false); len(got) != maxSuggestions {
		t.Errorf("a huge folder gave %d suggestions, want %d", len(got), maxSuggestions)
	}
}

// Accessible prompts stay plain: no suggestions, and Tab isn't touched.
func TestNoSuggestionsInAccessibleMode(t *testing.T) {
	s := newSandbox(t)
	a := s.newApp(s.Home) // the sandbox runs forms in accessible mode
	ans, _ := a.wizardSteps(nil, &accounts.Account{Hosts: []string{accounts.DefaultHost}}, true)
	if ans.completions != nil {
		t.Errorf("accessible mode set up completions: %v", ans.completions)
	}
}
