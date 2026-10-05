package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const accountsDir = ".config/doppel/accounts/"

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
		if got := s.gitConfig(s.gitInit(rel), "user.email"); got != want {
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
		if got := s.gitConfig(s.gitInit(rel), keyAccount); got != want {
			t.Errorf("%s: account = %q, want %q", rel, got, want)
		}
	}
}

func TestFolderAccountDoesNotInheritDefaultSettings(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	// Give the default account a signing key and an SSH key by hand;
	// doppel reads the signing settings back from the file.
	s.setConfig(accountsDir+"personal.gitconfig", keySigningKey, "~/.ssh/personal.pub")
	s.setConfig(accountsDir+"personal.gitconfig", keyCommitSign, "true")
	s.setConfig(accountsDir+"personal.gitconfig", keyAuthKey, "~/.ssh/personal")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")

	personal := s.gitInit("projects/blog")
	work := s.gitInit("projects/work/api")
	for key, want := range map[string]string{
		keyCommitSign: "true", keySigningKey: "~/.ssh/personal.pub", keySSHCommand: "ssh -i ~/.ssh/personal -o IdentitiesOnly=yes",
	} {
		if got := s.gitConfig(personal, key); got != want {
			t.Errorf("personal repo: %s = %q, want %q", key, got, want)
		}
	}
	for key, want := range map[string]string{keyCommitSign: "false", keySigningKey: "", keySSHCommand: "ssh"} {
		if got := s.gitConfig(work, key); got != want {
			t.Errorf("work repo: %s = %q, want %q (leaked from the default account)", key, got, want)
		}
	}
}

func TestIncludeIsAppendedOnceAtTheEnd(t *testing.T) {
	s := newSandbox(t)
	original := "[user]\n\tname = Old Global\n[include]\n\tpath = ~/.gitconfig.local\n"
	s.write(".gitconfig", original)

	s.addAccount("personal", "jane@personal.dev")
	got := s.read(".gitconfig")
	if !strings.HasPrefix(got, original) {
		t.Errorf("existing content changed:\n%s", got)
	}
	if !strings.HasSuffix(got, includeBlock(s.env())) {
		t.Errorf("include block isn't at the end:\n%s", got)
	}

	// A save that changes nothing leaves every file alone.
	out := s.mustRun("default", "personal")
	if after := s.read(".gitconfig"); after != got {
		t.Errorf("second save changed ~/.gitconfig:\n%s", after)
	}
	if strings.Contains(out, "updated") || strings.Contains(out, "created") {
		t.Errorf("unchanged save reported file changes:\n%s", out)
	}
}

func TestIncludeGoesToXDGConfigWhenThatIsTheOneInUse(t *testing.T) {
	s := newSandbox(t)
	s.write(".config/git/config", "[user]\n\tname = Old Global\n")
	s.addAccount("personal", "jane@personal.dev")
	if s.exists(".gitconfig") {
		t.Error("created ~/.gitconfig although Git uses ~/.config/git/config")
	}
	if !strings.Contains(s.read(".config/git/config"), includeComment) {
		t.Error("include block missing from ~/.config/git/config")
	}
}

