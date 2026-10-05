package store

import (
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/paths"
)

func TestRenderIndex(t *testing.T) {
	env := &paths.Env{Home: "/home/jane", ConfigDir: "/home/jane/.config", GOOS: "linux"}
	list := []*accounts.Account{
		{ID: "personal", Default: true, Folders: []string{"~/projects/", "~/zz/"}},
		{ID: "work", Folders: []string{"~/projects/work/", "~/clients/"}},
	}
	want := indexHeader + `
# Default account, for repos outside every folder below
[include]
	path = ~/.config/doppel/accounts/personal.gitconfig

# Folder rules, from broad to specific (the last match wins)
[includeIf "gitdir:~/clients/"]
	path = ~/.config/doppel/accounts/work.gitconfig
[includeIf "gitdir:~/projects/"]
	path = ~/.config/doppel/accounts/personal.gitconfig
[includeIf "gitdir:~/zz/"]
	path = ~/.config/doppel/accounts/personal.gitconfig
[includeIf "gitdir:~/projects/work/"]
	path = ~/.config/doppel/accounts/work.gitconfig
`
	if got := string(renderIndex(env, list, "")); got != want {
		t.Errorf("renderIndex:\n%s\nwant:\n%s", got, want)
	}

	env.GOOS = "darwin"
	list[0].Default = false
	got := string(renderIndex(env, list, ""))
	if !strings.Contains(got, `[includeIf "gitdir/i:~/clients/"]`) {
		t.Errorf("macOS rules should ignore case:\n%s", got)
	}
	if strings.Contains(got, "[include]\n") {
		t.Errorf("index includes a default account although there is none:\n%s", got)
	}
}

func TestQuoteValue(t *testing.T) {
	cases := map[string]string{
		"~/.config/doppel/index.gitconfig": "~/.config/doppel/index.gitconfig",
		"/path with spaces/x":              "/path with spaces/x",
		"/a#b":                             `"/a#b"`,
		"/a;b":                             `"/a;b"`,
		` /lead`:                           `" /lead"`,
		`/a"b\c`:                           `"/a\"b\\c"`,
	}
	for in, want := range cases {
		if got := quoteValue(in); got != want {
			t.Errorf("quoteValue(%q) = %q, want %q", in, got, want)
		}
		if back := unquoteValue(quoteValue(in)); back != in {
			t.Errorf("unquoteValue(quoteValue(%q)) = %q", in, back)
		}
	}
}
