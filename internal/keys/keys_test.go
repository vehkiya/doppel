package keys_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/proc"
	"github.com/vehkiya/doppel/internal/testenv"
)

func TestReadPublic(t *testing.T) {
	s := testenv.New(t)
	key := s.Key("id_work", "jane@acme.com", "")
	line, err := keys.ReadPublicLine(key)
	if err != nil || !strings.HasPrefix(line, "ssh-ed25519 ") || !strings.HasSuffix(line, " jane@acme.com") {
		t.Fatalf("ReadPublicLine = %q, %v", line, err)
	}
	pub, err := keys.ReadPublic(key + ".pub")
	if err != nil || pub != strings.TrimSuffix(line, " jane@acme.com") {
		t.Errorf("ReadPublic(.pub) = %q, %v; want the key without its comment", pub, err)
	}
	if lit, err := keys.ReadPublic(keys.LiteralPrefix + line); err != nil || lit != pub {
		t.Errorf("ReadPublic(literal) = %q, %v", lit, err)
	}

	s.Write("not-a-key.pub", "hello world\n")
	for _, bad := range []string{s.Path("not-a-key.pub"), s.Path("missing"), keys.LiteralPrefix + "ssh-ed25519 !!!"} {
		if _, err := keys.ReadPublic(bad); err == nil {
			t.Errorf("ReadPublic(%q) succeeded", bad)
		}
	}
}

func TestFingerprintMatchesOpenSSH(t *testing.T) {
	s := testenv.New(t)
	key := s.Key("id_work", "jane@acme.com", "")
	out, err := exec.Command("ssh-keygen", "-lf", key+".pub").Output() //nolint:gosec // OpenSSH's own fingerprint of the key the test just made
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(out))[1]
	if got, err := keys.Fingerprint(key); err != nil || got != want {
		t.Errorf("Fingerprint = %q, %v; ssh-keygen says %q", got, err, want)
	}
}

func TestCheckProtection(t *testing.T) {
	s := testenv.New(t)
	plain := s.Key("plain", "j@x.io", "")
	locked := s.Key("locked", "j@x.io", "a passphrase")
	agentOnly := s.Path(".ssh/agent_only.pub")
	if err := os.Rename(s.Key("agent_only", "j@x.io", "")+".pub", agentOnly); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(strings.TrimSuffix(agentOnly, ".pub"))

	cases := map[string]keys.Protection{
		plain:                                 keys.Unencrypted,
		locked:                                keys.Encrypted,
		agentOnly:                             keys.AgentOnly,
		strings.TrimSuffix(agentOnly, ".pub"): keys.AgentOnly, // only the .pub is left
		s.Path(".ssh/missing"):                keys.Unknown,
	}
	for key, want := range cases {
		if got := keys.CheckProtection(key); got != want {
			t.Errorf("CheckProtection(%s) = %v, want %v", filepath.Base(key), got, want)
		}
	}
}

