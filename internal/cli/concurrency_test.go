package cli

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
)

// another returns an app like a, as a second doppel process would be: its own
// output, no terminal, and no lock held.
func another(a *app) (*app, *bytes.Buffer) {
	var out bytes.Buffer
	b := *a
	b.stdout, b.stderr = &out, &out
	b.stdin = bufio.NewReader(strings.NewReader(""))
	b.interactive = false
	b.unlock = nil
	return &b, &out
}

// afterFirstRead runs do once, when its reader is first read from, which for
// a wizard is while it's open and asking its first question.
type afterFirstRead struct {
	r    io.Reader
	do   func()
	done bool
}

func (r *afterFirstRead) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.do()
	}
	return r.r.Read(p)
}

// editWizardWhile runs the edit wizard for work, and runs other
// as a second command while the wizard is open.
func editWizardWhile(t *testing.T, s *sandbox, other func(b *app)) (stderr string, code int) {
	t.Helper()
	a := s.newApp(s.Home)
	a.interactive = true
	a.stdin = bufio.NewReader(&afterFirstRead{
		r: strings.NewReader(script("", "jane@acme.io", "", "", "", "", "", "", "", "")),
		do: func() {
			b, _ := another(a)
			other(b)
		},
	})
	code = a.run([]string{"edit", "work"})
	return s.stderr.String(), code
}

func TestWizardDoesntOverwriteWhatChangedWhileItWasOpen(t *testing.T) {
	setup := func(t *testing.T) *sandbox {
		s := newSandbox(t)
		s.addAccount("personal", "jane@personal.dev")
		s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
		return s
	}

	t.Run("an account added meanwhile survives", func(t *testing.T) {
		s := setup(t)
		stderr, code := editWizardWhile(t, s, func(b *app) {
			if code := b.run([]string{"add", "side", "--name", "Jane", "--email", "jane@side.dev"}); code != 0 {
				t.Errorf("the other add exited %d", code)
			}
		})
		if code != 1 || !strings.Contains(stderr, "changed since it was read; run the command again") || !strings.Contains(stderr, "side.gitconfig") {
			t.Errorf("wizard exit %d, stderr:\n%s", code, stderr)
		}
		if !s.Exists(accountsDir + "side.gitconfig") {
			t.Error("the account added meanwhile was deleted")
		}
		if got := loadAccount(t, s, "work").Email; got != "jane@acme.com" {
			t.Errorf("the failed wizard still changed the email to %q", got)
		}
		// Running it again, now that it has the new account, works.
		s.tty = true
		s.stdin = script("", "jane@acme.io", "", "", "", "", "", "", "")
		s.mustRun("edit", "work")
		if got := loadAccount(t, s, "work").Email; got != "jane@acme.io" {
			t.Errorf("email = %q after running the wizard again", got)
		}
		if !s.Exists(accountsDir + "side.gitconfig") {
			t.Error("the account added meanwhile is gone after the second run")
		}
	})

	t.Run("a hand edit meanwhile survives", func(t *testing.T) {
		s := setup(t)
		stderr, code := editWizardWhile(t, s, func(*app) {
			s.SetConfig(accountsDir+"work.gitconfig", "pull.rebase", "true")
		})
		if code != 1 || !strings.Contains(stderr, "work.gitconfig changed since it was read") {
			t.Errorf("wizard exit %d, stderr:\n%s", code, stderr)
		}
		if !strings.Contains(s.Read(accountsDir+"work.gitconfig"), "rebase = true") {
			t.Error("the hand edit was lost")
		}
	})

	t.Run("an account removed meanwhile stays removed", func(t *testing.T) {
		s := setup(t)
		stderr, code := editWizardWhile(t, s, func(b *app) { b.run([]string{"rm", "personal", "--yes"}) })
		if code != 1 || !strings.Contains(stderr, "personal.gitconfig changed since it was read") {
			t.Errorf("wizard exit %d, stderr:\n%s", code, stderr)
		}
		if s.Exists(accountsDir + "personal.gitconfig") {
			t.Error("the wizard brought back an account that was removed meanwhile")
		}
	})
}

func TestConcurrentWritesRunOneAfterTheOther(t *testing.T) {
	s := newSandbox(t)
	ids := []string{"one", "two", "three", "four", "five", "six"}
	var wg sync.WaitGroup
	codes := make([]int, len(ids))
	outputs := make([]*bytes.Buffer, len(ids))
	for i, id := range ids {
		b, out := another(s.newApp(s.Home))
		outputs[i] = out
		wg.Go(func() {
			codes[i] = b.run([]string{"add", id, "--name", "Jane", "--email", id + "@example.com", "--folder", "~/" + id})
		})
	}
	wg.Wait()

	for i, code := range codes {
		if code != 0 {
			t.Errorf("add %s exited %d:\n%s", ids[i], code, outputs[i])
		}
	}
	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(ids) {
		t.Errorf("%d accounts after %d concurrent adds", len(list), len(ids))
	}
	index := s.Read(".config/doppel/index.gitconfig")
	for _, id := range ids {
		if !strings.Contains(index, "/"+id+".gitconfig") {
			t.Errorf("the index doesn't mention %s:\n%s", id, index)
		}
	}
	if got := strings.Count(s.Read(".gitconfig"), "[include]"); got != 1 {
		t.Errorf("the include was added %d times:\n%s", got, s.Read(".gitconfig"))
	}
	// Nothing is left half-done: doppel's files match the accounts. (doctor
	// has other things to say about six accounts that share ssh's own keys.)
	s.run("doctor")
	if out := s.stdout.String(); !strings.Contains(out, "doppel's files match the accounts") {
		t.Errorf("doctor:\n%s", out)
	}
}
