package cli

import (
	"errors"
	"strings"

	"charm.land/huh/v2"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/tui"
	"github.com/vehkiya/doppel/internal/ui"
	"github.com/vehkiya/doppel/internal/update"
	"github.com/vehkiya/doppel/internal/version"
)

// browse runs the account browser until the user quits. Each action leaves
// the browser, runs as the matching command would, and comes back to it.
func (a *app) browse() int {
	selected, status := "", ""
	for {
		a.github.Forget() // gh may have signed in to a host since the browser last opened
		list, err := accounts.Load(a.env)
		if err != nil {
			return a.fail(err)
		}
		act, err := tui.Run(tui.Options{Env: a.env, Accounts: list, LoadKeyInfo: func() map[string]tui.KeyInfo { return a.keyInfo(list) },
			Selected: selected, Status: status,
			CheckUpdate: a.updateCheck()})
		if err != nil {
			return a.fail(err)
		}
		if act.Kind == tui.Quit {
			return 0
		}
		if act.ID != "" {
			selected = act.ID
		}
		var quit bool
		if status, quit = a.runAction(act); quit {
			return 0
		}
	}
}

// updateCheck is the browser's background check for a newer release, or nil
// when it's off: for development builds, or with DOPPEL_NO_UPDATE_CHECK set.
func (a *app) updateCheck() func() (string, bool) {
	if update.CheckDisabled(version.Version) {
		return nil
	}
	return func() (string, bool) {
		latest, newer, err := update.LatestCached(version.Version)
		return latest, err == nil && newer
	}
}

// runAction carries out one browser action. It returns a short status to
// show when the browser opens again; anything longer (output to read,
// warnings, errors) waits for Enter first. quit ends the browser, after
// doppel updated itself.
func (a *app) runAction(act tui.Action) (status string, quit bool) {
	defer a.unlockWrites() // a command that failed early may still hold the lock
	a.warnings = 0
	switch act.Kind {
	case tui.Upgrade:
		installed, err := update.Perform(version.Version, a.stdout, false)
		if err != nil {
			a.fail(err)
		}
		if err != nil || installed == "" {
			a.pause()
			return "", false
		}
		a.successf("Updated doppel to %s. Run doppel again to use it.", installed)
		return "", true
	case tui.Export:
		a.cmdExport([]string{act.ID})
		a.pause()
		return "", false
	case tui.Test:
		a.cmdTest([]string{act.ID})
		a.pause()
		return "", false
	case tui.Upload:
		a.cmdUpload([]string{act.ID})
		a.pause()
		return "", false
	case tui.Bind:
		folder, err := a.askFolder(act.ID)
		if err != nil {
			return a.status(nil, err), false
		}
		return a.status(a.bind(act.ID, []string{folder}, writeFlags{})), false
	case tui.Add:
		return a.status(a.addWithWizard("", writeFlags{})), false
	case tui.Edit:
		return a.status(a.editWithWizard(act.ID, writeFlags{})), false
	case tui.Delete:
		// The browser asked before deleting.
		return a.status(a.remove(act.ID, writeFlags{yes: true})), false
	case tui.SetDefault:
		return a.status(a.setDefault(act.ID, writeFlags{})), false
	}
	return "", false
}

// status sums up a change for the browser's status line: its result, or
// the error that stopped it. An error or a warning (other than the user
// cancelling) waits for Enter, so it can be read before the browser covers it.
func (a *app) status(res *ops.Result, err error) string {
	switch {
	case err != nil:
		a.fail(err)
		if !cancelled(err) {
			a.pause()
		}
		return "✗ " + err.Error()
	case a.warnings > 0:
		a.pause()
	}
	return "✓ " + res.Message
}

// askFolder asks for a folder to bind to an account.
func (a *app) askFolder(id string) (string, error) {
	var folder string
	err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Bind a folder to " + id).
			Description("Repos inside it will use this account").
			Placeholder("~/projects/work").Value(&folder).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("enter a folder")
				}
				_, _, err := a.env.NormalizeFolder(s, a.cwd)
				return err
			}),
	)))
	return strings.TrimSpace(folder), err
}

// pause waits for Enter before the browser comes back.
func (a *app) pause() {
	a.printf("\n%s", ui.Dim.Render("Press Enter to return to doppel… "))
	_, _ = a.stdin.ReadString('\n')
}

// keyInfo works out how each account's keys are kept, for the browser.
func (a *app) keyInfo(list []*accounts.Account) map[string]tui.KeyInfo {
	info := map[string]tui.KeyInfo{}
	for _, acc := range list {
		var ki tui.KeyInfo
		if acc.AuthKey != "" {
			ki.Auth = a.keyWords(acc.AuthKey)
		}
		if acc.SigningKey != "" && !acc.SigningKey.SameKey(acc.AuthKey) {
			ki.Signing = a.keyWords(acc.SigningKey)
		}
		info[acc.ID] = ki
	}
	return info
}

// keyWords describes how a key is kept, such as ["passphrase", "in agent"].
func (a *app) keyWords(key keys.Ref) []string {
	path := key.Map(a.env.Expand)
	words := []string{keys.CheckProtection(path).String()}
	if loaded, running := keys.InAgent(path); running && loaded {
		words = append(words, "in agent")
	}
	return words
}
