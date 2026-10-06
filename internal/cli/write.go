package cli

import (
	"errors"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/store"
	"github.com/vehkiya/doppel/internal/ui"
)

// lockWrites takes the write lock, unless this command already holds it.
// It's released when the command ends.
func (a *app) lockWrites() error {
	if a.unlock != nil {
		return nil
	}
	release, err := store.Lock(a.env)
	if err != nil {
		return err
	}
	a.unlock = release
	return nil
}

func (a *app) unlockWrites() {
	if a.unlock != nil {
		a.unlock()
		a.unlock = nil
	}
}

// loadForWrite loads the accounts a command is about to change and save.
// A command that can't stop to ask anything takes the write lock first, so
// commands running at once go one after the other. One that may ask (it has a
// terminal) leaves the lock to the save, so a prompt never keeps other
// commands waiting; the save then fails if the files changed in the meantime.
func (a *app) loadForWrite(w writeFlags) ([]*accounts.Account, error) {
	if !a.interactive && !w.dryRun {
		if err := a.lockWrites(); err != nil {
			return nil, err
		}
	}
	return accounts.Load(a.env)
}

// opsContext is what operations need from this command: where it runs, and
// how to confirm (--yes answers up front).
func (a *app) opsContext(w writeFlags) ops.Context {
	return ops.Context{
		Env: a.env, Cwd: a.cwd,
		Confirm:  func(question string) error { return a.confirm(question, w.assumeYes()) },
		GitHub:   a.github,
		Keychain: a.macKeychain,
	}
}

// change is how every command, wizard and browser action changes accounts:
// it loads them, works out the change with op, and saves it, showing what
// happened. With --dry-run it shows what would change instead.
func (a *app) change(w writeFlags, op func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error)) (*ops.Result, error) {
	list, err := a.loadForWrite(w)
	if err != nil {
		return nil, err
	}
	ctx := a.opsContext(w)
	ch, err := op(ctx, list)
	if err != nil {
		return nil, err
	}
	for _, warning := range ch.Warnings {
		a.warnf("%s", warning)
	}
	opts := ops.SaveOptions{DryRun: w.dryRun, Lock: a.lockWrites}
	if a.interactive {
		opts.Generate = a.generateKey
	}
	res, err := ops.Save(ctx, ch, opts)
	if err != nil {
		return nil, err
	}
	a.showResult(res)
	if !res.DryRun {
		a.rememberPassphrases(res.NewKeys, w.assumeYes())
	}
	return res, nil
}

// exitStatus ends a command that changed accounts: 0 once it's done, or the
// error's status.
func (a *app) exitStatus(_ *ops.Result, err error) int {
	if err != nil {
		return a.fail(err)
	}
	return 0
}

// showResult says what a save did: the files it wrote, then its notes. For
// a dry run it shows the keys it would generate and the diff of every file.
func (a *app) showResult(res *ops.Result) {
	if res.DryRun {
		for _, k := range res.NewKeys {
			a.notef("Dry run: would generate the %s %s", k.Purpose, a.env.Shorten(k.Path))
		}
		if len(res.Changes) == 0 {
			a.notef("Dry run: nothing would change.")
			return
		}
		a.notef("Dry run: nothing was written. These changes would be made:")
		for _, c := range res.Changes {
			a.printf("\n")
			ui.WriteDiff(a.stdout, a.env.Shorten(c.Path), c)
		}
		return
	}
	a.successf("%s", res.Message)
	for _, c := range res.Changes {
		verb := "updated"
		switch {
		case !c.Existed:
			verb = "created"
		case !c.Exists:
			verb = "removed"
		}
		a.notef("  %s %s", verb, a.env.Shorten(c.Path))
	}
	for _, note := range res.Notes {
		a.notef("%s", note)
	}
}

// generateKey creates a key, letting ssh-keygen ask for its passphrase on
// the terminal.
func (a *app) generateKey(k ops.NewKey) error {
	a.printf("Generating the %s %s. Choose a passphrase to protect it.\n", k.Purpose, a.env.Shorten(k.Path))
	if err := a.generate(k.Path, k.Comment); err != nil {
		return err
	}
	if keys.CheckProtection(keys.Ref(k.Path)) == keys.Unencrypted {
		a.warnf("%s has no passphrase: anyone who copies it can use it. Add one with: ssh-keygen -p -f %s",
			a.env.Shorten(k.Path), a.env.Shorten(k.Path))
	}
	return nil
}

// cancelled reports whether err is the user declining or cancelling.
func cancelled(err error) bool { return errors.Is(err, errCancelled) }
