package cli

import (
	"slices"
	"strings"
	"testing"
)

// fakeAppleSSH stands in for Apple's ssh: it accepts UseKeychain, answers
// `ssh -G -F <config> <host>` with whether the config sets AddKeysToAgent,
// and greets every login as GitHub does.
func (s *sandbox) fakeAppleSSH() {
	s.T.Helper()
	s.FakeCommand("ssh", `echo "$@" >> "$HOME/ssh-calls"
case "$*" in
*UseKeychain=yes*) exit 0 ;;
-G*)
	if grep -qi '^ *AddKeysToAgent yes' "$3" 2>/dev/null; then echo "addkeystoagent true"; else echo "addkeystoagent false"; fi
	exit 0 ;;
esac
echo "`+greeting+`" >&2
exit 1`)
}

func TestKeychainOfferAfterGenerating(t *testing.T) {
	s := newSandbox(t)
	s.fakeAppleSSH()
	s.goos, s.tty, s.passphrase = "darwin", true, "a passphrase"
	key := s.Path(".ssh/id_ed25519_work")

	s.stdin = script("") // keep it in the Keychain: the default
	out := s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--generate-auth-key")
	if !slices.Equal(s.keychained, []string{key}) {
		t.Fatalf("keychained %q, want the new key", s.keychained)
	}
	for _, want := range []string{"Keep the passphrase of ~/.ssh/id_ed25519_work in your macOS Keychain?", "is in your Keychain", "ssh-add --apple-load-keychain"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}

	// Declining says how to do it later.
	s.keychained = nil
	s.stdin = script("n")
	out = s.mustRun("edit", "work", "--generate-signing-key")
	if len(s.keychained) != 0 || !strings.Contains(out, "To keep it later: ssh-add --apple-use-keychain ~/.ssh/id_ed25519_work_signing") {
		t.Errorf("declined: keychained %q\n%s", s.keychained, out)
	}

	// --yes doesn't ask.
	s.stdin = ""
	s.mustRun("add", "lab", "--name", "Jane Doe", "--email", "jane@lab.dev", "--host", "git.lab.dev", "--generate-auth-key", "--yes")
	if !slices.Equal(s.keychained, []string{s.Path(".ssh/id_ed25519_lab")}) {
		t.Errorf("--yes: keychained %q", s.keychained)
	}
}

func TestNoKeychainOfferWhereItCantHelp(t *testing.T) {
	cases := []struct {
		name, goos, passphrase string
		appleSSH               bool
		note                   string
	}{
		{"Linux", "linux", "a passphrase", true, "ssh-add <key>"},
		{"another ssh on macOS", "darwin", "a passphrase", false, "ssh-add <key>"},
		{"no passphrase", "darwin", "", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t)
			if c.appleSSH {
				s.fakeAppleSSH()
			} else {
				s.FakeSSH("unused")
			}
			s.goos, s.tty, s.passphrase = c.goos, true, c.passphrase
			out := s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--generate-auth-key")
			if len(s.keychained) != 0 || strings.Contains(out, "Keychain") {
				t.Errorf("offered the Keychain: keychained %q\n%s", s.keychained, out)
			}
			if c.note != "" && !strings.Contains(out, c.note) {
				t.Errorf("output is missing %q:\n%s", c.note, out)
			}
		})
	}
}

func TestTestSuggestsTheKeychain(t *testing.T) {
	s := newSandbox(t)
	s.fakeAppleSSH()
	s.goos = "darwin"
	s.Key("id_locked", "jane@acme.com", "a passphrase")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_locked", "--github-user", "jane-acme")

	// Without a terminal, the failed row names the command.
	s.mustFail(1, "test", "work")
	if !strings.Contains(s.stdout.String(), "load it with `ssh-add --apple-use-keychain ~/.ssh/id_locked`") {
		t.Errorf("batch test doesn't suggest the Keychain:\n%s", s.stdout.String())
	}
	// With one, ssh asks for the passphrase, and test says how to stop that.
	s.tty = true
	out := s.mustRun("test", "work")
	if !strings.Contains(out, "~/.ssh/id_locked isn't in your agent, so macOS asks for its passphrase. Keep it in the Keychain: ssh-add --apple-use-keychain ~/.ssh/id_locked") {
		t.Errorf("test doesn't suggest the Keychain:\n%s", out)
	}
	s.goos = "linux"
	if out := s.mustRun("test", "work"); strings.Contains(out, "Keychain") {
		t.Errorf("suggested the Keychain on Linux:\n%s", out)
	}
}

func TestDoctorChecksTheKeychain(t *testing.T) {
	s := newSandbox(t)
	s.fakeAppleSSH()
	s.goos = "darwin"
	s.Key("id_locked", "jane@acme.com", "a passphrase")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_locked")

	out := s.mustRun("doctor")
	for _, want := range []string{
		"ssh is Apple's",
		"~/.ssh/config doesn't set UseKeychain yes or AddKeysToAgent yes for github.com, so macOS asks for the passphrase of ~/.ssh/id_locked in the terminal",
		"Add UseKeychain yes and AddKeysToAgent yes under `Host github.com` (or `Host *`) in ~/.ssh/config, then run `ssh-add --apple-use-keychain ~/.ssh/id_locked` once",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor is missing %q:\n%s", want, out)
		}
	}

	s.Write(".ssh/config", "Host *\n  UseKeychain yes\n")
	if out := s.mustRun("doctor"); !strings.Contains(out, "doesn't set AddKeysToAgent for github.com, so ssh doesn't load ~/.ssh/id_locked into the agent") {
		t.Errorf("doctor doesn't name the missing AddKeysToAgent:\n%s", out)
	}

	s.Write(".ssh/config", "Host *\n  UseKeychain yes\n  AddKeysToAgent yes\n")
	if out := s.mustRun("doctor"); strings.Contains(out, "~/.ssh/config doesn't set") {
		t.Errorf("doctor warns with both set:\n%s", out)
	}

	// Another ssh can't use the Keychain at all.
	s.FakeSSH("unused")
	out = s.mustRun("doctor")
	if !strings.Contains(out, "isn't Apple's, so it can't keep passphrases in the Keychain") || strings.Contains(out, "~/.ssh/config doesn't set") {
		t.Errorf("doctor with another ssh:\n%s", out)
	}

	s.goos = "linux"
	if out := s.mustRun("doctor"); strings.Contains(out, "Keychain") {
		t.Errorf("doctor mentions the Keychain on Linux:\n%s", out)
	}
}
