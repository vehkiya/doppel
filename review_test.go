package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression tests for the review of milestone 1, plus the clone and
// worktree behavior SPEC.md §6.7 asks for.

func TestUninstallKeepsIncludesAddedToDoppelsSection(t *testing.T) {
	s := newSandbox(t)
	s.write(".gitconfig", "[user]\n\tname = Me\n")
	s.addAccount("personal", "jane@personal.dev")
	// `git config --add include.path` appends to the last [include]
	// section, which is doppel's.
	s.write("other.inc", "[core]\n\teditor = vim\n")
	if _, err := runGit(s.home, "config", "--global", "--add", "include.path", s.path("other.inc")); err != nil {
		t.Fatal(err)
	}

	s.mustRun("uninstall", "--yes")
	if got := s.gitConfig(s.home, "core.editor"); got != "vim" {
		t.Errorf("core.editor = %q after uninstall, want the other include to still work:\n%s", got, s.read(".gitconfig"))
	}
	if ok, err := includesIndex(s.env(), s.path(".gitconfig")); err != nil || ok {
		t.Errorf("index still included (err %v)", err)
	}
	if strings.Contains(s.read(".gitconfig"), includeComment) {
		t.Error("doppel's comment was left behind")
	}
}

func TestHandEditedFoldersAreValidated(t *testing.T) {
	for _, folder := range []string{`~/projects/x"y`, "projects/relative", "~/a*"} {
		t.Run(folder, func(t *testing.T) {
			s := newSandbox(t)
			s.addAccount("personal", "jane@personal.dev")
			before := s.read(".config/doppel/index.gitconfig")
			if _, err := runGit(s.home, "config", "--file", s.path(accountsDir+"personal.gitconfig"), "--add", keyFolder, folder); err != nil {
				t.Fatal(err)
			}

			if stderr := s.mustFail(1, "default", "personal"); !strings.Contains(stderr, "doppel.folder") {
				t.Errorf("error doesn't say what to fix: %s", stderr)
			}
			if got := s.read(".config/doppel/index.gitconfig"); got != before {
				t.Errorf("index was rewritten with the bad folder:\n%s", got)
			}
		})
	}
}

func TestDoppelRepairsABrokenIndex(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	// An unreadable index (say, from an older version or a hand edit) makes
	// every git command that reads the user's config fail.
	s.write(".config/doppel/index.gitconfig", "[includeIf \"gitdir:broken\n")
	if _, err := runGit(s.home, "init", "-q", s.path("repo")); err == nil {
		t.Fatal("expected git to fail on the broken index")
	}

	// doppel edits its own files with the user's config switched off, so it
	// still runs, and its next save regenerates the index.
	s.mustRun("ls")
	s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com")
	if got := s.gitConfig(s.gitInit("repo"), "user.email"); got != "jane@personal.dev" {
		t.Errorf("user.email = %q after the repair", got)
	}
}

func TestFailedRenameNeverLeavesAFolderWithoutItsAccount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work")
	repo := s.gitInit("work/repo")

	// The index can't be rewritten while doppel's directory is read-only.
	dir := s.path(".config/doppel")
	if err := os.Chmod(dir, 0500); err != nil { //nolint:gosec // a directory needs its execute bit; read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) //nolint:gosec // restores the directory's normal permissions
	s.mustFail(1, "rename", "work", "job")
	if err := os.Chmod(dir, 0700); err != nil { //nolint:gosec // restores the directory's normal permissions
		t.Fatal(err)
	}

	if got := s.gitConfig(repo, "user.email"); got != "jane@acme.com" {
		t.Errorf("user.email = %q after a failed rename, want the work account to still apply", got)
	}
}

func TestWhoamiForAFutureCloneInsideARepo(t *testing.T) {
	s := newSandbox(t)
	s.gitInit(".") // the home directory is itself a repo, as with yadm
	s.mkdir("work")
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work")

	out := s.mustRun("whoami", "~/work/newclone")
	if !strings.Contains(out, "work (folder ~/work/)") {
		t.Errorf("whoami for a clone target:\n%s", out)
	}
	// The existing folder belongs to the home repo, but a new repo there
	// would get the work account.
	out = s.mustRun("whoami", "~/work")
	if !strings.Contains(out, "personal (default account)") || !strings.Contains(out, "New repos work (folder ~/work/)") {
		t.Errorf("whoami for a plain folder inside the home repo:\n%s", out)
	}
	if got := s.gitConfig(s.gitInit("work/newclone"), "user.email"); got != "jane@acme.com" {
		t.Errorf("the clone got %q", got)
	}
}

