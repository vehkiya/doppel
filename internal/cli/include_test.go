package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/store"
)

func TestIncludeIsAppendedOnceAtTheEnd(t *testing.T) {
	s := newSandbox(t)
	original := "[user]\n\tname = Old Global\n[include]\n\tpath = ~/.gitconfig.local\n"
	s.Write(".gitconfig", original)

	s.addAccount("personal", "jane@personal.dev")
	got := s.Read(".gitconfig")
	if !strings.HasPrefix(got, original) {
		t.Errorf("existing content changed:\n%s", got)
	}
	if !strings.HasSuffix(got, store.IncludeBlock(s.Env())) {
		t.Errorf("include block isn't at the end:\n%s", got)
	}

	// A save that changes nothing leaves every file alone.
	out := s.mustRun("default", "personal")
	if after := s.Read(".gitconfig"); after != got {
		t.Errorf("second save changed ~/.gitconfig:\n%s", after)
	}
	if strings.Contains(out, "updated") || strings.Contains(out, "created") {
		t.Errorf("unchanged save reported file changes:\n%s", out)
	}
}

func TestIncludeGoesToXDGConfigWhenThatIsTheOneInUse(t *testing.T) {
	s := newSandbox(t)
	s.Write(".config/git/config", "[user]\n\tname = Old Global\n")
	s.addAccount("personal", "jane@personal.dev")
	if s.Exists(".gitconfig") {
		t.Error("created ~/.gitconfig although Git uses ~/.config/git/config")
	}
	if !strings.Contains(s.Read(".config/git/config"), store.IncludeComment) {
		t.Error("include block missing from ~/.config/git/config")
	}
}

func TestIncludeFollowsTheGlobalConfigGitUses(t *testing.T) {
	s := newSandbox(t)
	s.Write(".config/git/config", "[core]\n\tpager = less\n")
	s.addAccount("personal", "jane@personal.dev")

	// ~/.gitconfig appears later; Git now reads it after the XDG file.
	s.Write(".gitconfig", "[alias]\n\tst = status\n")
	s.addAccount("work", "jane@acme.com")
	if strings.Contains(s.Read(".config/git/config"), store.IncludeComment) {
		t.Error("the include wasn't moved out of ~/.config/git/config")
	}
	if !strings.HasSuffix(s.Read(".gitconfig"), store.IncludeBlock(s.Env())) {
		t.Errorf("the include isn't at the end of ~/.gitconfig:\n%s", s.Read(".gitconfig"))
	}

	// uninstall also cleans up an include left in the other file.
	s.Write(".config/git/config", s.Read(".config/git/config")+"\n"+store.IncludeBlock(s.Env()))
	s.mustRun("uninstall", "--yes")
	for _, rel := range []string{".gitconfig", ".config/git/config"} {
		if ok, err := store.IncludesIndex(s.Env(), s.Path(rel)); err != nil || ok {
			t.Errorf("%s still includes the index (err %v)", rel, err)
		}
	}
}

func TestGitConfigGlobalIsUsedAsGitUsesIt(t *testing.T) {
	s := newSandbox(t)
	custom := s.Path("custom/gitconfig")
	s.Write("custom/gitconfig", "[user]\n\tname = Old\n")
	t.Setenv("GIT_CONFIG_GLOBAL", custom)

	s.addAccount("personal", "jane@personal.dev")
	if !strings.Contains(s.Read("custom/gitconfig"), store.IncludeComment) {
		t.Error("include not written to $GIT_CONFIG_GLOBAL")
	}
	if s.Exists(".gitconfig") {
		t.Error("created ~/.gitconfig although GIT_CONFIG_GLOBAL is set")
	}
	if got := s.GitConfig(s.GitInit("repo"), "user.email"); got != "jane@personal.dev" {
		t.Errorf("user.email = %q", got)
	}

	// Git doesn't expand "~" in the variable, so neither does doppel.
	t.Setenv("GIT_CONFIG_GLOBAL", "~/custom/gitconfig")
	if got := s.Env().GlobalConfigPath(); got != "~/custom/gitconfig" {
		t.Errorf("GlobalConfigPath() = %q, want the variable as is", got)
	}
}

