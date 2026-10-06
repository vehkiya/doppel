package cli

import (
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/store"
	"github.com/vehkiya/doppel/internal/ui"
)

const testUsage = "doppel test [<id>]"

// cmdTest tries each account's keys for real: it logs in to every host and
// signs a test message, then verifies it as Git would.
func (a *app) cmdTest(args []string) int {
	positional, code, ok := a.parseCommand(newFlagSet("test"), args, testUsage)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		return a.usageError(testUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	if len(positional) == 1 {
		acc, err := ops.Find(list, positional[0])
		if err != nil {
			return a.fail(err)
		}
		list = []*accounts.Account{acc}
	}
	if len(list) == 0 {
		a.printf("No accounts to test.\n")
		return 0
	}
	signers, _, err := store.SignersFile(a.env)
	if err != nil {
		return a.fail(err)
	}

	ctx := a.opsContext(writeFlags{})
	failed := false
	for i, acc := range list {
		if i > 0 {
			a.printf("\n")
		}
		a.printf("%s\n", ui.Accent.Render(acc.ID))
		if ops.Test(ctx, acc, signers, !a.interactive, a.checkRow) {
			failed = true
		}
		// Without a terminal, the rows above already name the command.
		if a.interactive {
			for _, hint := range ops.KeychainHints(ctx, acc.AuthKey, acc.SigningKey) {
				a.notef("  %s", hint)
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

// checkRow shows the outcome of one check.
func (a *app) checkRow(c ops.Check) {
	switch {
	case c.Skipped:
		a.printf("  %s %-12s %s\n", ui.Dim.Render("–"), c.Label, ui.Dim.Render(c.Detail))
	case c.OK:
		a.printf("  %s %-12s %s\n", ui.OK.Render("✓"), c.Label, c.Detail)
	default:
		a.printf("  %s %-12s %s\n", ui.Error.Render("✗"), c.Label, c.Detail)
	}
}
