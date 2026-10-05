package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
)

func TestFoldersPickTheAccount(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")

	cases := map[string]string{
		"projects/work/api":      "jane@acme.com",
		"projects/work/team/svc": "jane@acme.com",
		"projects/workshop":      "jane@personal.dev", // not inside ~/projects/work/
		"projects/blog":          "jane@personal.dev",
		"elsewhere":              "jane@personal.dev",
	}
	for rel, want := range cases {
		if got := s.GitConfig(s.GitInit(rel), "user.email"); got != want {
			t.Errorf("%s: user.email = %q, want %q", rel, got, want)
		}
	}
}

func TestMostSpecificFolderWins(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("main", "jane@example.com")
	// Bind the nested folder first, so the order in the index has to come
	// from sorting rather than from the order of the commands.
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
	s.addAccount("personal", "jane@personal.dev", "--folder", "~/projects")

	cases := map[string]string{
		"projects/work/api": "work",
		"projects/blog":     "personal",
		"elsewhere":         "main",
	}
	for rel, want := range cases {
		if got := s.GitConfig(s.GitInit(rel), accounts.KeyAccount); got != want {
			t.Errorf("%s: account = %q, want %q", rel, got, want)
		}
	}
}

func TestFolderAccountDoesNotInheritDefaultSettings(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.Key("personal", "jane@personal.dev", "")
	// Give the default account a signing key and an SSH key by hand;
	// doppel reads the signing settings back from the file.
	s.SetConfig(accountsDir+"personal.gitconfig", accounts.KeySigningKey, "~/.ssh/personal.pub")
	s.SetConfig(accountsDir+"personal.gitconfig", accounts.KeyCommitSign, "true")
	s.SetConfig(accountsDir+"personal.gitconfig", accounts.KeyAuthKey, "~/.ssh/personal")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")

	personal := s.GitInit("projects/blog")
	work := s.GitInit("projects/work/api")
	for key, want := range map[string]string{
		accounts.KeyCommitSign: "true", accounts.KeySigningKey: "~/.ssh/personal.pub", accounts.KeySSHCommand: "ssh -i ~/.ssh/personal -o IdentitiesOnly=yes",
	} {
		if got := s.GitConfig(personal, key); got != want {
			t.Errorf("personal repo: %s = %q, want %q", key, got, want)
		}
	}
	for key, want := range map[string]string{accounts.KeyCommitSign: "false", accounts.KeySigningKey: "", accounts.KeySSHCommand: "ssh"} {
		if got := s.GitConfig(work, key); got != want {
			t.Errorf("work repo: %s = %q, want %q (leaked from the default account)", key, got, want)
		}
	}
}

func TestFolderIsStoredByItsRealPath(t *testing.T) {
	s := newSandbox(t)
	s.Mkdir("data/clients")
	if err := os.Symlink(s.Path("data/clients"), s.Path("clients")); err != nil {
		t.Fatal(err)
	}
	s.addAccount("work", "jane@acme.com", "--folder", "~/clients")
	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	if got := list[0].Folders; !slices.Equal(got, []string{"~/data/clients/"}) {
		t.Errorf("folders = %v, want the resolved path", got)
	}
	repo := s.GitInit("clients/acme") // created through the symlink
	if got := s.GitConfig(repo, accounts.KeyAccount); got != "work" {
		t.Errorf("repo opened through the symlink: account = %q", got)
	}
}

func TestSymlinkedHomeDirectory(t *testing.T) {
	s := newSandbox(t)
	s.Mkdir("real/work")
	link := s.Path("link")
	if err := os.Symlink(s.Path("real"), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(link, ".config"))

	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work")
	condition := "gitdir:"
	if s.Env().CaseInsensitive() {
		condition = "gitdir/i:" // macOS
	}
	if !strings.Contains(s.Read("real/.config/doppel/index.gitconfig"), `[includeIf "`+condition+`~/work/"]`) {
		t.Errorf("folder not stored with ~/:\n%s", s.Read("real/.config/doppel/index.gitconfig"))
	}
	repo := s.GitInit("real/work/api")
	if got := s.GitConfig(repo, "user.email"); got != "jane@acme.com" {
		t.Errorf("user.email = %q", got)
	}
	if out := s.mustRun("whoami", repo); !strings.Contains(out, "work (folder ~/work/)") {
		t.Errorf("whoami:\n%s", out)
	}
}

