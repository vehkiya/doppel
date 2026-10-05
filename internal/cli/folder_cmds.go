package cli

import (
	"fmt"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
)

const bindUsage = "doppel bind <id> <folder>... [--dry-run] [--yes]"

func (a *app) cmdBind(args []string) int {
	fs := newFlagSet("bind")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, bindUsage)
	if !ok {
		return code
	}
	if len(positional) < 2 {
		return a.usageError(bindUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := accounts.Find(list, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}
	if err := a.bindFolders(list, acc, positional[1:], w); err != nil {
		return a.fail(err)
	}
	return a.save(list, w, fmt.Sprintf("Bound to %s: %s", acc.ID, strings.Join(acc.Folders, ", ")))
}

const unbindUsage = "doppel unbind <folder>... [--dry-run]"

func (a *app) cmdUnbind(args []string) int {
	fs := newFlagSet("unbind")
	var w writeFlags
	w.register(fs)
	positional, code, ok := a.parseCommand(fs, args, unbindUsage)
	if !ok {
		return code
	}
	if len(positional) == 0 {
		return a.usageError(unbindUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	var removed []string
	for _, input := range positional {
		folder, _, err := a.env.NormalizeFolder(input, a.cwd)
		if err != nil {
			return a.fail(err)
		}
		owner := accounts.FolderOwner(a.env, list, folder)
		if owner == nil {
			return a.fail(fmt.Errorf("%s isn't bound to any account", folder))
		}
		owner.RemoveFolder(a.env, folder)
		removed = append(removed, fmt.Sprintf("%s (was %s)", folder, owner.ID))
	}
	return a.save(list, w, "Unbound "+strings.Join(removed, ", "))
}
