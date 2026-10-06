package keys_test

import (
	"runtime"
	"testing"

	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/testenv"
)

func TestUsesKeychain(t *testing.T) {
	cases := []struct {
		name, config string
		files        map[string]string // other files under ~/.ssh
		host         string
		want         bool
	}{
		{"every host", "Host *\n  UseKeychain yes\n", nil, "github.com", true},
		{"no setting", "Host *\n  AddKeysToAgent yes\n", nil, "github.com", false},
		{"before any Host", "UseKeychain=yes\nHost x\n", nil, "github.com", true},
		{"another host only", "Host gitlab.com\n  UseKeychain yes\n", nil, "github.com", false},
		{"wildcard", "Host *.com\n\tusekeychain true\n", nil, "github.com", true},
		{"ignores case", "HOST GitHub.com\n  UseKeychain yes\n", nil, "github.com", true},
		{"several patterns", "Host gitlab.com github.com\n  UseKeychain yes\n", nil, "github.com", true},
		{"negated", "Host * !github.com\n  UseKeychain yes\n", nil, "github.com", false},
		{"first value wins", "Host github.com\n  UseKeychain no\nHost *\n  UseKeychain yes\n", nil, "github.com", false},
		{"comment", "Host *\n  # UseKeychain yes\n", nil, "github.com", false},
		{"quoted", "Host \"github.com\"\n  UseKeychain \"yes\"\n", nil, "github.com", true},
		{"Match is taken to apply", "Match exec \"true\"\n  UseKeychain yes\n", nil, "github.com", true},
		{"include", "Include config.d/*\n", map[string]string{"config.d/mac": "Host *\n  UseKeychain yes\n"}, "github.com", true},
		{"include from home", "Include ~/.ssh/mac.conf\n", map[string]string{"mac.conf": "UseKeychain yes\n"}, "github.com", true},
		{"include in another host's block", "Host gitlab.com\n  Include mac.conf\n", map[string]string{"mac.conf": "UseKeychain yes\n"}, "github.com", false},
		{"include in this host's block", "Host github.com\n  Include mac.conf\n", map[string]string{"mac.conf": "UseKeychain yes\n"}, "github.com", true},
		{"include loop", "Include config\n", nil, "github.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testenv.New(t)
			s.Write(".ssh/config", c.config)
			for name, content := range c.files {
				s.Write(".ssh/"+name, content)
			}
			if got := keys.UsesKeychain(s.Path(".ssh/config"), c.host); got != c.want {
				t.Errorf("UsesKeychain = %v, want %v", got, c.want)
			}
		})
	}
	s := testenv.New(t)
	if keys.UsesKeychain(s.Path(".ssh/config"), "github.com") {
		t.Error("UsesKeychain without a config = true")
	}
}

// Real ssh reads AddKeysToAgent from the config, as -G prints it.
func TestAddsKeysToAgent(t *testing.T) {
	s := testenv.New(t)
	cfg := s.Path(".ssh/config")
	s.Write(".ssh/config", "Host github.com\n  AddKeysToAgent yes\nHost gitlab.com\n  AddKeysToAgent confirm 5m\nHost codeberg.org\n  AddKeysToAgent no\n")
	for host, want := range map[string]bool{"github.com": true, "gitlab.com": true, "codeberg.org": false, "example.com": false} {
		if got := keys.AddsKeysToAgent(cfg, host); got != want {
			t.Errorf("AddsKeysToAgent(%s) = %v, want %v", host, got, want)
		}
	}
	if keys.AddsKeysToAgent(s.Path(".ssh/missing"), "github.com") {
		t.Error("AddsKeysToAgent without a config = true")
	}
}

func TestKeychainSupported(t *testing.T) {
	testenv.New(t)
	// Only Apple's ssh knows UseKeychain. A Mac may have another ssh first
	// on PATH, so only other systems have a known answer.
	if runtime.GOOS != "darwin" && keys.KeychainSupported() {
		t.Error("KeychainSupported = true on " + runtime.GOOS)
	}
	s := testenv.New(t)
	s.FakeCommand("ssh", `case "$*" in *UseKeychain=yes*) exit 0 ;; esac
exit 255`)
	if !keys.KeychainSupported() {
		t.Error("KeychainSupported = false with an ssh that accepts UseKeychain")
	}
}
