package cli

import (
	"fmt"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
)

const uploadUsage = "doppel upload <id> [--auth | --signing]"

// cmdUpload adds an account's keys to its user on each GitHub host it uses,
// through gh. Without gh, or when gh isn't signed in as that user, it shows
// how to add them by hand instead.
func (a *app) cmdUpload(args []string) int {
	fs := newFlagSet("upload")
	var onlyAuth, onlySigning bool
	fs.BoolVar(&onlyAuth, "auth", false, "only the auth key")
	fs.BoolVar(&onlySigning, "signing", false, "only the signing key")
	positional, code, ok := a.parseCommand(fs, args, uploadUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 || (onlyAuth && onlySigning) {
		return a.usageError(uploadUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc, err := ops.Find(list, positional[0])
	if err != nil {
		return a.fail(err)
	}
	ctx := a.opsContext(writeFlags{})
	exported, err := ops.ExportKeys(ctx, acc, onlyAuth, onlySigning)
	if err != nil {
		return a.fail(err)
	}
	res, err := ops.Upload(ctx, acc, exported)
	if res != nil {
		for _, up := range res.Uploaded {
			if up.Already {
				a.successf("%s is already one of %s's %s keys", up.Key, up.Where, up.Kind)
			} else {
				a.successf("Added %s to %s's %s keys", up.Key, up.Where, up.Kind)
			}
		}
	}
	if err != nil {
		return a.fail(err)
	}
	if res.ByHand != "" {
		a.warnf("%s. Meanwhile, here's how to add the keys by hand.\n", res.ByHand)
		a.showExport(acc, exported, true)
		return 0
	}
	if res.Spelling != "" {
		a.offerGitHubUser(acc, res.Spelling)
	}
	return 0
}

// offerGitHubUser offers to store the GitHub user the way GitHub and gh
// spell it, when the account has it differently in capitals. gh matches the
// name exactly, so the other spelling only works because doppel looked it up.
func (a *app) offerGitHubUser(acc *accounts.Account, login string) {
	if !a.interactive {
		a.notef("GitHub spells the user %s, not %s. Correct it with: doppel edit %s --github-user %s", login, acc.GitHubUser, acc.ID, login)
		return
	}
	question := fmt.Sprintf("GitHub spells the user %s, not %s. Correct account %s?", login, acc.GitHubUser, acc.ID)
	if err := a.confirm(question, false); err != nil {
		return // declined, or the question was cancelled; the upload itself worked
	}
	_, err := a.change(writeFlags{}, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		return ops.Edit(ctx, list, ops.EditRequest{ID: acc.ID, GitHubUser: &login})
	})
	if err != nil {
		a.fail(err)
	}
}
