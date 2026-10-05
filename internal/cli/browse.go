package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/tui"
	"github.com/vehkiya/doppel/internal/ui"
)

// browse runs the account browser until the user quits. Each action leaves
// the browser, runs as the matching command would, and comes back to it.
func (a *app) browse() int {
	selected, status := "", ""
	for {
		list, err := accounts.Load(a.env)
		if err != nil {
			return a.fail(err)
		}
		act, err := tui.Run(tui.Options{Env: a.env, Accounts: list, KeyInfo: a.keyInfo(list), Selected: selected, Status: status})
		if err != nil {
			return a.fail(err)
		}
		if act.Kind == tui.Quit {
			return 0
		}
		if act.ID != "" {
			selected = act.ID
		}
		status = a.runAction(act)
	}
}

// runAction carries out one browser action. It returns a short status to
// show when the browser opens again; anything longer (output to read,
// warnings, errors) waits for Enter first.
func (a *app) runAction(act tui.Action) string {
	switch act.Kind {
	case tui.Export:
		a.cmdExport([]string{act.ID})
		a.pause()
		return ""
	case tui.Test:
		a.cmdTest([]string{act.ID})
		a.pause()
		return ""
	case tui.Upload:
		a.cmdUpload([]string{act.ID})
		a.pause()
		return ""
	case tui.Bind:
		folder, err := a.askFolder(act.ID)
		if err != nil {
			return statusOf(err.Error(), "")
		}
		return a.captured(func() int { return a.cmdBind([]string{act.ID, folder}) })
	case tui.Add:
		return a.captured(func() int { return a.addWithWizard("", writeFlags{}) })
	case tui.Edit:
		return a.captured(func() int { return a.cmdEdit([]string{act.ID}) })
	case tui.Delete:
		return a.captured(func() int { return a.cmdRm([]string{act.ID, "--yes"}) })
	case tui.SetDefault:
		return a.captured(func() int { return a.cmdDefault([]string{act.ID}) })
	}
	return ""
}

// captured runs a command, showing its output as usual, and sums it up as a
// status for the browser. Warnings and errors (other than a cancellation)
// wait for Enter, so they can be read before the browser covers them.
func (a *app) captured(run func() int) string {
	var out, errOut bytes.Buffer
	stdout, stderr := a.stdout, a.stderr
	a.stdout, a.stderr = io.MultiWriter(stdout, &out), io.MultiWriter(stderr, &errOut)
	run()
	a.stdout, a.stderr = stdout, stderr

	problems := ansi.Strip(errOut.String())
	if strings.TrimSpace(problems) != "" && !strings.Contains(problems, errCancelled.Error()) {
		a.pause()
	}
	return statusOf(ansi.Strip(out.String()), problems)
}

// statusOf picks the line worth showing from a command's output: an error,
// else its result.
func statusOf(out, problems string) string {
	for _, line := range strings.Split(problems, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "✗") {
			return strings.TrimSpace(line)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "✓") {
			return strings.TrimSpace(line)
		}
	}
	return ""
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
		if acc.SigningKey != "" && keys.PublicPath(acc.SigningKey) != keys.PublicPath(acc.AuthKey) {
			ki.Signing = a.keyWords(strings.TrimSuffix(acc.SigningKey, ".pub"))
		}
		info[acc.ID] = ki
	}
	return info
}

// keyWords describes how a key is kept, such as ["passphrase", "in agent"].
func (a *app) keyWords(key string) []string {
	path := a.env.Expand(key)
	words := []string{keys.CheckProtection(path).String()}
	if loaded, running := keys.InAgent(path); running && loaded {
		words = append(words, "in agent")
	}
	return words
}
