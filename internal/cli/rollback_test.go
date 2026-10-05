package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tree lists every file under the sandbox's home, backups and hidden files
// included, with its content, and every symlink with its target.
func (s *sandbox) tree() map[string]string {
	s.T.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(s.Home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.Home, path)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			got[rel] = "-> " + target
		case !d.IsDir():
			data, err := os.ReadFile(path) //nolint:gosec // a file in the sandbox
			if err != nil {
				return err
			}
			got[rel] = string(data)
		}
		return nil
	})
	if err != nil {
		s.T.Fatal(err)
	}
	return got
}

// A global config that's a symlink into a folder doppel can't write to, as
// home-manager and Nix setups have, fails the last write of a rename. By
// then the new account file and the index were already written.
func TestFailedRenameLeavesEveryFileAsItWas(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/w")
	repo := s.GitInit("w/repo")
	s.mustRun("uninstall", "--yes") // the rename has to add the include again

	readOnly := s.Path("nix")
	s.Write("nix/gitconfig", s.Read(".gitconfig"))
	if err := os.Remove(s.Path(".gitconfig")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(s.Path("nix/gitconfig"), s.Path(".gitconfig")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnly, 0500); err != nil { //nolint:gosec // a directory needs its execute bit; read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0700) }) //nolint:gosec // restores the directory's normal permissions
	before := s.tree()

	stderr := s.mustFail(1, "rename", "work", "job")
	if !strings.Contains(stderr, "put back every file it had already written") {
		t.Errorf("the error doesn't say what doppel did about it:\n%s", stderr)
	}
	if after := s.tree(); !reflect.DeepEqual(after, before) {
		for name := range after {
			if _, ok := before[name]; !ok {
				t.Errorf("left behind: %s", name)
			}
		}
		for name, content := range before {
			if got, ok := after[name]; !ok || got != content {
				t.Errorf("changed or removed: %s", name)
			}
		}
	}
	// Writes that were refused don't wedge doppel: other commands still work...
	if out := s.mustRun("ls"); !strings.Contains(out, "work") || strings.Contains(out, "job") {
		t.Errorf("ls after the failed rename:\n%s", out)
	}

	// ...and once the global config can be written, the same rename works.
	if err := os.Chmod(readOnly, 0700); err != nil { //nolint:gosec // restores the directory's normal permissions
		t.Fatal(err)
	}
	s.mustRun("rename", "work", "job")
	if got := s.GitConfig(repo, "user.email"); got != "jane@acme.com" {
		t.Errorf("user.email = %q after the rename, want the account's email", got)
	}
	if s.Exists(accountsDir+"work.gitconfig") || !s.Exists(accountsDir+"job.gitconfig") {
		t.Error("the rename didn't move the account file")
	}
}
