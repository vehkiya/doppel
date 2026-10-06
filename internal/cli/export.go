package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/proc"
	"github.com/vehkiya/doppel/internal/ui"
)

const exportUsage = "doppel export <id> [--auth | --signing] [--no-copy]"

func (a *app) cmdExport(args []string) int {
	fs := newFlagSet("export")
	var onlyAuth, onlySigning, noCopy bool
	fs.BoolVar(&onlyAuth, "auth", false, "only the auth key")
	fs.BoolVar(&onlySigning, "signing", false, "only the signing key")
	fs.BoolVar(&noCopy, "no-copy", false, "don't copy the key to the clipboard")
	positional, code, ok := a.parseCommand(fs, args, exportUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 || (onlyAuth && onlySigning) {
		return a.usageError(exportUsage)
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc, err := ops.Find(list, positional[0])
	if err != nil {
		return a.fail(err)
	}

	exported, err := ops.ExportKeys(a.opsContext(writeFlags{}), acc, onlyAuth, onlySigning)
	if err != nil {
		return a.fail(err)
	}
	a.showExport(acc, exported, !noCopy)
	return 0
}

// showExport prints the public keys and how to add them on each of acc's
// hosts. With toClipboard, a single key also goes on the clipboard.
func (a *app) showExport(acc *accounts.Account, exported []ops.ExportKey, toClipboard bool) {
	ctx := a.opsContext(writeFlags{})
	for _, k := range exported {
		a.printf("%s %s\n%s\n\n", ui.Label.Render(k.Purpose()+" for "+acc.ID), ui.Dim.Render("("+ops.PublicName(ctx, k.Key)+")"), k.Line)
	}
	switch {
	case !toClipboard:
	case len(exported) == 1:
		if err := a.copy(exported[0].Line); err != nil {
			a.warnf("Couldn't copy the key: %v", err)
		} else {
			a.successf("Copied to the clipboard\n")
		}
	default:
		a.notef("Two keys, so nothing was copied. Copy one with --auth or --signing.\n")
	}

	title := ops.KeyTitle(acc)
	for _, h := range acc.Hosts {
		a.printf("%s\n", ui.Accent.Render(h))
		for _, k := range exported {
			file := cmpOr(k.Key.Public().Map(a.env.Shorten).PublicPath(), "<the public key file>")
			for i, step := range a.github.Steps(h, k.Use, title, file) {
				a.printf("  %d. %s\n", i+1, step)
			}
		}
		a.printf("\n")
	}
}

// copyToClipboard puts text on the clipboard: through the terminal (OSC 52,
// which also works over SSH) and with the first clipboard tool it finds.
func copyToClipboard(text string) error {
	copied := false
	if term.IsTerminal(os.Stdout.Fd()) {
		_, _ = fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
		copied = true
	}
	for _, tool := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}, {"pbcopy"}} {
		if _, err := exec.LookPath(tool[0]); err != nil {
			continue
		}
		cmd, finish := proc.Command(proc.Local, tool[0], tool[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if finish(cmd.Run()) == nil {
			return nil
		}
	}
	if copied {
		return nil
	}
	return errors.New("no clipboard tool found (wl-copy, xclip, xsel or pbcopy)")
}
