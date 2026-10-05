package git

import (
	"slices"
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
		major, minor, ok := ParseVersion(in)
		if !ok || major != want[0] || minor != want[1] {
			t.Errorf("parseGitVersion(%q) = %d, %d, %v", in, major, minor, ok)
		}
	}
	if _, _, ok := ParseVersion("not git"); ok {
		t.Error("parseGitVersion accepted garbage")
	}
}
