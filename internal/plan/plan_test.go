package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// snapshot lists every file under dir, hidden backups included, with its
// content, and every symlink with its target.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			got[rel] = "-> " + target
		case !d.IsDir():
			data, err := os.ReadFile(path) //nolint:gosec // a file in the test's temp directory
			if err != nil {
				return err
			}
			got[rel] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// mixedPlan changes four files in one plan: one replaced (with an older
// backup already there), one replaced with no backup yet, one created, and
// one removed. Applying it takes 3 backups, 3 writes and 1 removal.
func mixedPlan(t *testing.T) (dir string, p *Plan) {
	t.Helper()
	dir = t.TempDir()
	write(t, filepath.Join(dir, "a.conf"), "a old\n")
	write(t, filepath.Join(dir, ".a.conf.doppel.bak"), "a older\n")
	write(t, filepath.Join(dir, "b.conf"), "b old\n")
	write(t, filepath.Join(dir, "gone.conf"), "gone old\n")

	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	// In this order, which is the order Apply writes them in.
	for _, name := range []string{"a", "b", "c"} {
		if err := p.SetContent(filepath.Join(dir, name+".conf"), []byte(name+" new\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Remove(filepath.Join(dir, "gone.conf")); err != nil {
		t.Fatal(err)
	}
	return dir, p
}

// failOn makes the nth call (counting from 1) of *fn, and every call after
// it if keepFailing, return an error. It returns how many calls were made.
func failOn[F any](t *testing.T, fn *F, wrap func(real F, fail func() bool) F, n int, keepFailing bool) *int {
	t.Helper()
	real := *fn
	t.Cleanup(func() { *fn = real })
	calls := 0
	*fn = wrap(real, func() bool {
		calls++
		return calls == n || (keepFailing && calls > n)
	})
	return &calls
}

var errInjected = errors.New("injected failure")

func failWrites(t *testing.T, n int, keepFailing bool) *int {
	return failOn(t, &writeFile, func(real func(string, []byte, os.FileMode) error, fail func() bool) func(string, []byte, os.FileMode) error {
		return func(path string, data []byte, perm os.FileMode) error {
			if fail() {
				return errInjected
			}
			return real(path, data, perm)
		}
	}, n, keepFailing)
}

func failRemovals(t *testing.T, n int, keepFailing bool) *int {
	return failOn(t, &removeFile, func(real func(string) error, fail func() bool) func(string) error {
		return func(path string) error {
			if fail() {
				return errInjected
			}
			return real(path)
		}
	}, n, keepFailing)
}

func TestApplyWritesBacksUpAndRemoves(t *testing.T) {
	dir, p := mixedPlan(t)
	writes := failWrites(t, 0, false)
	removals := failRemovals(t, 0, false)
	changes, err := p.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 || *writes != 6 || *removals != 1 {
		t.Fatalf("%d changes, %d writes, %d removals; want 4, 6, 1", len(changes), *writes, *removals)
	}
	want := map[string]string{
		"a.conf": "a new\n", ".a.conf.doppel.bak": "a old\n",
		"b.conf": "b new\n", ".b.conf.doppel.bak": "b old\n",
		"c.conf":                "c new\n",
		".gone.conf.doppel.bak": "gone old\n",
	}
	if got := snapshot(t, dir); !reflect.DeepEqual(got, want) {
		t.Errorf("files after Apply:\n got %q\nwant %q", got, want)
	}
}

func TestApplyLeavesNothingBehindWhenAnyStepFails(t *testing.T) {
	steps := []struct {
		name  string
		fail  func(t *testing.T, n int) *int
		calls int
	}{
		{"write", func(t *testing.T, n int) *int { return failWrites(t, n, false) }, 6}, // 3 backups, then 3 files
		{"removal", func(t *testing.T, n int) *int { return failRemovals(t, n, false) }, 1},
	}
	for _, step := range steps {
		for n := 1; n <= step.calls; n++ {
			t.Run(fmt.Sprintf("%s %d", step.name, n), func(t *testing.T) {
				dir, p := mixedPlan(t)
				before := snapshot(t, dir)
				step.fail(t, n)

				_, err := p.Apply()
				if !errors.Is(err, errInjected) {
					t.Fatalf("Apply error = %v, want the injected failure", err)
				}
				if got := snapshot(t, dir); !reflect.DeepEqual(got, before) {
					t.Errorf("files changed although Apply failed:\n got %q\nwant %q", got, before)
				}
				if !strings.Contains(err.Error(), "nothing") {
					t.Errorf("error doesn't say the files are as they were: %v", err)
				}
			})
		}
	}
}

func TestApplyRollbackKeepsASymlinkAndItsTarget(t *testing.T) {
	dir := t.TempDir()
	// A dotfiles manager's link whose target doesn't exist yet.
	if err := os.Symlink(filepath.Join(dir, "store", "link.conf"), filepath.Join(dir, "link.conf")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "b.conf"), "b old\n")
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, path := range []string{"link.conf", "b.conf"} { // the link is written first
		if err := p.SetContent(filepath.Join(dir, path), []byte("new\n")); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshot(t, dir)
	failWrites(t, 3, false) // the backup of b.conf, then the link's file, then b.conf

	if _, err := p.Apply(); !errors.Is(err, errInjected) {
		t.Fatalf("Apply error = %v", err)
	}
	if got := snapshot(t, dir); !reflect.DeepEqual(got, before) {
		t.Errorf("files after rollback:\n got %q\nwant %q", got, before)
	}
}

func TestApplySaysWhatItCouldNotPutBack(t *testing.T) {
	dir, p := mixedPlan(t)
	failWrites(t, 5, true) // the second file fails, and so does every restore after it

	_, err := p.Apply()
	if !errors.Is(err, errInjected) {
		t.Fatalf("Apply error = %v", err)
	}
	for _, want := range []string{"couldn't put everything back", "restore " + filepath.Join(dir, "a.conf"), ".a.conf.doppel.bak"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestApplyRefusesFilesChangedSinceTheyWereRead(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, dir string)
	}{
		{"edited", func(t *testing.T, dir string) { write(t, filepath.Join(dir, "a.conf"), "someone else's edit\n") }},
		{"deleted", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "b.conf")); err != nil {
				t.Fatal(err)
			}
		}},
		{"created", func(t *testing.T, dir string) { write(t, filepath.Join(dir, "c.conf"), "someone else's file\n") }},
		{"edited before removal", func(t *testing.T, dir string) { write(t, filepath.Join(dir, "gone.conf"), "kept\n") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, p := mixedPlan(t)
			c.change(t, dir)
			before := snapshot(t, dir)

			_, err := p.Apply()
			var stale *StaleError
			if !errors.As(err, &stale) {
				t.Fatalf("Apply error = %v, want a StaleError", err)
			}
			if !strings.Contains(err.Error(), "run the command again") {
				t.Errorf("error doesn't say what to do: %v", err)
			}
			if got := snapshot(t, dir); !reflect.DeepEqual(got, before) {
				t.Errorf("Apply wrote files although one was stale:\n got %q\nwant %q", got, before)
			}
		})
	}
}
