package paths_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/testenv"
)

func TestNormalizeFolder(t *testing.T) {
	s := testenv.New(t)
	env := s.Env()
	s.Mkdir("projects/work")
	s.Write("notes.txt", "")
	if err := os.Symlink(s.Path("projects"), s.Path("p")); err != nil {
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
		{"work", s.Path("projects"), "~/projects/work/", true},
		{"..", s.Path("projects/work"), "~/projects/", true},
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
	env := &paths.Env{Home: "/home/jane", GOOS: "linux"}
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
	mac := &paths.Env{Home: "/Users/jane", GOOS: "darwin"}
	if !mac.FolderContains("~/Projects/Work/", "/Users/jane/projects/work/api") {
		t.Error("macOS folder rules should ignore case")
	}
}

func TestHomePaths(t *testing.T) {
	env := &paths.Env{Home: "/home/jane"}
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
