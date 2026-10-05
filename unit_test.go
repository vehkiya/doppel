package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNormalizeFolder(t *testing.T) {
	s := newSandbox(t)
	env := s.env()
	s.mkdir("projects/work")
	s.write("notes.txt", "")
	if err := os.Symlink(s.path("projects"), s.path("p")); err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		input, cwd string
		want       string
		exists     bool
	}{
		{"~/projects/work", "/", "~/projects/work/", true},
		{"~/projects/work/", "/", "~/projects/work/", true},
		{"work", s.path("projects"), "~/projects/work/", true},
		{"..", s.path("projects/work"), "~/projects/", true},
		{"~/p/work", "/", "~/projects/work/", true},                  // symlink resolved
		{"~/p/new/sub", "/", "~/projects/new/sub/", false},           // nearest existing parent resolved
		{"~", "/", "~/", true},                                       // the whole home directory
		{outside, "/", strings.TrimSuffix(outside, "/") + "/", true}, // outside home stays absolute
	}
	for _, c := range cases {
		got, exists, err := env.NormalizeFolder(c.input, c.cwd)
		if err != nil || got != c.want || exists != c.exists {
			t.Errorf("NormalizeFolder(%q) = %q, %v, %v; want %q, %v", c.input, got, exists, err, c.want, c.exists)
		}
	}
	for _, bad := range []string{"", "  ", "~/notes.txt", "~/a*", "~/a?", "~/[ab]", `~/a"b`, `~/a\b`} {
		if got, _, err := env.NormalizeFolder(bad, "/"); err == nil {
			t.Errorf("NormalizeFolder(%q) = %q, want an error", bad, got)
		}
	}
}

func TestFolderContains(t *testing.T) {
	env := &Env{Home: "/home/jane", GOOS: "linux"}
	cases := []struct {
		folder, path string
		want         bool
	}{
		{"~/projects/work/", "/home/jane/projects/work", true},
		{"~/projects/work/", "/home/jane/projects/work/api/.git", true},
		{"~/projects/work/", "/home/jane/projects/workshop", false},
		{"~/projects/work/", "/home/jane/projects", false},
		{"~/projects/work/", "/home/jane/Projects/Work/api", false},
		{"/srv/git/", "/srv/git/repo", true},
	}
	for _, c := range cases {
		if got := env.FolderContains(c.folder, c.path); got != c.want {
			t.Errorf("FolderContains(%q, %q) = %v, want %v", c.folder, c.path, got, c.want)
		}
	}
	mac := &Env{Home: "/Users/jane", GOOS: "darwin"}
	if !mac.FolderContains("~/Projects/Work/", "/Users/jane/projects/work/api") {
		t.Error("macOS folder rules should ignore case")
	}
}

