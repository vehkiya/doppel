package hosts_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/hosts"
	"github.com/vehkiya/doppel/internal/testenv"
)

func TestSSHHost(t *testing.T) {
	cases := map[string]string{
		"git@github.com:jane/repo.git":          "github.com",
		"ssh://git@gitlab.com/jane/repo.git":    "gitlab.com",
		"ssh://git@git.acme.com:2222/jane/repo": "git.acme.com",
		"ssh://git@[::1]:22/repo":               "::1",
		"github.com:jane/repo":                  "github.com",
		"https://github.com/jane/repo.git":      "",
		"/srv/git/repo.git":                     "",
		"./repo:with-colon":                     "",
	}
	for url, want := range cases {
		if got := hosts.SSHHost(url); got != want {
			t.Errorf("SSHHost(%q) = %q, want %q", url, got, want)
		}
	}
	if got := hosts.Scheme("https://github.com/jane/repo"); got != "HTTPS" {
		t.Errorf("Scheme = %q", got)
	}
	if got := hosts.Scheme("/srv/git/repo.git"); got != "a local path" {
		t.Errorf("Scheme = %q", got)
	}
}

func TestSSHURL(t *testing.T) {
	cases := map[string]string{ //nolint:gosec // a made-up token, to check it is dropped
		"https://github.com/vehkiya/configsh.git":         "git@github.com:vehkiya/configsh.git",
		"https://github.com/vehkiya/configsh":             "git@github.com:vehkiya/configsh",
		"http://gitlab.acme.com/group/sub/repo.git/":      "git@gitlab.acme.com:group/sub/repo.git",
		"https://jane:ghp_secret@github.com/acme/api.git": "git@github.com:acme/api.git", // the token is dropped
		"https://git.acme.com:8443/acme/api.git":          "",                            // the SSH port can't be told
		"https://github.com/":                             "",
		"git@github.com:acme/api.git":                     "",
		"ssh://git@github.com/acme/api.git":               "",
		"https://github.com/acme/api.git?ref=main#readme": "",
	}
	for url, want := range cases {
		got, ok := hosts.SSHURL(url)
		if got != want || ok != (want != "") {
			t.Errorf("SSHURL(%q) = %q, %v; want %q", url, got, ok, want)
		}
	}
}

func TestSwitchToSSH(t *testing.T) {
	r := hosts.Remote{Name: "origin", FetchURL: "https://github.com/acme/api.git", PushURL: "https://github.com/acme/api.git"}
	if got := hosts.SwitchToSSH(r, ""); got != "git remote set-url origin git@github.com:acme/api.git" {
		t.Errorf("SwitchToSSH = %q", got)
	}
	if got := hosts.SwitchToSSH(r, "~/my repos/it's"); got != `git -C ~/'my repos/it'\''s' remote set-url origin git@github.com:acme/api.git` {
		t.Errorf("SwitchToSSH quotes the folder as %q", got)
	}
	r.FetchURL = "git@github.com:acme/api.git" // only pushing goes over HTTPS
	if got := hosts.SwitchToSSH(r, ""); got != "git remote set-url --push origin git@github.com:acme/api.git" {
		t.Errorf("SwitchToSSH = %q", got)
	}
	if got := hosts.SwitchToSSH(hosts.Remote{Name: "origin", FetchURL: "git@github.com:a/b", PushURL: "git@github.com:a/b"}, ""); got != "" {
		t.Errorf("SwitchToSSH on an SSH remote = %q", got)
	}
}

func TestRemotes(t *testing.T) {
	s := testenv.New(t)
	repo := s.GitInit("repo")
	for _, args := range [][]string{
		{"remote", "add", "upstream", "https://github.com/up/api.git"},
		{"remote", "add", "origin", "https://github.com/me/api.git"},
		{"remote", "set-url", "--push", "origin", "git@github.com:me/api.git"},
	} {
		if _, err := git.Run(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	got := hosts.Remotes(repo)
	want := []hosts.Remote{
		{Name: "origin", FetchURL: "https://github.com/me/api.git", PushURL: "git@github.com:me/api.git"},
		{Name: "upstream", FetchURL: "https://github.com/up/api.git", PushURL: "https://github.com/up/api.git"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Remotes = %+v\nwant %+v", got, want)
	}
	if main, ok := hosts.MainRemote(repo); !ok || main.Name != "origin" {
		t.Errorf("MainRemote = %+v, %v", main, ok)
	}
}

func TestSteps(t *testing.T) {
	testenv.New(t) // a gh signed in nowhere, so only known names count as GitHub
	var g hosts.GitHub
	both := hosts.Use{Auth: true, Signing: true}

	steps := strings.Join(g.Steps("ssh.github.com", both, "doppel: work (mac)", "~/.ssh/id_work.pub"), "\n")
	for _, want := range []string{"https://github.com/settings/ssh/new", "Title: doppel: work (mac)", "a second time with Key type: Signing Key"} {
		if !strings.Contains(steps, want) {
			t.Errorf("GitHub steps are missing %q:\n%s", want, steps)
		}
	}
	if steps := strings.Join(g.Steps("gitlab.example.com", both, "t", "k"), "\n"); !strings.Contains(steps, "Usage type: Authentication & Signing") {
		t.Errorf("GitLab steps:\n%s", steps)
	}
	steps = strings.Join(g.Steps("codeberg.org", hosts.Use{Signing: true}, "t", "~/.ssh/id_work.pub"), "\n")
	if !strings.Contains(steps, "/user/settings/keys") || !strings.Contains(steps, "-f ~/.ssh/id_work.pub") {
		t.Errorf("Gitea steps:\n%s", steps)
	}

	if !g.Any([]string{"codeberg.org", "acme.ghe.com"}) || g.Is("codeberg.org") {
		t.Error("GitHub detection by name is wrong")
	}
	if got := g.APIHosts([]string{"github.com", "ssh.github.com", "acme.ghe.com", "codeberg.org"}); strings.Join(got, ",") != "github.com,acme.ghe.com" {
		t.Errorf("APIHosts = %v", got)
	}
}
