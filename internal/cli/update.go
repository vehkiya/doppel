package cli

import (
	"github.com/vehkiya/doppel/internal/update"
	"github.com/vehkiya/doppel/internal/version"
)

const updateUsage = "doppel update [--check] [--force]"

// cmdUpdate installs the latest signed release over this binary. With
// --check it only says whether there's a newer one.
func (a *app) cmdUpdate(args []string) int {
	fs := newFlagSet("update")
	var check, force bool
	fs.BoolVar(&check, "check", false, "only say whether a newer release exists")
	fs.BoolVar(&force, "force", false, "reinstall the latest release even when up to date")
	positional, code, ok := a.parseCommand(fs, args, updateUsage)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		return a.usageError(updateUsage)
	}
	if check {
		rel, newer, err := update.Latest(version.Version)
		if err != nil {
			return a.fail(err)
		}
		if newer {
			a.printf("doppel %s is available (installed: %s). Run `doppel update` to install it.\n", rel.TagName, version.Version)
		} else {
			a.printf("doppel is up to date (%s)\n", version.Version)
		}
		return 0
	}
	installed, err := update.Perform(version.Version, a.stdout, force)
	if err != nil {
		return a.fail(err)
	}
	if installed != "" {
		a.successf("Updated doppel to %s", installed)
	}
	return 0
}
