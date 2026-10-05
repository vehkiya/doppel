package accounts

import (
	"slices"
	"testing"

	"github.com/vehkiya/doppel/internal/paths"
)

func TestFolderRulesAndMatch(t *testing.T) {
	env := &paths.Env{Home: "/home/jane", GOOS: "linux"}
	list := []*Account{
		{ID: "client", Folders: []string{"~/projects/work/client/"}},
		{ID: "personal", Folders: []string{"~/projects/", "/srv/code/"}},
		{ID: "work", Folders: []string{"~/projects/work/"}},
	}

	var order []string
	for _, r := range FolderRules(env, list) {
		order = append(order, r.Folder)
	}
	want := []string{"/srv/code/", "~/projects/", "~/projects/work/", "~/projects/work/client/"} // ~/ is /home/jane: deeper than /srv
	if !slices.Equal(order, want) {
		t.Errorf("rules from broad to specific:\n got %q\nwant %q", order, want)
	}

	cases := map[string]string{
		"/home/jane/projects/work/client/app": "client",
		"/home/jane/projects/work/api":        "work",
		"/home/jane/projects/work":            "work",
		"/home/jane/projects/workshop":        "personal",
		"/srv/code/tool":                      "personal",
		"/home/jane/elsewhere":                "",
	}
	for path, wantID := range cases {
		r, ok := MatchFolder(env, list, path)
		if r.ID != wantID || ok != (wantID != "") {
			t.Errorf("MatchFolder(%s) = %+v, %v; want %q", path, r, ok, wantID)
		}
	}
}

func TestMatchFolderPicksWhatGitPicksOnATie(t *testing.T) {
	// On macOS, gitdir/i: makes ~/Work/ and ~/work/ both match, and Git takes
	// the later rule in the index.
	env := &paths.Env{Home: "/home/jane", GOOS: "darwin"}
	list := []*Account{
		{ID: "lower", Folders: []string{"~/work/"}},
		{ID: "upper", Folders: []string{"~/Work/"}},
	}
	rules := FolderRules(env, list)
	last := rules[len(rules)-1]
	if r, ok := MatchFolder(env, list, "/home/jane/work/repo"); !ok || r != last {
		t.Errorf("MatchFolder = %+v, %v; want the index's last matching rule %+v", r, ok, last)
	}
}
