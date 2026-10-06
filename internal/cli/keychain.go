package cli

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/vehkiya/doppel/internal/keys"
)

// macKeychain reports whether passphrases can be kept in the macOS
// Keychain: this is a Mac, and the ssh on PATH is Apple's. It's asked once
// per command.
func (a *app) macKeychain() bool {
	if a.env.GOOS != "darwin" {
		return false
	}
	if a.appleSSH == nil {
		ok := keys.KeychainSupported()
		a.appleSSH = &ok
	}
	return *a.appleSSH
}

// loadCommand is the command that loads key into the agent so nothing asks
// for its passphrase. On a Mac it also keeps the passphrase in the Keychain,
// so the key comes back after logging in again.
func (a *app) loadCommand(key keys.Ref) string {
	if a.macKeychain() {
		return "ssh-add --apple-use-keychain " + key.PrivatePath()
	}
	return "ssh-add " + key.PrivatePath()
}

// rememberPassphrases helps Git use newly generated keys without asking for
// their passphrases. On a Mac it offers to keep each one in the Keychain;
// elsewhere it says how to load them into the agent.
func (a *app) rememberPassphrases(pending []pendingKey, assumeYes bool) {
	var locked []string
	for _, k := range pending {
		if keys.CheckProtection(keys.Ref(k.path)) == keys.Encrypted {
			locked = append(locked, a.env.Shorten(k.path))
		}
	}
	if len(locked) == 0 {
		return
	}
	if !a.macKeychain() {
		a.notef("Load new keys into your agent so Git doesn't ask for the passphrase on every commit: ssh-add <key>")
		return
	}
	for _, key := range locked {
		keep := true
		if !assumeYes {
			err := a.runForm(huh.NewForm(huh.NewGroup(
				huh.NewConfirm().Title(fmt.Sprintf("Keep the passphrase of %s in your macOS Keychain?", key)).
					Description("Then Git doesn't ask for it. ssh-add asks for it once more to save it").
					Affirmative("Yes").Negative("No").Value(&keep),
			)))
			keep = keep && err == nil
		}
		if !keep {
			a.notef("To keep it later: %s", a.loadCommand(keys.Ref(key)))
			continue
		}
		if err := a.keychain(a.env.Expand(key)); err != nil {
			a.warnf("Couldn't keep the passphrase in the Keychain (%v). Try again with: %s", err, a.loadCommand(keys.Ref(key)))
			continue
		}
		a.successf("The passphrase of %s is in your Keychain, and the key is in your agent", key)
	}
	a.notef("ssh reads passphrases from the Keychain for hosts with `UseKeychain yes` and `AddKeysToAgent yes` in ~/.ssh/config; `doppel doctor` checks them. Signing only uses keys in the agent: after logging in, `ssh-add --apple-load-keychain` puts them back.")
}

// keychainHints says how to stop macOS asking for passphrases in the
// terminal: for each of keyPaths that has one and isn't in the agent, the
// command that keeps it in the Keychain.
func (a *app) keychainHints(refs ...keys.Ref) {
	if !a.macKeychain() {
		return
	}
	seen := map[string]bool{}
	for _, key := range refs {
		private := key.PrivatePath()
		if private == "" || seen[private] {
			continue
		}
		seen[private] = true
		path := key.Map(a.env.Expand)
		if keys.CheckProtection(path) != keys.Encrypted {
			continue
		}
		if loaded, _ := keys.InAgent(path); !loaded {
			a.notef("  %s isn't in your agent, so macOS asks for its passphrase. Keep it in the Keychain: %s", private, a.loadCommand(key))
		}
	}
}
