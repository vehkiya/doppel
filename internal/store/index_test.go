package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestStaleIndex(t *testing.T) {
	tmp := t.TempDir()
	env := &paths.Env{Home: tmp, ConfigDir: filepath.Join(tmp, ".config"), GOOS: "linux"}
	list := []*accounts.Account{
		{ID: "work", File: env.AccountPath("work"), Folders: []string{"~/work/"}},
	}

	// 1. Missing index with accounts -> stale.
	if !StaleIndex(env, list) {
		t.Error("expected StaleIndex = true when index is missing")
	}

	// 2. Missing index with 0 accounts -> not stale.
	if StaleIndex(env, nil) {
		t.Error("expected StaleIndex = false when index is missing and no accounts exist")
	}

	// Write accounts dir and account file.
	if err := os.MkdirAll(env.AccountsDir(), 0700); err != nil {
		t.Fatal(err)
	}
	accFile := env.AccountPath("work")
	if err := os.WriteFile(accFile, []byte("[doppel]\n\taccount = work\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Write up-to-date index.
	if err := os.WriteFile(env.IndexPath(), renderIndex(env, list, ""), 0600); err != nil {
		t.Fatal(err)
	}
	// Ensure index modtime is newer than account file and accounts dir.
	past := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(env.AccountsDir(), past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(accFile, past, past); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(env.IndexPath(), now, now); err != nil {
		t.Fatal(err)
	}

	if StaleIndex(env, list) {
		t.Error("expected StaleIndex = false when index is up to date")
	}

	// 3. Newer accounts directory -> stale.
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(env.AccountsDir(), future, future); err != nil {
		t.Fatal(err)
	}
	if !StaleIndex(env, list) {
		t.Error("expected StaleIndex = true when accounts dir is newer")
	}
	if err := os.Chtimes(env.AccountsDir(), past, past); err != nil {
		t.Fatal(err)
	}

	// 4. Newer account file -> stale.
	if err := os.Chtimes(accFile, future, future); err != nil {
		t.Fatal(err)
	}
	if !StaleIndex(env, list) {
		t.Error("expected StaleIndex = true when account file is newer")
	}
	if err := os.Chtimes(accFile, past, past); err != nil {
		t.Fatal(err)
	}

	// 5. Content mismatch (e.g. list has a new folder, but modtimes were kept).
	list[0].Folders = append(list[0].Folders, "~/clients/")
	if !StaleIndex(env, list) {
		t.Error("expected StaleIndex = true when folder rules don't match")
	}
}