func TestHomePaths(t *testing.T) {
	env := &Env{Home: "/home/jane"}
	for in, want := range map[string]string{"~": "/home/jane", "~/a/": "/home/jane/a/", "/x": "/x", "~other": "~other"} {
		if got := env.Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"/home/jane": "~", "/home/jane/a/": "~/a/", "/home/janet": "/home/janet", "/x": "/x"} {
		if got := env.Shorten(in); got != want {
			t.Errorf("Shorten(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseConfigList(t *testing.T) {
	plain := "user.name\nJane Doe\x00commit.gpgsign\x00user.signingkey\n\x00core.editor\nvim\nwith newline\x00"
	got := parseConfigList(plain, false)
	want := []configEntry{
		{Key: "user.name", Value: "Jane Doe"},
		{Key: "commit.gpgsign", Value: "true"}, // a key without a value is true
		{Key: "user.signingkey", Value: ""},
		{Key: "core.editor", Value: "vim\nwith newline"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseConfigList(plain) = %#v", got)
	}

	withOrigin := "global\x00file:/home/jane/.gitconfig\x00user.email\njane@x.io\x00local\x00file:.git/config\x00user.email\nother@x.io\x00"
	got = parseConfigList(withOrigin, true)
	want = []configEntry{
		{Scope: "global", Origin: "file:/home/jane/.gitconfig", Key: "user.email", Value: "jane@x.io"},
		{Scope: "local", Origin: "file:.git/config", Key: "user.email", Value: "other@x.io"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseConfigList(withOrigin) = %#v", got)
	}
	if got := parseConfigList("", true); got != nil {
		t.Errorf("parseConfigList(empty) = %#v", got)
	}
}

func TestParseGitVersion(t *testing.T) {
	cases := map[string][2]int{
		"git version 2.56.0":                 {2, 56},
		"git version 2.39.5 (Apple Git-154)": {2, 39},
		"git version 2.45.1.windows.1":       {2, 45},
	}
	for in, want := range cases {
		major, minor, ok := parseGitVersion(in)
		if !ok || major != want[0] || minor != want[1] {
			t.Errorf("parseGitVersion(%q) = %d, %d, %v", in, major, minor, ok)
		}
	}
	if _, _, ok := parseGitVersion("not git"); ok {
		t.Error("parseGitVersion accepted garbage")
	}
}

func TestSSHCommand(t *testing.T) {
	cases := map[string]string{
		"":                    "ssh",
		"~/.ssh/id_ed25519_w": "ssh -i ~/.ssh/id_ed25519_w -o IdentitiesOnly=yes",
		"~/my keys/id":        "ssh -i '~/my keys/id' -o IdentitiesOnly=yes",
		"/keys/it's":          `ssh -i '/keys/it'\''s' -o IdentitiesOnly=yes`,
	}
	for key, want := range cases {
		if got := sshCommand(key); got != want {
			t.Errorf("sshCommand(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestRenderIndex(t *testing.T) {
	env := &Env{Home: "/home/jane", ConfigDir: "/home/jane/.config", GOOS: "linux"}
	accounts := []*Account{
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
	if got := string(renderIndex(env, accounts)); got != want {
		t.Errorf("renderIndex:\n%s\nwant:\n%s", got, want)
	}

	env.GOOS = "darwin"
	accounts[0].Default = false
	got := string(renderIndex(env, accounts))
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

func TestValidateAccounts(t *testing.T) {
	env := &Env{Home: "/home/jane", GOOS: "linux"}
	acc := func(id string, def bool, folders ...string) *Account {
		return &Account{ID: id, Name: "Jane", Email: id + "@x.io", Default: def, Folders: folders}
	}
	cases := []struct {
		accounts []*Account
		want     string
	}{
		{[]*Account{acc("a", true), acc("b", false)}, ""},
		{[]*Account{acc("a", true), acc("b", true)}, "all marked as the default"},
		{[]*Account{acc("a", false), acc("a", false)}, "two accounts named a"},
		{[]*Account{acc("a", false, "~/x/"), acc("b", false, "~/x/")}, "bound to both a and b"},
		{[]*Account{acc("a", false, "~/x/", "~/x/")}, "lists ~/x/ twice"},
	}
	for _, c := range cases {
		err := validateAccounts(env, c.accounts)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("unexpected error: %v", err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("error = %v, want one mentioning %q", err, c.want)
		}
	}
}

func TestWriteDiff(t *testing.T) {
	old := []byte("a\nb\nc\nd\ne\nf\ng\nh\n")
	updated := []byte("a\nB\nc\nd\ne\nf\ng\nh\ni\n")
	var buf bytes.Buffer
	writeDiff(&buf, "~/f", fileChange{Old: old, New: updated, Existed: true, Exists: true})
	want := `--- ~/f
+++ ~/f
@@ -1,4 +1,4 @@
 a
-b
+B
 c
 d
@@ -7,2 +7,3 @@
 g
 h
+i
`
	if got := buf.String(); got != want {
		t.Errorf("writeDiff:\n%s\nwant:\n%s", got, want)
	}

	buf.Reset()
	writeDiff(&buf, "~/new", fileChange{New: []byte("x\n"), Exists: true})
	if got, want := buf.String(), "new file ~/new\n@@ -0,0 +1,1 @@\n+x\n"; got != want {
		t.Errorf("writeDiff(new file) = %q, want %q", got, want)
	}
}
