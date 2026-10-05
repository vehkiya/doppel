package store

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/plan"
	"github.com/vehkiya/doppel/internal/testenv"
)

func newAccount(id string) *accounts.Account {
	return &accounts.Account{ID: id, Name: "Jane", Email: id + "@example.com", Hosts: []string{accounts.DefaultHost}}
}

// saveAll writes list as the whole set of accounts and applies it.
func saveAll(t *testing.T, s *testenv.Sandbox, list []*accounts.Account, opts Options) error {
	t.Helper()
	p, err := plan.New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := Save(s.Env(), p, list, opts); err != nil {
		return err
	}
	_, err = p.Apply()
	return err
}

func TestSaveDeletesOnlyWhatItWasTold(t *testing.T) {
	s := testenv.New(t)
	if err := saveAll(t, s, []*accounts.Account{newAccount("a"), newAccount("b"), newAccount("c")}, Options{}); err != nil {
		t.Fatal(err)
	}
	loaded, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := loaded[0], loaded[1], loaded[2]

	// Dropping an account from the list without saying it's removed deletes nothing.
	err = saveAll(t, s, []*accounts.Account{a, c}, Options{})
	var stale *plan.StaleError
	if !errors.As(err, &stale) || !strings.HasSuffix(stale.Path, "b.gitconfig") {
		t.Errorf("Save without b and without removing it: %v", err)
	}
	if !s.Exists(".config/doppel/accounts/b.gitconfig") {
		t.Error("b.gitconfig was deleted although nothing asked for it")
	}

	// Removing it says so.
	if err := saveAll(t, s, []*accounts.Account{a, c}, Options{Removed: []*accounts.Account{b}}); err != nil {
		t.Fatal(err)
	}
	if s.Exists(".config/doppel/accounts/b.gitconfig") || !s.Exists(".config/doppel/accounts/a.gitconfig") || !s.Exists(".config/doppel/accounts/c.gitconfig") {
		t.Error("Save didn't delete exactly b.gitconfig")
	}

	// A rename deletes the old file, and only that one.
	loaded, _ = accounts.Load(s.Env())
	loaded[0].ID = "z"
	if err := saveAll(t, s, loaded, Options{}); err != nil {
		t.Fatal(err)
	}
	if s.Exists(".config/doppel/accounts/a.gitconfig") || !s.Exists(".config/doppel/accounts/z.gitconfig") || !s.Exists(".config/doppel/accounts/c.gitconfig") {
		t.Error("renaming a to z didn't move exactly that file")
	}
}

func TestSaveRefusesAnInvalidAccount(t *testing.T) {
	s := testenv.New(t)
	acc := newAccount("a")
	acc.Folders = []string{`~/x"y/`}
	err := saveAll(t, s, []*accounts.Account{acc}, Options{})
	if err == nil || !strings.Contains(err.Error(), "doppel.folder") {
		t.Errorf("Save with a folder that breaks Git's config: %v", err)
	}
	if s.Exists(".config/doppel") {
		t.Error("Save wrote files for an invalid account")
	}
}

func TestLockIsExclusive(t *testing.T) {
	s := testenv.New(t)
	env := s.Env()
	release, err := Lock(env)
	if err != nil {
		t.Fatal(err)
	}

	old := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	if _, err := Lock(env); err == nil || !strings.Contains(err.Error(), "another doppel command") {
		t.Errorf("a second Lock while the first is held: %v", err)
	}

	release()
	lockWait = old
	second, err := Lock(env)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	second()
	if _, err := os.Stat(env.DoppelDir()); err != nil {
		t.Error(err)
	}
}
