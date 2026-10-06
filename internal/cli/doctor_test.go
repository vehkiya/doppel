package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
)

// doctorSandbox has a default account and a work account with a key of
// its own, signing with it, bound to an existing folder.
func doctorSandbox(t *testing.T) *sandbox {
	t.Helper()
	s := newSandbox(t)
	// These tests count warnings. On a Mac, keys with a passphrase would add
	// the Keychain checks, which depend on the machine's ssh; those have
	// their own tests.
	s.goos = "linux"
	s.Key("id_personal", "jane@personal.dev", "a passphrase")
	s.Key("id_work", "jane@acme.com", "a passphrase")
	s.Mkdir("projects/work")
	s.addAccount("personal", "jane@personal.dev", "--auth-key", "~/.ssh/id_personal")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key", "--github-user", "jane-acme", "--folder", "~/projects/work")
	return s
}

func TestDoctorOnAHealthySetup(t *testing.T) {
	s := doctorSandbox(t)
	out := s.mustRun("doctor")
	for _, want := range []string{"✓ Git ", "✓ OpenSSH ", "~/.gitconfig includes doppel's accounts", "doppel's files match the accounts",
		"Auth key ~/.ssh/id_work (passphrase)", "Signs commits and tags with the auth key", "No problems found"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output is missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorSeesWhatGitSees(t *testing.T) {
	s := doctorSandbox(t)
	// A line Git rejects, in a file only Git reads. doppel's own calls ignore
	// the user's config, so only a check run as the user finds it.
	s.Write(".config/doppel/index.gitconfig", s.Read(".config/doppel/index.gitconfig")+"[includeIf \"gitdir:~/a\"b/\"]\n\tpath = x\n")
	s.mustFail(1, "doctor")
	out := s.stdout.String()
	if !strings.Contains(out, "Git can't read your global config") || !strings.Contains(out, "bad config line") {
		t.Errorf("doctor doesn't report that Git can't read the config:\n%s", out)
	}

	// --fix rewrites the index, and Git can read the config again.
	s.mustRun("doctor", "--fix")
	if out := s.stdout.String(); !strings.Contains(out, "Git reads your global config") || strings.Contains(out, "✗") {
		t.Errorf("doctor --fix:\n%s", out)
	}
}

func TestDoctorFindsProblems(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *sandbox)
		want  string
	}{
		{"GIT_SSH_COMMAND overrides every account", func(s *sandbox) {
			s.T.Setenv("GIT_SSH_COMMAND", "ssh -i ~/.ssh/other")
		}, "GIT_SSH_COMMAND=ssh -i ~/.ssh/other is set"},
		{"a global setting after the include", func(s *sandbox) {
			s.Write(".gitconfig", s.Read(".gitconfig")+"[user]\n\temail = late@example.com\n")
		}, "~/.gitconfig sets user.email after doppel's include"},
		{"the include is gone", func(s *sandbox) {
			s.mustRun("uninstall", "--yes")
		}, "~/.gitconfig doesn't include doppel's accounts"},
		{"a missing auth key", func(s *sandbox) {
			_ = os.Remove(s.Path(".ssh/id_work"))
			_ = os.Remove(s.Path(".ssh/id_work.pub"))
		}, "Auth key ~/.ssh/id_work doesn't exist"},
		{"two accounts share an auth key", func(s *sandbox) {
			s.mustRun("edit", "personal", "--auth-key", "~/.ssh/id_work")
		}, "personal and work use the same auth key on github.com"},
		{"two accounts use ssh's own keys on one host", func(s *sandbox) {
			s.mustRun("edit", "personal", "--auth-key", "")
			s.mustRun("edit", "work", "--auth-key", "", "--no-signing")
		}, "personal and work both log in to github.com with ssh's own keys"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := doctorSandbox(t)
			c.setup(s)
			s.mustFail(1, "doctor")
			if out := s.stdout.String(); !strings.Contains(out, c.want) {
				t.Errorf("doctor output is missing %q:\n%s", c.want, out)
			}
		})
	}
}

func TestDoctorWarnings(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(s *sandbox)
		want     string
		warnings string
	}{
		{"a key without a passphrase", func(s *sandbox) {
			s.Key("id_plain", "jane@acme.com", "")
			s.mustRun("edit", "work", "--auth-key", "~/.ssh/id_plain", "--sign-with-auth-key")
		}, "Auth key ~/.ssh/id_plain has no passphrase", "1 warning"},
		{"a folder that doesn't exist yet", func(s *sandbox) {
			s.mustRun("bind", "work", "~/clients")
		}, "Folder ~/clients/ doesn't exist yet", "1 warning"},
		{"~/.ssh/config offers another key for the host", func(s *sandbox) {
			s.Key("id_old", "jane@old.dev", "")
			s.Write(".ssh/config", "Host github.com\n  IdentityFile ~/.ssh/id_old\n")
		}, "~/.ssh/config also offers ~/.ssh/id_old for github.com", "2 warnings"}, // one per github.com account
		{"a repo fetching over HTTPS", func(s *sandbox) {
			repo := s.GitInit("projects/work/api")
			if _, err := git.Run(repo, "remote", "add", "origin", "https://github.com/acme/api.git"); err != nil {
				s.T.Fatal(err)
			}
		}, "1 repo in its folders fetches over HTTPS, which doppel's keys don't cover: ~/projects/work/api", "1 warning"},
		{"doppel's files were edited by hand", func(s *sandbox) {
			s.SetConfig(accountsDir+"work.gitconfig", accounts.KeySSHCommand, "ssh -i ~/.ssh/something-else")
		}, "doppel's files don't match the accounts: ~/.config/doppel/accounts/work.gitconfig", "1 warning"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := doctorSandbox(t)
			c.setup(s)
			out := s.mustRun("doctor") // warnings alone don't fail
			if !strings.Contains(out, c.want) || !strings.Contains(out, "0 problems, "+c.warnings+"\n") {
				t.Errorf("doctor output is missing %q, or doesn't count %s:\n%s", c.want, c.warnings, out)
			}
		})
	}
}

func TestDoctorFix(t *testing.T) {
	s := doctorSandbox(t)
	s.SetConfig(accountsDir+"work.gitconfig", accounts.KeySSHCommand, "ssh -i ~/.ssh/something-else")
	s.mustRun("uninstall", "--yes")
	s.mustFail(1, "doctor")

	out := s.mustRun("doctor", "--fix")
	for _, want := range []string{"Updated ~/.config/doppel/accounts/work.gitconfig", "Updated ~/.gitconfig", "No problems found"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor --fix output is missing %q:\n%s", want, out)
		}
	}
	if got := s.GitConfig(s.GitInit("projects/work/api"), accounts.KeySSHCommand); got != "ssh -i ~/.ssh/id_work -o IdentitiesOnly=yes" {
		t.Errorf("core.sshCommand after --fix = %q", got)
	}
}

func TestDoctorWithoutAccounts(t *testing.T) {
	s := newSandbox(t)
	out := s.mustRun("doctor")
	if !strings.Contains(out, "No accounts yet") || s.Exists(".gitconfig") || s.Exists(".config/doppel") {
		t.Errorf("doctor without accounts:\n%s", out)
	}
}
