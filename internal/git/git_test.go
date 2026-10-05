package git

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseConfigList(t *testing.T) {
	plain := "user.name\nJane Doe\x00commit.gpgsign\x00user.signingkey\n\x00core.editor\nvim\nwith newline\x00"
	got := ParseConfigList(plain, false)
	want := []ConfigEntry{
		{Key: "user.name", Value: "Jane Doe"},
		{Key: "commit.gpgsign", Value: "true"}, // a key without a value is true
		{Key: "user.signingkey", Value: ""},
		{Key: "core.editor", Value: "vim\nwith newline"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseConfigList(plain) = %#v", got)
	}

	withOrigin := "global\x00file:/home/jane/.gitconfig\x00user.email\njane@x.io\x00local\x00file:.git/config\x00user.email\nother@x.io\x00"
	got = ParseConfigList(withOrigin, true)
	want = []ConfigEntry{
		{Scope: "global", Origin: "file:/home/jane/.gitconfig", Key: "user.email", Value: "jane@x.io"},
		{Scope: "local", Origin: "file:.git/config", Key: "user.email", Value: "other@x.io"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseConfigList(withOrigin) = %#v", got)
	}
	if got := ParseConfigList("", true); got != nil {
		t.Errorf("parseConfigList(empty) = %#v", got)
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string][2]int{
		"git version 2.56.0":                 {2, 56},
		"git version 2.39.5 (Apple Git-154)": {2, 39},
		"git version 2.45.1.windows.1":       {2, 45},
	}
	for in, want := range cases {
		major, minor, ok := parseVersion(in)
		if !ok || major != want[0] || minor != want[1] {
			t.Errorf("parseGitVersion(%q) = %d, %d, %v", in, major, minor, ok)
		}
	}
	if _, _, ok := parseVersion("not git"); ok {
		t.Error("parseGitVersion accepted garbage")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReadConfigFileIncludes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	main := filepath.Join(home, ".gitconfig")
	writeFile(t, main, "[a]\n\tv = main-1\n[include]\n\tpath = ~/conf/home.inc\n[a]\n\tv = main-2\n"+
		"[include]\n\tpath = skipped/own.inc\n[includeIf \"gitdir:/\"]\n\tpath = conditional.inc\n")
	writeFile(t, filepath.Join(home, "conf", "home.inc"), "[a]\n\tv = home\n[include]\n\tpath = nested.inc\n")
	writeFile(t, filepath.Join(home, "conf", "nested.inc"), "[a]\n\tv = nested\n")
	writeFile(t, filepath.Join(home, "skipped", "own.inc"), "this isn't valid config, but it's skipped\n")
	writeFile(t, filepath.Join(home, "conditional.inc"), "[a]\n\tv = conditional\n")

	skipped := filepath.Join(home, "skipped")
	entries, err := ReadConfigFileIncludes(main, func(path string) bool { return filepath.Dir(path) == skipped })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if e.Key == "a.v" {
			rel, _ := filepath.Rel(home, e.Origin)
			got = append(got, e.Value+" from "+rel)
		}
	}
	want := []string{"main-1 from .gitconfig", "home from conf/home.inc", "nested from conf/nested.inc", "main-2 from .gitconfig"}
	if !slices.Equal(got, want) {
		t.Errorf("values in the order Git reads them:\n got %q\nwant %q", got, want)
	}

	if entries, err := ReadConfigFileIncludes(filepath.Join(home, "missing"), func(string) bool { return false }); err != nil || entries != nil {
		t.Errorf("missing file = %v, %v; want no values", entries, err)
	}

	loop := filepath.Join(home, "loop.inc")
	writeFile(t, loop, "[include]\n\tpath = loop.inc\n")
	if _, err := ReadConfigFileIncludes(loop, func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "more than 10 deep") {
		t.Errorf("a file including itself: %v", err)
	}
}

func TestReadConfigFromText(t *testing.T) {
	values, err := ReadConfig([]byte("[user]\n\temail = jane@x.io\n[core]\n\tsshCommand = ssh\n"))
	if err != nil {
		t.Fatal(err)
	}
	if values["user.email"][0] != "jane@x.io" || values["core.sshcommand"][0] != "ssh" {
		t.Errorf("values = %q", values)
	}
	if _, err := ReadConfig([]byte("not config\n")); err == nil {
		t.Error("text Git can't read gave no error")
	}
}
