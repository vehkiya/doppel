package cli

import (
	"fmt"

	"github.com/vehkiya/doppel/internal/ops"
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
	res, err := ops.Uninstall(a.opsContext(w), ops.SaveOptions{DryRun: w.dryRun, Lock: a.lockWrites})
	if err != nil {
		return a.fail(err)
	}
	if len(res.Changes) == 0 && res.Message == "" {
		a.notef("%s doesn't include doppel's accounts; nothing to remove.", global)
		return 0
	}
	a.showResult(res)
	return 0
}
