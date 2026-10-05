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
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/ui"
)

const exportUsage = "doppel export <id> [--auth | --signing] [--no-copy]"

// exportedKey is one public key export shows, and what it's for.
type exportedKey struct {
	key           string // the key path, expanded
	auth, signing bool
}

func (k exportedKey) purpose() string {
	switch {
	case k.auth && k.signing:
		return "Auth and signing key"
	case k.auth:
		return "Auth key"
	}
	return "Signing key"
}

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
	acc := accounts.Find(list, positional[0])
	if acc == nil {
		return a.fail(fmt.Errorf("no account named %s", positional[0]))
	}

	exported, err := a.keysToExport(acc, onlyAuth, onlySigning)
	if err != nil {
		return a.fail(err)
	}
	var lines []string
	for _, k := range exported {
		line, err := keys.ReadPublicLine(k.key)
		if err != nil {
			return a.fail(fmt.Errorf("%s: %w", a.env.Shorten(keys.PublicPath(k.key)), err))
		}
		lines = append(lines, line)
		a.printf("%s %s\n%s\n\n", ui.Label.Render(k.purpose()+" for "+acc.ID), ui.Dim.Render("("+a.env.Shorten(keys.PublicPath(k.key))+")"), line)
	}
	switch {
	case noCopy:
	case len(lines) == 1:
		if err := a.copy(lines[0]); err != nil {
			a.warnf("Couldn't copy the key: %v", err)
		} else {
			a.successf("Copied to the clipboard\n")
		}
	default:
		a.notef("Two keys, so nothing was copied. Copy one with --auth or --signing.\n")
	}

	title := keyTitle(acc)
	for _, h := range acc.Hosts {
		a.printf("%s\n", ui.Accent.Render(h))
		for _, k := range exported {
			for i, step := range hostSteps(h, k, title) {
				a.printf("  %d. %s\n", i+1, step)
			}
		}
		a.printf("\n")
	}
	return 0
}

// keyTitle names a key on a host's settings page: the account and this machine.
func keyTitle(acc *accounts.Account) string {
	host, _ := os.Hostname()
	return fmt.Sprintf("doppel: %s (%s)", acc.ID, strings.TrimSuffix(host, ".local"))
}

// keysToExport picks the keys export shows. A key used for both logging in
// and signing is shown once.
func (a *app) keysToExport(acc *accounts.Account, onlyAuth, onlySigning bool) ([]exportedKey, error) {
	auth, signing := a.env.Expand(acc.AuthKey), a.env.Expand(acc.SigningKey)
	switch {
	case onlyAuth && auth == "":
		return nil, fmt.Errorf("account %s has no auth key", acc.ID)
	case onlySigning && signing == "":
		return nil, fmt.Errorf("account %s doesn't sign", acc.ID)
	case auth == "" && signing == "":
		return nil, fmt.Errorf("account %s has no keys yet; add one with `doppel edit %s --generate-auth-key --sign-with-auth-key`", acc.ID, acc.ID)
	}
	same := auth != "" && signing != "" && keys.PublicPath(auth) == keys.PublicPath(signing)
	var out []exportedKey
	if auth != "" && !onlySigning {
		out = append(out, exportedKey{key: auth, auth: true, signing: same && !onlyAuth})
	}
	if signing != "" && !onlyAuth && (!same || len(out) == 0) {
		out = append(out, exportedKey{key: signing, signing: true})
	}
	return out, nil
}

// hostSteps says where to add a key on a host, and which key type to pick.
func hostSteps(host string, k exportedKey, title string) []string {
	switch {
	case strings.EqualFold(host, "github.com"):
		steps := []string{"Open https://github.com/settings/ssh/new", "Title: " + title}
		switch {
		case k.auth && k.signing:
			steps = append(steps, "Key type: Authentication Key. Then add it a second time with Key type: Signing Key.")
		case k.auth:
			steps = append(steps, "Key type: Authentication Key")
		default:
			steps = append(steps, "Key type: Signing Key")
		}
		return append(steps, "Paste the key and click Add SSH key.")

	case strings.Contains(strings.ToLower(host), "gitlab"):
		usage := "Signing"
		switch {
		case k.auth && k.signing:
			usage = "Authentication & Signing"
		case k.auth:
			usage = "Authentication"
		}
		return []string{
			"Open https://" + host + "/-/user_settings/ssh_keys",
			"Paste the key and set Title: " + title,
			"Usage type: " + usage,
			"Click Add key.",
		}
	}

	steps := []string{
		"On Gitea, Forgejo or Codeberg, open https://" + host + "/user/settings/keys; on other hosts, find the SSH keys page in your settings.",
		"Add the key under SSH Keys, named " + title + ".",
	}
	if k.signing {
		steps = append(steps, "For signed commits to show as verified, click Verify next to the key and sign the token shown: echo -n '<token>' | ssh-keygen -Y sign -n gitea -f "+keys.PublicPath(k.key))
	}
	return steps
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
		cmd := exec.Command(tool[0], tool[1:]...) //nolint:gosec // fixed clipboard tools
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return nil
		}
	}
	if copied {
		return nil
	}
	return errors.New("no clipboard tool found (wl-copy, xclip, xsel or pbcopy)")
}
