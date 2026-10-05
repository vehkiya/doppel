package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/store"
)

func TestOtherSettingsSurviveEditAndRename(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("work", "jane@acme.com")
	s.SetConfig(accountsDir+"work.gitconfig", "pull.rebase", "true")

	s.mustRun("edit", "work", "--email", "jane@acme.io")
	s.mustRun("rename", "work", "acme")

	if s.Exists(accountsDir + "work.gitconfig") {
		t.Error("old account file still exists after rename")
	}
	if !s.Exists(accountsDir + ".work.gitconfig.doppel.bak") {
		t.Error("no backup of the renamed account file")
	}
	acme := s.Path(accountsDir + "acme.gitconfig")
	values, err := git.ReadConfigFile(acme)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["pull.rebase"]; !slices.Equal(got, []string{"true"}) {
		t.Errorf("pull.rebase = %v, want [true]", got)
	}
	if got := values["user.email"]; !slices.Equal(got, []string{"jane@acme.io"}) {
		t.Errorf("user.email = %v, want [jane@acme.io]", got)
	}
	if got := s.GitConfig(s.GitInit("repo"), accounts.KeyAccount); got != "acme" {
		t.Errorf("default account after rename = %q, want acme", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	s := newSandbox(t)
	out := s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--dry-run")
	if s.Exists(".config/doppel") || s.Exists(".gitconfig") {
		t.Fatal("--dry-run wrote files")
	}
	for _, want := range []string{"new file ~/.config/doppel/accounts/work.gitconfig", "+\temail = jane@acme.com", "+" + store.IncludeComment} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output is missing %q:\n%s", want, out)
		}
	}
}

func TestConfirmations(t *testing.T) {
	t.Run("rm needs --yes without a terminal", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("work", "jane@acme.com")
		if stderr := s.mustFail(1, "rm", "work"); !strings.Contains(stderr, "--yes") {
			t.Errorf("stderr doesn't mention --yes: %s", stderr)
		}
		if !s.Exists(accountsDir + "work.gitconfig") {
			t.Fatal("account deleted without confirmation")
		}
		s.mustRun("rm", "work", "--yes")
		if s.Exists(accountsDir + "work.gitconfig") {
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
		if s.Exists(accountsDir + "work.gitconfig") {
			t.Error("account file still exists after answering yes")
		}
	})

	t.Run("moving a folder between accounts", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("personal", "jane@personal.dev", "--folder", "~/projects")
		s.addAccount("work", "jane@acme.com")
		s.mustFail(1, "bind", "work", "~/projects")
		s.mustRun("bind", "work", "~/projects", "--yes")
		list, err := accounts.Load(s.Env())
		if err != nil {
			t.Fatal(err)
		}
		if p := accounts.Find(list, "personal"); len(p.Folders) != 0 {
			t.Errorf("personal still has %v", p.Folders)
		}
		if w := accounts.Find(list, "work"); !slices.Equal(w.Folders, []string{"~/projects/"}) {
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
	if got := s.GitConfig(s.GitInit("repo"), accounts.KeyAccount); got != "work" {
		t.Errorf("account = %q after `default work`", got)
	}

	s.mustRun("rm", "work", "--yes")
	if strings.Contains(s.Read(".config/doppel/index.gitconfig"), "[include]") {
		t.Error("index still includes a default account after removing it")
	}
	if got := s.GitConfig(s.Path("repo"), accounts.KeyAccount); got != "" {
		t.Errorf("account = %q with no default", got)
	}
}

func TestFailedRenameNeverLeavesAFolderWithoutItsAccount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work")
	repo := s.GitInit("work/repo")

	// The index can't be rewritten while doppel's directory is read-only.
	dir := s.Path(".config/doppel")
	if err := os.Chmod(dir, 0500); err != nil { //nolint:gosec // a directory needs its execute bit; read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) //nolint:gosec // restores the directory's normal permissions
	s.mustFail(1, "rename", "work", "job")
	if err := os.Chmod(dir, 0700); err != nil { //nolint:gosec // restores the directory's normal permissions
		t.Fatal(err)
	}

	if got := s.GitConfig(repo, "user.email"); got != "jane@acme.com" {
		t.Errorf("user.email = %q after a failed rename, want the work account to still apply", got)
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
	if files, _ := filepath.Glob(s.Path(accountsDir + "*.gitconfig")); len(files) != 1 {
		t.Errorf("failed commands left account files behind: %v", files)
	}
}

func TestFlagsMayFollowArguments(t *testing.T) {
	s := newSandbox(t)
	s.mustRun("add", "--name", "Jane Doe", "work", "--email", "jane@acme.com", "--folder", "~/a", "--folder", "~/b")
	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	if got := list[0].Folders; !slices.Equal(got, []string{"~/a/", "~/b/"}) {
		t.Errorf("folders = %v", got)
	}
}