func TestSymlinkedGlobalConfigStaysASymlink(t *testing.T) {
	s := newSandbox(t)
	s.write("dotfiles/gitconfig", "[user]\n\tname = Old Global\n")
	if err := os.Symlink(s.path("dotfiles/gitconfig"), s.path(".gitconfig")); err != nil {
		t.Fatal(err)
	}
	s.addAccount("personal", "jane@personal.dev")
	info, err := os.Lstat(s.path(".gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("~/.gitconfig was replaced by a regular file")
	}
	if !strings.Contains(s.read("dotfiles/gitconfig"), includeComment) {
		t.Error("include block wasn't written through the symlink")
	}
}

func TestOtherSettingsSurviveEditAndRename(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("work", "jane@acme.com")
	s.setConfig(accountsDir+"work.gitconfig", "pull.rebase", "true")

	s.mustRun("edit", "work", "--email", "jane@acme.io")
	s.mustRun("rename", "work", "acme")

	if s.exists(accountsDir + "work.gitconfig") {
		t.Error("old account file still exists after rename")
	}
	if !s.exists(accountsDir + ".work.gitconfig.doppel.bak") {
		t.Error("no backup of the renamed account file")
	}
	acme := s.path(accountsDir + "acme.gitconfig")
	values, err := readConfigFile(acme)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["pull.rebase"]; !slices.Equal(got, []string{"true"}) {
		t.Errorf("pull.rebase = %v, want [true]", got)
	}
	if got := values["user.email"]; !slices.Equal(got, []string{"jane@acme.io"}) {
		t.Errorf("user.email = %v, want [jane@acme.io]", got)
	}
	if got := s.gitConfig(s.gitInit("repo"), keyAccount); got != "acme" {
		t.Errorf("default account after rename = %q, want acme", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	s := newSandbox(t)
	out := s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--dry-run")
	if s.exists(".config/doppel") || s.exists(".gitconfig") {
		t.Fatal("--dry-run wrote files")
	}
	for _, want := range []string{"new file ~/.config/doppel/accounts/work.gitconfig", "+\temail = jane@acme.com", "+" + includeComment} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output is missing %q:\n%s", want, out)
		}
	}
}

func TestUninstall(t *testing.T) {
	t.Run("restores the global config", func(t *testing.T) {
		s := newSandbox(t)
		original := "[user]\n\temail = old@example.com\n"
		s.write(".gitconfig", original)
		s.addAccount("personal", "jane@personal.dev")

		s.mustRun("uninstall", "--yes")
		if got := s.read(".gitconfig"); got != original {
			t.Errorf("~/.gitconfig after uninstall:\n%q\nwant:\n%q", got, original)
		}
		if !s.exists(accountsDir + "personal.gitconfig") {
			t.Error("uninstall deleted the account file")
		}
		if got := s.gitConfig(s.gitInit("repo"), "user.email"); got != "old@example.com" {
			t.Errorf("user.email after uninstall = %q, want the global one", got)
		}
	})

	t.Run("finds an include that was moved", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("personal", "jane@personal.dev")
		s.write(".gitconfig", "[include]\n\tpath = ~/.config/doppel/index.gitconfig\n[user]\n\temail = old@example.com\n")

		s.mustRun("uninstall", "--yes")
		if ok, err := includesIndex(s.env(), s.path(".gitconfig")); err != nil || ok {
			t.Errorf("index still included (err %v):\n%s", err, s.read(".gitconfig"))
		}
		if !strings.Contains(s.read(".gitconfig"), "old@example.com") {
			t.Error("uninstall removed unrelated settings")
		}
	})

	t.Run("reports when there is nothing to remove", func(t *testing.T) {
		s := newSandbox(t)
		if out := s.mustRun("uninstall", "--yes"); !strings.Contains(out, "nothing to remove") {
			t.Errorf("output: %s", out)
		}
	})
}

func TestConfirmations(t *testing.T) {
	t.Run("rm needs --yes without a terminal", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("work", "jane@acme.com")
		if stderr := s.mustFail(1, "rm", "work"); !strings.Contains(stderr, "--yes") {
			t.Errorf("stderr doesn't mention --yes: %s", stderr)
		}
		if !s.exists(accountsDir + "work.gitconfig") {
			t.Fatal("account deleted without confirmation")
		}
		s.mustRun("rm", "work", "--yes")
		if s.exists(accountsDir + "work.gitconfig") {
			t.Error("account file still exists")
		}
	})

	t.Run("rm asks on a terminal", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("work", "jane@acme.com")
		s.tty, s.stdin = true, "n\n"
		s.mustFail(1, "rm", "work")
		s.stdin = "y\n"
		s.mustRun("rm", "work")
		if s.exists(accountsDir + "work.gitconfig") {
			t.Error("account file still exists after answering yes")
		}
	})

	t.Run("moving a folder between accounts", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("personal", "jane@personal.dev", "--folder", "~/projects")
		s.addAccount("work", "jane@acme.com")
		s.mustFail(1, "bind", "work", "~/projects")
		s.mustRun("bind", "work", "~/projects", "--yes")
		accounts, err := loadAccounts(s.env())
		if err != nil {
			t.Fatal(err)
		}
		if p := findAccount(accounts, "personal"); len(p.Folders) != 0 {
			t.Errorf("personal still has %v", p.Folders)
		}
		if w := findAccount(accounts, "work"); !slices.Equal(w.Folders, []string{"~/projects/"}) {
			t.Errorf("work folders = %v", w.Folders)
		}
	})
}

func TestDefaultAccount(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com")
	if got := strings.TrimSpace(s.mustRun("default")); got != "personal" {
		t.Errorf("first account isn't the default: %q", got)
	}

	s.mustRun("default", "work")
	if got := s.gitConfig(s.gitInit("repo"), keyAccount); got != "work" {
		t.Errorf("account = %q after `default work`", got)
	}

	s.mustRun("rm", "work", "--yes")
	if strings.Contains(s.read(".config/doppel/index.gitconfig"), "[include]") {
		t.Error("index still includes a default account after removing it")
	}
	if got := s.gitConfig(s.path("repo"), keyAccount); got != "" {
		t.Errorf("account = %q with no default", got)
	}
}

