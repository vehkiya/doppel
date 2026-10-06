package cli

import (
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
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
	return a.exitStatus(a.bind(positional[0], positional[1:], w))
}

// bind binds folders to an account, asking before moving one bound to
// another account.
func (a *app) bind(id string, folders []string, w writeFlags) (*ops.Result, error) {
	return a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Bind(ctx, list, id, folders)
	})
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
	return a.exitStatus(a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Unbind(ctx, list, positional)
	}))
}
