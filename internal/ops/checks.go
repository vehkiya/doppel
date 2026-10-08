package ops

import (
	"fmt"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/github"
	"github.com/vehkiya/doppel/internal/keys"
)

// Check is the outcome of trying one of an account's keys for real.
type Check struct {
	Label   string // the host logged in to, or "signing"
	OK      bool
	Skipped bool // there was nothing to try, such as signing for an account that doesn't sign
	Detail  string
	Fix     string // a command that fixes what failed or was skipped, if there is one
}

// Test logs in to each of acc's hosts and signs a test message, verifying
// it against signers as Git would. It reports each check as it finishes.
// Checks run non-interactively without prompting for passphrases. failed is
// true when any check failed.
func Test(ctx Context, acc *accounts.Account, signers string, report func(Check)) (failed bool) {
	for _, host := range acc.Hosts {
		if acc.AllowsSSH() {
			c := Login(ctx, acc, host)
			report(c)
			failed = failed || !c.OK
		}
		if acc.AllowsHTTPS() {
			c := CheckHTTPS(ctx, acc, host)
			report(c)
			failed = failed || !c.OK
		}
	}
	c := Sign(ctx, acc, signers)
	report(c)
	return failed || (!c.OK && !c.Skipped)
}

// CheckHTTPS verifies HTTPS authentication configuration for host.
func CheckHTTPS(ctx Context, acc *accounts.Account, host string) Check {
	label := host
	if acc.AllowsSSH() {
		label += " (https)"
	}
	c := Check{Label: label}
	user := acc.EffectiveHTTPSUser()
	if user == "" {
		c.OK, c.Detail = true, "credential helper default"
		return c
	}
	if ctx.GitHub.Is(host) {
		accs, err := github.Accounts(host)
		if err == nil && len(accs) > 0 {
			found := false
			for _, a := range accs {
				if strings.EqualFold(a.Login, user) {
					found = true
					break
				}
			}
			if found {
				c.OK, c.Detail = true, "signed in with gh as "+user
				return c
			}
			c.Detail = fmt.Sprintf("gh is not signed in as %s on %s", user, host)
			c.Fix = fmt.Sprintf("gh auth login -h %s", host)
			return c
		}
	}
	c.OK, c.Detail = true, "configured for "+user
	return c
}

// Login logs in to host with acc's auth key in batch mode and says how it went.
// On GitHub it also checks the key logs in as the account's GitHub user.
// Nothing asks for anything, such as a passphrase.
func Login(ctx Context, acc *accounts.Account, host string) Check {
	c := Check{Label: host}
	if c.Detail = needsPassphrase(ctx, acc.AuthKey); c.Detail != "" {
		return c
	}
	res := keys.Login(host, acc.AuthKey.Map(ctx.Env.Expand), true)
	switch {
	case !res.Accepted:
		c.Detail = res.Problem
	case acc.GitHubUser != "" && ctx.GitHub.Is(host) && !strings.EqualFold(res.User, acc.GitHubUser):
		c.Detail = fmt.Sprintf("logged in as %s, but the account's GitHub user is %s", res.User, acc.GitHubUser)
	default:
		c.OK, c.Detail = true, "logged in as "+res.User
	}
	return c
}

// Sign signs a test message with acc's signing key and verifies it against
// the allowed_signers file signers in batch mode, as Git does.
func Sign(ctx Context, acc *accounts.Account, signers string) Check {
	c := Check{Label: "signing"}
	switch {
	case acc.SigningKey == "":
		c.Skipped, c.Detail = true, "off"
	case needsPassphrase(ctx, acc.SigningKey) != "":
		c.Detail = needsPassphrase(ctx, acc.SigningKey)
	default:
		if err := keys.SignCheck(acc.SigningKey.Map(ctx.Env.Expand), acc.Email, signers); err != nil {
			c.Detail = err.Error()
		} else {
			c.OK, c.Detail = true, "signed and verified as "+acc.Email
		}
	}
	return c
}

// needsPassphrase explains why a key can't be used when nothing may ask
// for its passphrase: it has one, and ssh-agent doesn't hold it.
// It returns "" when the key can be used.
func needsPassphrase(ctx Context, key keys.Ref) string {
	if key == "" {
		return ""
	}
	path := key.Map(ctx.Env.Expand)
	if keys.CheckProtection(path) != keys.Encrypted {
		return ""
	}
	if loaded, _ := keys.InAgent(path); loaded {
		return ""
	}
	return "the key has a passphrase and isn't loaded in your agent; load it with `" + keys.LoadCommand(key, ctx.keychain()) + "`"
}

// KeychainHints says, on a Mac with Apple's ssh, how to stop macOS asking
// for passphrases in the terminal: for each key with a passphrase that isn't
// in the agent, the command that keeps it in the Keychain.
func KeychainHints(ctx Context, refs ...keys.Ref) []string {
	if !ctx.keychain() {
		return nil
	}
	var hints []string
	seen := map[string]bool{}
	for _, key := range refs {
		private := key.PrivatePath()
		if private == "" || seen[private] {
			continue
		}
		seen[private] = true
		path := key.Map(ctx.Env.Expand)
		if keys.CheckProtection(path) != keys.Encrypted {
			continue
		}
		if loaded, _ := keys.InAgent(path); !loaded {
			hints = append(hints, fmt.Sprintf("%s isn't in your agent, so macOS asks for its passphrase. Keep it in the Keychain: %s",
				private, keys.LoadCommand(key, true)))
		}
	}
	return hints
}

// KeyStatus describes how a key is kept, such as ["passphrase", "in agent"].
func KeyStatus(ctx Context, key keys.Ref) []string {
	path := key.Map(ctx.Env.Expand)
	words := []string{keys.CheckProtection(path).String()}
	if loaded, running := keys.InAgent(path); running && loaded {
		words = append(words, "in agent")
	}
	return words
}