func TestFolderIsStoredByItsRealPath(t *testing.T) {
	s := newSandbox(t)
	s.mkdir("data/clients")
	if err := os.Symlink(s.path("data/clients"), s.path("clients")); err != nil {
		t.Fatal(err)
	}
	s.addAccount("work", "jane@acme.com", "--folder", "~/clients")
	accounts, err := loadAccounts(s.env())
	if err != nil {
		t.Fatal(err)
	}
	if got := accounts[0].Folders; !slices.Equal(got, []string{"~/data/clients/"}) {
		t.Errorf("folders = %v, want the resolved path", got)
	}
	repo := s.gitInit("clients/acme") // created through the symlink
	if got := s.gitConfig(repo, keyAccount); got != "work" {
		t.Errorf("repo opened through the symlink: account = %q", got)
	}
}

func TestWhoami(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
	api := s.gitInit("projects/work/api")

	out := s.mustRun("whoami", "projects/work/api")
	for _, want := range []string{"work (folder ~/projects/work/)", "jane@acme.com", "your default SSH keys", "off"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami output is missing %q:\n%s", want, out)
		}
	}
	if s.stderr.Len() > 0 {
		t.Errorf("unexpected warnings:\n%s", s.stderr.String())
	}

	if _, err := runGit(api, "config", "user.email", "override@example.com"); err != nil {
		t.Fatal(err)
	}
	s.runIn(api, "whoami")
	if !strings.Contains(s.stderr.String(), "user.email = override@example.com comes from .git/config (local config)") {
		t.Errorf("no warning about the repo's own email:\n%s", s.stderr.String())
	}

	// A global setting added below doppel's include overrides every account.
	s.write(".gitconfig", s.read(".gitconfig")+"[user]\n\tname = Late Global\n")
	s.run("whoami", "projects/work/api")
	if !strings.Contains(s.stderr.String(), "user.name = Late Global comes from "+s.path(".gitconfig")) {
		t.Errorf("no warning about the global setting after the include:\n%s", s.stderr.String())
	}

	out = s.mustRun("whoami", "~/projects/work/not-cloned-yet")
	if !strings.Contains(out, "doesn't exist yet") || !strings.Contains(out, "work (folder ~/projects/work/)") {
		t.Errorf("whoami for a future clone:\n%s", out)
	}
	out = s.mustRun("whoami", s.mkdir("notes"))
	if !strings.Contains(out, "not a Git repository") || !strings.Contains(out, "personal (default account)") {
		t.Errorf("whoami outside a repo:\n%s", out)
	}
}

func TestWhoamiWithoutAccounts(t *testing.T) {
	s := newSandbox(t)
	s.gitInit("repo")
	out := s.mustRun("whoami", "repo")
	if !strings.Contains(out, "none") || !strings.Contains(out, "doppel bind") {
		t.Errorf("whoami with no accounts:\n%s", out)
	}
}

func TestLs(t *testing.T) {
	s := newSandbox(t)
	if out := s.mustRun("ls"); !strings.Contains(out, "No accounts yet") {
		t.Errorf("ls with no accounts:\n%s", out)
	}
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work", "--folder", "~/clients")
	out := s.mustRun()
	for _, want := range []string{"★  personal", "jane@acme.com", "~/projects/work/, ~/clients/"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output is missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("trailing spaces in %q", line)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("work", "jane@acme.com")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"add", "Work", "--name", "J", "--email", "j@x.io"}, "lowercase"},
		{[]string{"add", "other", "--name", "J"}, "needs an email"},
		{[]string{"add", "other", "--email", "j@x.io"}, "needs a name"},
		{[]string{"add", "other", "--name", "J", "--email", "not-an-email"}, "valid email"},
		{[]string{"add", "work", "--name", "J", "--email", "j@x.io"}, "already exists"},
		{[]string{"add", "other", "--name", "J", "--email", "j@x.io", "--folder", "~/a*b"}, "pattern"},
		{[]string{"edit", "nobody", "--name", "J"}, "no account named nobody"},
		{[]string{"unbind", "~/projects"}, "isn't bound"},
		{[]string{"rename", "work", "Bad"}, "lowercase"},
	}
	for _, c := range cases {
		if stderr := s.mustFail(1, c.args...); !strings.Contains(stderr, c.want) {
			t.Errorf("doppel %s: stderr %q doesn't mention %q", strings.Join(c.args, " "), stderr, c.want)
		}
	}
	s.mustFail(2, "edit", "work") // nothing to change
	s.mustFail(2, "frobnicate")
	if files, _ := filepath.Glob(s.path(accountsDir + "*.gitconfig")); len(files) != 1 {
		t.Errorf("failed commands left account files behind: %v", files)
	}
}

func TestFlagsMayFollowArguments(t *testing.T) {
	s := newSandbox(t)
	s.mustRun("add", "--name", "Jane Doe", "work", "--email", "jane@acme.com", "--folder", "~/a", "--folder", "~/b")
	accounts, err := loadAccounts(s.env())
	if err != nil {
		t.Fatal(err)
	}
	if got := accounts[0].Folders; !slices.Equal(got, []string{"~/a/", "~/b/"}) {
		t.Errorf("folders = %v", got)
	}
}
