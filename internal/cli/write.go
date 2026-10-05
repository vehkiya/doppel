package cli

import (
	"fmt"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/plan"
	"github.com/vehkiya/doppel/internal/store"
	"github.com/vehkiya/doppel/internal/ui"
)

// save writes list as the complete set of accounts, first generating any
// pending keys. With --dry-run it shows what would change instead.
func (a *app) save(list []*accounts.Account, w writeFlags, done string, pending ...pendingKey) int {
	if err := accounts.ValidateAll(a.env, list); err != nil {
		return a.fail(err)
	}
	var opts store.Options
	if w.dryRun {
		for _, k := range pending {
			a.notef("Dry run: would generate the %s %s", k.purpose, a.env.Shorten(k.path))
		}
		opts.PublicKey = a.dryRunKeys(pending)
	} else if err := a.generateKeys(pending); err != nil {
		return a.fail(err)
	}
	p, err := plan.New()
	if err != nil {
		return a.fail(err)
	}
	defer p.Close()
	if err := store.Stage(a.env, p, list, opts); err != nil {
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