func TestHandEditedFoldersAreValidated(t *testing.T) {
	for _, folder := range []string{`~/projects/x"y`, "projects/relative", "~/a*"} {
		t.Run(folder, func(t *testing.T) {
			s := newSandbox(t)
			s.addAccount("personal", "jane@personal.dev")
			if _, err := git.Run(s.Home, "config", "--file", s.Path(accountsDir+"personal.gitconfig"), "--add", accounts.KeyFolder, folder); err != nil {
				t.Fatal(err)
			}
			index, global := s.Read(".config/doppel/index.gitconfig"), s.Read(".gitconfig")

			if stderr := s.mustFail(1, "default", "personal"); !strings.Contains(stderr, "doppel.folder") {
				t.Errorf("error doesn't say what to fix: %s", stderr)
			}
			// doctor --fix goes through the same checks, and says which file and value.
			s.mustFail(1, "doctor", "--fix")
			out := s.stdout.String()
			for _, want := range []string{"Can't bring doppel's files up to date", "doppel.folder", folder, "personal.gitconfig"} {
				if !strings.Contains(out, want) {
					t.Errorf("doctor --fix output is missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Updated") {
				t.Errorf("doctor --fix wrote a file:\n%s", out)
			}
			if got := s.Read(".config/doppel/index.gitconfig"); got != index {
				t.Errorf("index was rewritten with the bad folder:\n%s", got)
			}
			if got := s.Read(".gitconfig"); got != global {
				t.Errorf("global config was rewritten:\n%s", got)
			}
			// Git can still read everything.
			if _, err := git.Run(s.Home, "config", "--global", "--includes", "--list"); err != nil {
				t.Errorf("Git can't read the config: %v", err)
			}
		})
	}
}

func TestCloneIntoABoundFolderUsesItsAccount(t *testing.T) {
	s := newSandbox(t)
	// A fake ssh records how Git calls it, then fails the clone.
	bin := s.Mkdir("bin")
	s.Write("bin/ssh", "#!/bin/sh\necho \"$@\" >> \"$HOME/ssh-calls\"\nexit 1\n")
	if err := os.Chmod(s.Path("bin/ssh"), 0700); err != nil { //nolint:gosec // the fake ssh has to be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
	s.SetConfig(accountsDir+"work.gitconfig", accounts.KeyAuthKey, "~/.ssh/id_ed25519_work")
	s.mustRun("default", "personal") // any save regenerates core.sshCommand

	clone := func(dir string) string {
		t.Helper()
		_ = os.Remove(s.Path("ssh-calls"))
		_, _ = git.Run(s.Mkdir(dir), "clone", "-q", "git@example.invalid:acme/api.git")
		return s.Read("ssh-calls")
	}
	if calls := clone("projects/work"); !strings.Contains(calls, "-i "+s.Path(".ssh/id_ed25519_work")+" -o IdentitiesOnly=yes") {
		t.Errorf("clone into the work folder didn't use the work key: %q", calls)
	}
	if calls := clone("projects/personal"); strings.Contains(calls, "-i ") {
		t.Errorf("clone outside the work folder used a key it shouldn't: %q", calls)
	}
}

func TestWorktreesFollowTheirMainRepo(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev", "--folder", "~/projects/personal")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
	repo := s.GitInit("projects/work/api")
	if _, err := git.Run(repo, "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	worktree := s.Path("projects/personal/api-hotfix")
	if _, err := git.Run(repo, "worktree", "add", "-q", worktree); err != nil {
		t.Fatal(err)
	}
	if got := s.GitConfig(worktree, "user.email"); got != "jane@acme.com" {
		t.Errorf("worktree user.email = %q, want the main repo's account", got)
	}
	if out := s.mustRun("whoami", worktree); !strings.Contains(out, "work (folder ~/projects/work/)") {
		t.Errorf("whoami in the worktree:\n%s", out)
	}
}
