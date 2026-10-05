package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new", "file")
	if err := Write(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("two"), 0640); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the test's own file
	if err != nil || string(data) != "two" {
		t.Errorf("content = %q, %v", data, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestWriteLeavesNothingWhenItFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a-folder")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Write(target, []byte("x"), 0600); err == nil {
		t.Fatal("replaced a folder with a file")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}