func TestSymlinkedGlobalConfigStaysASymlink(t *testing.T) {
	s := newSandbox(t)
	s.Write("dotfiles/gitconfig", "[user]\n\tname = Old Global\n")
	if err := os.Symlink(s.Path("dotfiles/gitconfig"), s.Path(".gitconfig")); err != nil {
		t.Fatal(err)
	}
	s.addAccount("personal", "jane@personal.dev")
	info, err := os.Lstat(s.Path(".gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("~/.gitconfig was replaced by a regular file")
	}
	if !strings.Contains(s.Read("dotfiles/gitconfig"), store.IncludeComment) {
		t.Error("include block wasn't written through the symlink")
	}
}

func TestDanglingGlobalConfigSymlinkIsKept(t *testing.T) {
	s := newSandbox(t)
	s.Mkdir("dotfiles")
	if err := os.Symlink("dotfiles/gitconfig", s.Path(".gitconfig")); err != nil { // relative, as stow makes them
		t.Fatal(err)
	}
	s.addAccount("personal", "jane@personal.dev")
	if info, err := os.Lstat(s.Path(".gitconfig")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.gitconfig is no longer a symlink (err %v)", err)
	}
	if !strings.Contains(s.Read("dotfiles/gitconfig"), store.IncludeComment) {
		t.Error("include not written to the link's target")
	}
}

func TestUninstall(t *testing.T) {
	t.Run("restores the global config", func(t *testing.T) {
		s := newSandbox(t)
		original := "[user]\n\temail = old@example.com\n"
		s.Write(".gitconfig", original)
		s.addAccount("personal", "jane@personal.dev")

		s.mustRun("uninstall", "--yes")
		if got := s.Read(".gitconfig"); got != original {
			t.Errorf("~/.gitconfig after uninstall:\n%q\nwant:\n%q", got, original)
		}
		if !s.Exists(accountsDir + "personal.gitconfig") {
			t.Error("uninstall deleted the account file")
		}
		if got := s.GitConfig(s.GitInit("repo"), "user.email"); got != "old@example.com" {
			t.Errorf("user.email after uninstall = %q, want the global one", got)
		}
	})

	t.Run("finds an include that was moved", func(t *testing.T) {
		s := newSandbox(t)
		s.addAccount("personal", "jane@personal.dev")
		s.Write(".gitconfig", "[include]\n\tpath = ~/.config/doppel/index.gitconfig\n[user]\n\temail = old@example.com\n")

		s.mustRun("uninstall", "--yes")
		if ok, err := store.IncludesIndex(s.Env(), s.Path(".gitconfig")); err != nil || ok {
			t.Errorf("index still included (err %v):\n%s", err, s.Read(".gitconfig"))
		}
		if !strings.Contains(s.Read(".gitconfig"), "old@example.com") {
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

func TestUninstallKeepsIncludesAddedToDoppelsSection(t *testing.T) {
	s := newSandbox(t)
	s.Write(".gitconfig", "[user]\n\tname = Me\n")
	s.addAccount("personal", "jane@personal.dev")
	// `git config --add include.path` appends to the last [include]
	// section, which is doppel's.
	s.Write("other.inc", "[core]\n\teditor = vim\n")
	if _, err := git.Run(s.Home, "config", "--global", "--add", "include.path", s.Path("other.inc")); err != nil {
		t.Fatal(err)
	}

	s.mustRun("uninstall", "--yes")
	if got := s.GitConfig(s.Home, "core.editor"); got != "vim" {
		t.Errorf("core.editor = %q after uninstall, want the other include to still work:\n%s", got, s.Read(".gitconfig"))
	}
	if ok, err := store.IncludesIndex(s.Env(), s.Path(".gitconfig")); err != nil || ok {
		t.Errorf("index still included (err %v)", err)
	}
	if strings.Contains(s.Read(".gitconfig"), store.IncludeComment) {
		t.Error("doppel's comment was left behind")
	}
}

func TestDoppelRepairsABrokenIndex(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	// An unreadable index (say, from an older version or a hand edit) makes
	// every git command that reads the user's config fail.
	s.Write(".config/doppel/index.gitconfig", "[includeIf \"gitdir:broken\n")
	if _, err := git.Run(s.Home, "init", "-q", s.Path("repo")); err == nil {
		t.Fatal("expected git to fail on the broken index")
	}

	// doppel edits its own files with the user's config switched off, so it
	// still runs, and its next save regenerates the index.
	s.mustRun("ls")
	s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com")
	if got := s.GitConfig(s.GitInit("repo"), "user.email"); got != "jane@personal.dev" {
		t.Errorf("user.email = %q after the repair", got)
	}
}