func TestIncludeFollowsTheGlobalConfigGitUses(t *testing.T) {
	s := newSandbox(t)
	s.write(".config/git/config", "[core]\n\tpager = less\n")
	s.addAccount("personal", "jane@personal.dev")

	// ~/.gitconfig appears later; Git now reads it after the XDG file.
	s.write(".gitconfig", "[alias]\n\tst = status\n")
	s.addAccount("work", "jane@acme.com")
	if strings.Contains(s.read(".config/git/config"), includeComment) {
		t.Error("the include wasn't moved out of ~/.config/git/config")
	}
	if !strings.HasSuffix(s.read(".gitconfig"), includeBlock(s.env())) {
		t.Errorf("the include isn't at the end of ~/.gitconfig:\n%s", s.read(".gitconfig"))
	}

	// uninstall also cleans up an include left in the other file.
	s.write(".config/git/config", s.read(".config/git/config")+"\n"+includeBlock(s.env()))
	s.mustRun("uninstall", "--yes")
	for _, rel := range []string{".gitconfig", ".config/git/config"} {
		if ok, err := includesIndex(s.env(), s.path(rel)); err != nil || ok {
			t.Errorf("%s still includes the index (err %v)", rel, err)
		}
	}
}

func TestGitConfigGlobalIsUsedAsGitUsesIt(t *testing.T) {
	s := newSandbox(t)
	custom := s.path("custom/gitconfig")
	s.write("custom/gitconfig", "[user]\n\tname = Old\n")
	t.Setenv("GIT_CONFIG_GLOBAL", custom)

	s.addAccount("personal", "jane@personal.dev")
	if !strings.Contains(s.read("custom/gitconfig"), includeComment) {
		t.Error("include not written to $GIT_CONFIG_GLOBAL")
	}
	if s.exists(".gitconfig") {
		t.Error("created ~/.gitconfig although GIT_CONFIG_GLOBAL is set")
	}
	if got := s.gitConfig(s.gitInit("repo"), "user.email"); got != "jane@personal.dev" {
		t.Errorf("user.email = %q", got)
	}

	// Git doesn't expand "~" in the variable, so neither does doppel.
	t.Setenv("GIT_CONFIG_GLOBAL", "~/custom/gitconfig")
	if got := s.env().GlobalConfigPath(); got != "~/custom/gitconfig" {
		t.Errorf("GlobalConfigPath() = %q, want the variable as is", got)
	}
}

func TestSymlinkedHomeDirectory(t *testing.T) {
	s := newSandbox(t)
	s.mkdir("real/work")
	link := s.path("link")
	if err := os.Symlink(s.path("real"), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(link, ".config"))

	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work")
	if !strings.Contains(s.read("real/.config/doppel/index.gitconfig"), `[includeIf "gitdir:~/work/"]`) {
		t.Errorf("folder not stored with ~/:\n%s", s.read("real/.config/doppel/index.gitconfig"))
	}
	repo := s.gitInit("real/work/api")
	if got := s.gitConfig(repo, "user.email"); got != "jane@acme.com" {
		t.Errorf("user.email = %q", got)
	}
	if out := s.mustRun("whoami", repo); !strings.Contains(out, "work (folder ~/work/)") {
		t.Errorf("whoami:\n%s", out)
	}
}

func TestDanglingGlobalConfigSymlinkIsKept(t *testing.T) {
	s := newSandbox(t)
	s.mkdir("dotfiles")
	if err := os.Symlink("dotfiles/gitconfig", s.path(".gitconfig")); err != nil { // relative, as stow makes them
		t.Fatal(err)
	}
	s.addAccount("personal", "jane@personal.dev")
	if info, err := os.Lstat(s.path(".gitconfig")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.gitconfig is no longer a symlink (err %v)", err)
	}
	if !strings.Contains(s.read("dotfiles/gitconfig"), includeComment) {
		t.Error("include not written to the link's target")
	}
}

func TestCloneIntoABoundFolderUsesItsAccount(t *testing.T) {
	s := newSandbox(t)
	// A fake ssh records how Git calls it, then fails the clone.
	bin := s.mkdir("bin")
	s.write("bin/ssh", "#!/bin/sh\necho \"$@\" >> \"$HOME/ssh-calls\"\nexit 1\n")
	if err := os.Chmod(s.path("bin/ssh"), 0700); err != nil { //nolint:gosec // the fake ssh has to be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
	s.setConfig(accountsDir+"work.gitconfig", keyAuthKey, "~/.ssh/id_ed25519_work")
	s.mustRun("default", "personal") // any save regenerates core.sshCommand

	clone := func(dir string) string {
		t.Helper()
		_ = os.Remove(s.path("ssh-calls"))
		_, _ = runGit(s.mkdir(dir), "clone", "-q", "git@example.invalid:acme/api.git")
		return s.read("ssh-calls")
	}
	if calls := clone("projects/work"); !strings.Contains(calls, "-i "+s.path(".ssh/id_ed25519_work")+" -o IdentitiesOnly=yes") {
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
	repo := s.gitInit("projects/work/api")
	if _, err := runGit(repo, "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	worktree := s.path("projects/personal/api-hotfix")
	if _, err := runGit(repo, "worktree", "add", "-q", worktree); err != nil {
		t.Fatal(err)
	}
	if got := s.gitConfig(worktree, "user.email"); got != "jane@acme.com" {
		t.Errorf("worktree user.email = %q, want the main repo's account", got)
	}
	if out := s.mustRun("whoami", worktree); !strings.Contains(out, "work (folder ~/projects/work/)") {
		t.Errorf("whoami in the worktree:\n%s", out)
	}
}