func TestGenerateNeverOverwrites(t *testing.T) {
	s := testenv.New(t)
	path := keys.DefaultPath(s.Path(".ssh"), "work", false)
	if path != s.Path(".ssh/id_ed25519_work") {
		t.Errorf("DefaultPath = %s", path)
	}
	if got := keys.DefaultPath(s.Path(".ssh"), "work", true); got != s.Path(".ssh/id_ed25519_work_signing") {
		t.Errorf("DefaultPath(signing) = %s", got)
	}
	empty := ""
	if err := keys.Generate(path, "j@x.io", &empty, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	before := s.Read(".ssh/id_ed25519_work")
	if err := keys.Generate(path, "j@x.io", &empty, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Errorf("second Generate: %v", err)
	}
	if s.Read(".ssh/id_ed25519_work") != before {
		t.Error("the existing key changed")
	}
}

func TestParseGreeting(t *testing.T) {
	cases := map[string]string{
		"Hi jane-acme! You've successfully authenticated, but GitHub does not provide shell access.": "jane-acme",
		"Welcome to GitLab, @jane!": "jane",
		"Hi there, jane! You've successfully authenticated with the key named work, but Forgejo does not provide shell access.": "jane",
		"authenticated via ssh key.\n\nYou can use git to connect to Bitbucket. Shell access is disabled.\nlogged in as jane.":  "jane",
	}
	for reply, want := range cases {
		if got, ok := keys.ParseGreeting(reply); !ok || got != want {
			t.Errorf("ParseGreeting(%q) = %q, %v; want %q", reply, got, ok, want)
		}
	}
	if _, ok := keys.ParseGreeting("git@github.com: Permission denied (publickey)."); ok {
		t.Error("a rejected login parsed as a greeting")
	}
}

func TestLogin(t *testing.T) {
	cases := []struct {
		reply      string
		accepted   bool
		user, want string
	}{
		{"Hi jane! You've successfully authenticated, but GitHub does not provide shell access.", true, "jane", ""},
		{"git@github.com: Permission denied (publickey).", false, "", "rejected the key"},
		{"Host key verification failed.", false, "", "isn't trusted yet"},
		{"ssh: Could not resolve hostname github.com: Name or service not known", false, "", "couldn't reach"},
		{"something else entirely", false, "", "unexpected reply: something else entirely"},
	}
	for _, c := range cases {
		s := testenv.New(t)
		s.FakeSSH(c.reply)
		res := keys.Login("github.com", "/keys/id_work", true)
		if res.Accepted != c.accepted || res.User != c.user || !strings.Contains(res.Problem, c.want) {
			t.Errorf("reply %q: got %+v", c.reply, res)
		}
	}

	s := testenv.New(t)
	s.FakeSSH("Hi jane! You've successfully authenticated")
	keys.Login("github.com", "/keys/id_work", true)
	keys.Login("gitlab.com", "", false)
	calls := strings.Split(strings.TrimSpace(s.Read("ssh-calls")), "\n")
	if !strings.Contains(calls[0], "-o BatchMode=yes -i /keys/id_work -o IdentitiesOnly=yes git@github.com") {
		t.Errorf("ssh called with %q", calls[0])
	}
	if strings.Contains(calls[1], "BatchMode") || strings.Contains(calls[1], "-i ") || !strings.HasSuffix(calls[1], "git@gitlab.com") {
		t.Errorf("ssh without a key or batch mode called with %q", calls[1])
	}
}

func TestSignCheck(t *testing.T) {
	s := testenv.New(t)
	key := s.Key("id_work", "jane@acme.com", "")
	pub, err := keys.ReadPublic(key)
	if err != nil {
		t.Fatal(err)
	}
	s.Write("allowed_signers", "jane@acme.com namespaces=\"git\" "+pub+"\n")
	signers := s.Path("allowed_signers")

	if err := keys.SignCheck(key+".pub", "jane@acme.com", signers); err != nil {
		t.Errorf("SignCheck: %v", err)
	}
	if err := keys.SignCheck(key+".pub", "someone@else.com", signers); err == nil || !strings.Contains(err.Error(), "couldn't verify") {
		t.Errorf("SignCheck for an email that isn't trusted: %v", err)
	}
	// With a passphrase and nothing to ask for it, signing fails rather than waiting.
	locked := s.Key("id_locked", "jane@acme.com", "a passphrase")
	if err := keys.SignCheck(locked+".pub", "jane@acme.com", signers); err == nil || !strings.Contains(err.Error(), "couldn't sign") {
		t.Errorf("SignCheck with a locked key: %v", err)
	}
}

func TestToolsThatHangAreStopped(t *testing.T) {
	s := testenv.New(t)
	key := s.Key("id_work", "jane@acme.com", "")
	old := proc.Local
	proc.Local = 200 * time.Millisecond
	t.Cleanup(func() { proc.Local = old })
	oldNet := proc.Network
	proc.Network = 200 * time.Millisecond
	t.Cleanup(func() { proc.Network = oldNet })
	// An agent that stopped answering, and a host that accepts the
	// connection but never replies.
	s.FakeCommand("ssh-add", "sleep 30")
	s.FakeCommand("ssh", "sleep 30")

	start := time.Now()
	if loaded, running := keys.InAgent(key); loaded || running {
		t.Errorf("InAgent = %v, %v; want no answer from a hung agent", loaded, running)
	}
	res := keys.Login("github.com", key, true)
	if res.Accepted || !strings.Contains(res.Problem, "ssh didn't finish within 200ms") {
		t.Errorf("Login = %+v, want it stopped with a message naming ssh", res)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("took %s; the tools weren't stopped", elapsed)
	}
}
