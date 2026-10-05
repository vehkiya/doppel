package cli

import (
	"fmt"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/plan"
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
// terminal) leaves the lock to save, so a prompt never keeps other commands
// waiting; save then fails if the files changed in the meantime.
func (a *app) loadForWrite(w writeFlags) ([]*accounts.Account, error) {
	if !a.interactive && !w.dryRun {
		if err := a.lockWrites(); err != nil {
			return nil, err
		}
	}
	return accounts.Load(a.env)
}

// save writes list as the complete set of accounts, first generating any
// pending keys. With --dry-run it shows what would change instead.
func (a *app) save(list []*accounts.Account, w writeFlags, done string, pending ...pendingKey) int {
	return a.saveRemoving(nil, list, w, done, pending...)
}

// saveRemoving is save for a command that deletes accounts: removed are
// those accounts, which are no longer in list. No other account is deleted.
func (a *app) saveRemoving(removed, list []*accounts.Account, w writeFlags, done string, pending ...pendingKey) int {
	if err := accounts.ValidateAll(a.env, list); err != nil {
		return a.fail(err)
	}
	opts := store.Options{Removed: removed}
	if w.dryRun {
		for _, k := range pending {
			a.notef("Dry run: would generate the %s %s", k.purpose, a.env.Shorten(k.path))
		}
		opts.PublicKey = a.dryRunKeys(pending)
	} else {
		if err := a.generateKeys(pending); err != nil {
			return a.fail(err)
		}
		if err := a.lockWrites(); err != nil {
			return a.fail(err)
		}
	}
	p, err := plan.New()
	if err != nil {
		return a.fail(err)
	}
	defer p.Close()
	if err := store.Save(a.env, p, list, opts); err != nil {
		return a.fail(err)
	}
	code := a.finish(p, w, done)
	if code == 0 && !w.dryRun && len(pending) > 0 {
		a.notef("Load new keys into your agent so Git doesn't ask for the passphrase on every commit: ssh-add <key>")
	}
	return code
}

// finish applies a plan, or prints it for --dry-run.
func (a *app) finish(p *plan.Plan, w writeFlags, done string) int {
	if w.dryRun {
		changes, err := p.Changes()
		if err != nil {
			return a.fail(err)
		}
		if len(changes) == 0 {
			a.notef("Dry run: nothing would change.")
			return 0
		}
		a.notef("Dry run: nothing was written. These changes would be made:")
		for _, c := range changes {
			a.printf("\n")
			ui.WriteDiff(a.stdout, a.env.Shorten(c.Path), c)
		}
		return 0
	}
	changes, err := p.Apply()
	if err != nil {
		return a.fail(err)
	}
	a.successf("%s", done)
	for _, c := range changes {
		verb := "updated"
		switch {
		case !c.Existed:
			verb = "created"
		case !c.Exists:
			verb = "removed"
		}
		a.notef("  %s %s", verb, a.env.Shorten(c.Path))
	}
	return 0
}

// bindFolders adds folders to acc. A folder bound to another account moves
// to acc after confirmation.
func (a *app) bindFolders(list []*accounts.Account, acc *accounts.Account, inputs []string, w writeFlags) error {
	for _, input := range inputs {
		folder, exists, err := a.env.NormalizeFolder(input, a.cwd)
		if err != nil {
			return err
		}
		if !exists {
			a.warnf("%s doesn't exist yet; the rule applies to repos created there later", folder)
		}
		owner := accounts.FolderOwner(a.env, list, folder)
		if owner == acc {
			continue
		}
		if owner != nil {
			question := fmt.Sprintf("%s is bound to %s. Move it to %s?", folder, owner.ID, acc.ID)
			if err := a.confirm(question, w.assumeYes()); err != nil {
				return err
			}
			owner.RemoveFolder(a.env, folder)
		}
		acc.Folders = append(acc.Folders, folder)
	}
	return nil
}
