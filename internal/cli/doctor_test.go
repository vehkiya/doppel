package cli

import (
	"os"
	"strings"
	"testing"
	"time"

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
	s.FakeAgent(s.Path(".ssh/id_personal"), s.Path(".ssh/id_work")) // signing needs the work key in the agent
	return s
}

func TestDoctorOnAHealthySetup(t *testing.T) {
	s := doctorSandbox(t)
	out := s.mustRun("doctor")
	for _, want := range []string{"✓ Git ", "✓ OpenSSH ", "~/.gitconfig includes doppel's accounts", "doppel's files match the accounts",
		"Auth key ~/.ssh/id_work (passphrase, in agent)", "Signs commits and tags with the auth key", "No problems found"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output is missing %q:\n%s", want, out)
		}
	}
}

// Signing with a key that has a passphrase needs it in the agent:
// ssh-keygen never reads the macOS Keychain, so every commit would ask.
func TestDoctorWantsTheSigningKeyInTheAgent(t *testing.T) {
	for _, c := range []struct{ name, setup, want string }{
		{"the agent doesn't hold it", "running",
			"Signing key ~/.ssh/id_work.pub has a passphrase but isn't in your agent, so Git asks for it on every signed commit\n    ↳ Load it with `ssh-add ~/.ssh/id_work`"},
		{"no agent", "none", "no ssh-agent is running to hold it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := doctorSandbox(t)
			if c.setup == "running" {
				s.FakeAgent(s.Path(".ssh/id_personal")) // the work key, which signs, isn't in it
			} else {
				_ = os.Remove(s.Path("fake-bin/ssh-add"))
			}
			out := s.mustRun("doctor") // a warning
			if !strings.Contains(out, c.want) || !strings.Contains(out, "0 problems, 1 warning\n") {
				t.Errorf("doctor output is missing %q:\n%s", c.want, out)
			}
		})
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
			// A token in the URL never shows up in the fix.
			if _, err := git.Run(repo, "remote", "add", "origin", "https://jane:ghp_secret@github.com/acme/api.git"); err != nil {
				s.T.Fatal(err)
			}
		}, "~/projects/work/api fetches over HTTPS, which doppel's keys don't cover\n" +
			"    ↳ Switch it to SSH: git -C ~/projects/work/api remote set-url origin git@github.com:acme/api.git", "1 warning"},
		{"doppel's files were edited by hand", func(s *sandbox) {
			s.SetConfig(accountsDir+"work.gitconfig", accounts.KeySSHCommand, "ssh -i ~/.ssh/something-else")
		}, "doppel's files don't match the accounts: ~/.config/doppel/accounts/work.gitconfig", "1 warning"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := doctorSandbox(t)
			c.setup(s)
			out := s.mustRun("doctor") // warnings alone don't fail
			if strings.Contains(out, "ghp_secret") {
				t.Errorf("doctor printed a token:\n%s", out)
			}
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

func TestDoctorFixStaleIndex(t *testing.T) {
	s := doctorSandbox(t)

	// Make index older than accounts (simulates account arriving via sync or edit).
	past := time.Now().Add(-100 * time.Second)
	indexPath := s.Path(".config/doppel/index.gitconfig")
	if err := os.Chtimes(indexPath, past, past); err != nil {
		t.Fatal(err)
	}

	// doctor warns about stale index.
	out := s.mustRun("doctor")
	if !strings.Contains(out, "doppel's files don't match the accounts: ~/.config/doppel/index.gitconfig") {
		t.Errorf("doctor didn't warn about stale index:\n%s", out)
	}

	// doctor --fix brings index up to date even though content matches.
	out = s.mustRun("doctor", "--fix")
	if !strings.Contains(out, "Updated ~/.config/doppel/index.gitconfig") {
		t.Errorf("doctor --fix didn't update index:\n%s", out)
	}
	if !strings.Contains(out, "No problems found") {
		t.Errorf("doctor --fix didn't report clean state:\n%s", out)
	}

	// whoami reports fresh index.
	s.run("whoami", "projects/work")
	if strings.Contains(s.stderr.String(), "doppel's index is out of date") {
		t.Errorf("whoami still warned after doctor --fix:\n%s", s.stderr.String())
	}
}

func TestEditDoesNotStaleIndex(t *testing.T) {
	s := doctorSandbox(t)
	s.mustRun("edit", "work", "--email", "jane@new.com")
	s.run("whoami", "projects/work")
	if strings.Contains(s.stderr.String(), "doppel's index is out of date") {
		t.Errorf("whoami warned about stale index after edit:\n%s", s.stderr.String())
	}
}

func TestDoctorIDESettings(t *testing.T) {
	s := doctorSandbox(t)
	s.Write(".config/JetBrains/IntelliJIdea2024.1/options/git.xml", `<application>
  <component name="Git.Application.Settings">
    <option name="SSH_EXECUTABLE" value="IDEA_SSH" />
  </component>
</application>`)

	s.Write(".config/Code/User/settings.json", `{
  "git.useBuiltinCredentialProvider": true
}`)

	s.mustFail(1, "doctor")
	out := s.stdout.String()
	if !strings.Contains(out, "IDE settings") {
		t.Errorf("doctor missing IDE settings heading:\n%s", out)
	}
	if !strings.Contains(out, "IntelliJ IDEA") || !strings.Contains(out, "built-in SSH executable") {
		t.Errorf("doctor missing IntelliJ warning:\n%s", out)
	}
	if !strings.Contains(out, "VS Code") || !strings.Contains(out, "git.useBuiltinCredentialProvider") {
		t.Errorf("doctor missing VS Code warning:\n%s", out)
	}
}

func TestDoctorRunFromRepoWithIDE(t *testing.T) {
	s := doctorSandbox(t)
	repo := s.GitInit("projects/work/api")
	s.Write("projects/work/api/.vscode/settings.json", `{
  "git.useBuiltinCredentialProvider": false
}`)
	s.Write("projects/work/api/.idea/vcs.xml", `<project>
  <component name="Git.Settings">
    <option name="SSH_EXECUTABLE" value="IDEA_SSH" />
  </component>
</project>`)

	s.stdout.Reset()
	s.stderr.Reset()
	code := s.runIn(repo, "doctor")
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	out := s.stdout.String()
	if !strings.Contains(out, "IDE settings") {
		t.Errorf("doctor in repo missing IDE settings heading:\n%s", out)
	}
	if !strings.Contains(out, "Current repo: .idea uses the built-in SSH executable") {
		t.Errorf("doctor in repo missing .idea warning:\n%s", out)
	}
	if !strings.Contains(out, "Current repo: .vscode: built-in credential provider is disabled") {
		t.Errorf("doctor in repo missing .vscode finding:\n%s", out)
	}
}
