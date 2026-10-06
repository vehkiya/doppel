package hosts_test

import (
	"strings"
	"testing"

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
