package cli

import (
	"fmt"

	"github.com/vehkiya/doppel/internal/plan"
	"github.com/vehkiya/doppel/internal/store"
)

const uninstallUsage = "doppel uninstall [--dry-run] [--yes]"

func (a *app) cmdUninstall(args []string) int {
	fs := newFlagSet("uninstall")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, uninstallUsage)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		return a.usageError(uninstallUsage)
	}
	global := a.env.Shorten(a.env.GlobalConfigPath())
	question := fmt.Sprintf("Remove doppel's include from %s and its block from allowed_signers? Git will stop using doppel's accounts; account files and keys are kept.", global)
	if err := a.confirm(question, w.assumeYes()); err != nil {
		return a.fail(err)
	}
	if !w.dryRun {
		if err := a.lockWrites(); err != nil {
			return a.fail(err)
		}
	}
	p, err := plan.New()
	if err != nil {
		return a.fail(err)
	}
	defer p.Close()
	removedInclude, err := store.RemoveInclude(a.env, p)
	if err != nil {
		return a.fail(err)
	}
	removedSigners, err := store.RemoveSigners(a.env, p)
	if err != nil {
		return a.fail(err)
	}
	if !removedInclude && !removedSigners {
		a.notef("%s doesn't include doppel's accounts; nothing to remove.", global)
		return 0
	}
	done := "Removed doppel's include from " + global
	if removedSigners {
		done += " and its keys from allowed_signers"
	}
	code = a.finish(p, w, done)
	if code == 0 && !w.dryRun {
		a.notef("Account files are still in %s. Any doppel command that changes accounts adds the include back.",
			a.env.Shorten(a.env.AccountsDir()))
	}
	return code
}
