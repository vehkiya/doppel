package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
)

const greeting = "Hi jane-acme! You've successfully authenticated, but GitHub does not provide shell access."

// commitAndVerify makes a commit in repo and returns what Git says about its signature.
func commitAndVerify(t *testing.T, repo string) string {
	t.Helper()
	if _, err := git.Run(repo, "commit", "-q", "--allow-empty", "-m", "test"); err != nil {
		t.Fatal(err)
	}
	out, err := git.Run(repo, "log", "--show-signature", "-1")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSignedCommitsVerify(t *testing.T) {
	s := newSandbox(t)
	key := s.Key("id_work", "jane@acme.com", "")
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key")

	work := s.GitInit("work/api")
	if got := s.GitConfig(work, accounts.KeySSHCommand); got != "ssh -i ~/.ssh/id_work -o IdentitiesOnly=yes" {
		t.Errorf("core.sshCommand = %q", got)
	}
	if out := commitAndVerify(t, work); !strings.Contains(out, `Good "git" signature for jane@acme.com`) {
		t.Errorf("work commit isn't verified:\n%s", out)
	}
	// The default account doesn't sign, and doesn't inherit the work key.
	blog := s.GitInit("blog")
	if out := commitAndVerify(t, blog); strings.Contains(out, "signature") {
		t.Errorf("personal commit was signed:\n%s", out)
	}
	if got := s.GitConfig(blog, accounts.KeySSHCommand); got != "ssh" {
		t.Errorf("personal core.sshCommand = %q", got)
	}
	_ = key
}

func TestAllowedSignersKeepsOtherEntries(t *testing.T) {
	s := newSandbox(t)
	teammate := "bob@acme.com namespaces=\"git\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBOB\n"
	s.Write(".ssh/allowed_signers", teammate)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key")

	signers := s.Read(".ssh/allowed_signers")
	if !strings.HasPrefix(signers, teammate) || !strings.Contains(signers, "jane@acme.com namespaces=\"git\" ssh-ed25519 ") {
		t.Fatalf("allowed_signers:\n%s", signers)
	}

	s.mustRun("edit", "work", "--email", "jane@acme.io")
	signers = s.Read(".ssh/allowed_signers")
	if strings.Contains(signers, "jane@acme.com") || !strings.Contains(signers, "jane@acme.io namespaces") {
		t.Errorf("entry not updated after an email change:\n%s", signers)
	}

	s.mustRun("edit", "work", "--no-signing")
	if got := s.Read(".ssh/allowed_signers"); got != teammate {
		t.Errorf("allowed_signers after --no-signing:\n%q", got)
	}
	if got := s.GitConfig(s.GitInit("repo"), accounts.KeyCommitSign); got != "false" {
		t.Errorf("commit.gpgsign = %q after --no-signing", got)
	}

	s.mustRun("edit", "work", "--sign-with-auth-key")
	s.mustRun("uninstall", "--yes")
	if got := s.Read(".ssh/allowed_signers"); got != teammate {
		t.Errorf("allowed_signers after uninstall:\n%q", got)
	}
}

func TestUserConfiguredSignersFile(t *testing.T) {
	s := newSandbox(t)
	s.Write(".gitconfig", "[gpg \"ssh\"]\n\tallowedSignersFile = ~/team/allowed_signers\n")
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key")

	if !strings.Contains(s.Read("team/allowed_signers"), "jane@acme.com namespaces") {
		t.Error("entry not written to the configured allowed_signers file")
	}
	if s.Exists(".ssh/allowed_signers") {
		t.Error("created ~/.ssh/allowed_signers although another file is configured")
	}
	if strings.Contains(s.Read(".config/doppel/index.gitconfig"), "allowedSignersFile") {
		t.Error("the index overrides the user's allowedSignersFile")
	}
	if out := commitAndVerify(t, s.GitInit("repo")); !strings.Contains(out, `Good "git" signature`) {
		t.Errorf("commit isn't verified:\n%s", out)
	}
}

func TestSignersFileSetInAnIncludedFile(t *testing.T) {
	s := newSandbox(t)
	s.Write(".config/git/signing.inc", "[gpg \"ssh\"]\n\tallowedSignersFile = ~/team/allowed_signers\n")
	s.Write(".gitconfig", "[include]\n\tpath = ~/.config/git/signing.inc\n")
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key")

	if !strings.Contains(s.Read("team/allowed_signers"), "jane@acme.com namespaces") {
		t.Error("entry not written to the allowed_signers file set in the included file")
	}
	if s.Exists(".ssh/allowed_signers") || strings.Contains(s.Read(".config/doppel/index.gitconfig"), "allowedSignersFile") {
		t.Error("doppel used its own allowed_signers file instead of the one the user configured")
	}
	if out := commitAndVerify(t, s.GitInit("repo")); !strings.Contains(out, `Good "git" signature`) {
		t.Errorf("commit isn't verified:\n%s", out)
	}
}

func TestIndexKeepsSettingTheSignersFileItChose(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key")
	// The index now sets allowedSignersFile itself. Reading the global
	// config with its includes must not mistake that for the user's choice.
	s.mustRun("edit", "work", "--name", "Jane A. Doe")

	if !strings.Contains(s.Read(".config/doppel/index.gitconfig"), "allowedSignersFile = ~/.ssh/allowed_signers") {
		t.Errorf("index stopped pointing Git at allowed_signers:\n%s", s.Read(".config/doppel/index.gitconfig"))
	}
	if out := commitAndVerify(t, s.GitInit("repo")); !strings.Contains(out, `Good "git" signature`) {
		t.Errorf("commit isn't verified:\n%s", out)
	}
}

func TestSignOnlyTags(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key", "--sign-commits=false")
	repo := s.GitInit("repo")
	if got := s.GitConfig(repo, accounts.KeyCommitSign); got != "false" {
		t.Errorf("commit.gpgsign = %q", got)
	}
	if got := s.GitConfig(repo, accounts.KeyTagSign); got != "true" {
		t.Errorf("tag.gpgsign = %q", got)
	}
}

func TestGenerateKeys(t *testing.T) {
	s := newSandbox(t)
	args := []string{"add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--generate-auth-key", "--generate-signing-key"}
	if stderr := s.mustFail(1, args...); !strings.Contains(stderr, "needs a terminal") {
		t.Errorf("generating without a terminal: %s", stderr)
	}
	if s.Exists(".ssh/id_ed25519_work") || s.Exists(accountsDir+"work.gitconfig") {
		t.Fatal("a failed generation left files behind")
	}

	s.tty = true
	s.mustRun(args...)
	for _, f := range []string{".ssh/id_ed25519_work", ".ssh/id_ed25519_work.pub", ".ssh/id_ed25519_work_signing", ".ssh/id_ed25519_work_signing.pub"} {
		if !s.Exists(f) {
			t.Errorf("%s wasn't generated", f)
		}
	}
	if !strings.Contains(s.stderr.String(), "has no passphrase") {
		t.Errorf("no warning about the empty passphrase:\n%s", s.stderr.String())
	}
	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	if w := list[0]; w.AuthKey != "~/.ssh/id_ed25519_work" || w.SigningKey != "~/.ssh/id_ed25519_work_signing.pub" || !w.SignCommits || !w.SignTags {
		t.Errorf("account = %+v", w)
	}
	if out := commitAndVerify(t, s.GitInit("repo")); !strings.Contains(out, `Good "git" signature`) {
		t.Errorf("commit with the generated key isn't verified:\n%s", out)
	}

	// The keys exist now, so generating them again is refused.
	if stderr := s.mustFail(1, "edit", "work", "--generate-auth-key"); !strings.Contains(stderr, "already exists; use it with --auth-key") {
		t.Errorf("regenerating: %s", stderr)
	}
}

// A save that can't be staged fails before any key is generated, so the
// same command works once the cause is fixed.
func TestKeysAreGeneratedOnlyOnceTheSaveCanStage(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_personal", "jane@personal.dev", "")
	s.addAccount("personal", "jane@personal.dev", "--auth-key", "~/.ssh/id_personal", "--sign-with-auth-key")
	if err := os.Remove(s.Path(".ssh/id_personal.pub")); err != nil { // R4.4a: its signing key can't be read now
		t.Fatal(err)
	}

	s.tty = true
	args := []string{"add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--generate-auth-key"}
	if stderr := s.mustFail(1, args...); !strings.Contains(stderr, "account personal: signing key ~/.ssh/id_personal.pub") {
		t.Errorf("add with an unreadable signing key: %s", stderr)
	}
	if s.Exists(".ssh/id_ed25519_work") || s.Exists(".ssh/id_ed25519_work.pub") {
		t.Fatal("a save that couldn't stage still generated the key")
	}

	s.mustRun("edit", "personal", "--no-signing")
	s.mustRun(args...)
	if !s.Exists(".ssh/id_ed25519_work") {
		t.Error("the retry didn't generate the key")
	}
}

func TestDryRunDoesNotGenerateKeys(t *testing.T) {
	s := newSandbox(t)
	s.tty = true
	out := s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com", "--generate-auth-key", "--sign-with-auth-key", "--dry-run")
	if s.Exists(".ssh/id_ed25519_work") {
		t.Fatal("--dry-run generated a key")
	}
	for _, want := range []string{"would generate the auth key ~/.ssh/id_ed25519_work", "<the key generated at ~/.ssh/id_ed25519_work>"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output is missing %q:\n%s", want, out)
		}
	}
}

func TestKeyFlagErrors(t *testing.T) {
	s := newSandbox(t)
	key := s.Key("id_work", "jane@acme.com", "")
	if err := os.Rename(key+".pub", s.Path("elsewhere.pub")); err != nil {
		t.Fatal(err)
	}
	base := []string{"add", "work", "--name", "J", "--email", "j@x.io"}
	cases := []struct {
		flags []string
		want  string
	}{
		{[]string{"--auth-key", "~/.ssh/id_work", "--generate-auth-key"}, "either --auth-key or --generate-auth-key"},
		{[]string{"--sign-with-auth-key", "--no-signing"}, "only one of"},
		{[]string{"--sign-with-auth-key"}, "needs an auth key"},
		{[]string{"--auth-key", "~/.ssh/nope"}, "no key file at ~/.ssh/nope"},
		{[]string{"--signing-key", "~/.ssh/id_work"}, "ssh-keygen -y -f ~/.ssh/id_work > ~/.ssh/id_work.pub"},
	}
	for _, c := range cases {
		if stderr := s.mustFail(1, append(base, c.flags...)...); !strings.Contains(stderr, c.want) {
			t.Errorf("%v: stderr %q doesn't mention %q", c.flags, stderr, c.want)
		}
	}
}

func TestAgentOnlyAuthKey(t *testing.T) {
	s := newSandbox(t)
	key := s.Key("id_card", "jane@acme.com", "")
	if err := os.Remove(key); err != nil { // only the .pub stays; an agent holds the private key
		t.Fatal(err)
	}
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_card.pub")
	if got := s.GitConfig(s.GitInit("repo"), accounts.KeySSHCommand); got != "ssh -i ~/.ssh/id_card.pub -o IdentitiesOnly=yes" {
		t.Errorf("core.sshCommand = %q", got)
	}
}

func TestSharedAuthKeyWarning(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_shared", "jane@acme.com", "")
	s.addAccount("personal", "jane@personal.dev", "--auth-key", "~/.ssh/id_shared")
	s.addAccount("lab", "jane@lab.dev", "--auth-key", "~/.ssh/id_shared", "--host", "git.lab.dev")
	if strings.Contains(s.stderr.String(), "same auth key") {
		t.Errorf("warned about accounts on different hosts:\n%s", s.stderr.String())
	}
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_shared")
	if !strings.Contains(s.stderr.String(), "personal and work use the same auth key on github.com") {
		t.Errorf("no warning about the shared key:\n%s", s.stderr.String())
	}
}

func TestExport(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.Key("id_work_signing", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key", "--host", "github.com", "--host", "gitlab.acme.com")
	s.addAccount("oss", "jane@oss.dev", "--auth-key", "~/.ssh/id_work", "--signing-key", "~/.ssh/id_work_signing", "--host", "codeberg.org")
	s.addAccount("empty", "jane@empty.dev")
	pub := strings.TrimSpace(s.Read(".ssh/id_work.pub"))

	out := s.mustRun("export", "work")
	for _, want := range []string{
		pub, "Auth and signing key for work",
		"https://github.com/settings/ssh/new", "add it a second time with Key type: Signing Key",
		"https://gitlab.acme.com/-/user_settings/ssh_keys", "Usage type: Authentication & Signing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export work is missing %q:\n%s", want, out)
		}
	}
	if len(s.copied) != 1 || s.copied[0] != pub {
		t.Errorf("copied %q, want the key once", s.copied)
	}

	s.copied = nil
	out = s.mustRun("export", "oss")
	if len(s.copied) != 0 || !strings.Contains(out, "nothing was copied") {
		t.Errorf("two keys: copied %q\n%s", s.copied, out)
	}
	if !strings.Contains(out, "codeberg.org/user/settings/keys") || !strings.Contains(out, "ssh-keygen -Y sign -n gitea") {
		t.Errorf("export oss has no Forgejo steps:\n%s", out)
	}
	s.mustRun("export", "oss", "--signing")
	if len(s.copied) != 1 || s.copied[0] != strings.TrimSpace(s.Read(".ssh/id_work_signing.pub")) {
		t.Errorf("--signing copied %q", s.copied)
	}
	if stderr := s.mustFail(1, "export", "empty"); !strings.Contains(stderr, "no keys yet") {
		t.Errorf("export without keys: %s", stderr)
	}
}

func TestTestCommand(t *testing.T) {
	s := newSandbox(t)
	s.FakeSSH(greeting)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key", "--github-user", "jane-acme")

	out := s.mustRun("test", "work")
	for _, want := range []string{"github.com   logged in as jane-acme", "signed and verified as jane@acme.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("test output is missing %q:\n%s", want, out)
		}
	}
	if calls := s.Read("ssh-calls"); !strings.Contains(calls, "-i "+s.Path(".ssh/id_work")+" -o IdentitiesOnly=yes git@github.com") {
		t.Errorf("ssh called with %q", calls)
	}

	s.mustRun("edit", "work", "--github-user", "jane-at-acme")
	s.mustFail(1, "test", "work")
	if !strings.Contains(s.stdout.String(), "logged in as jane-acme, but the account's GitHub user is jane-at-acme") {
		t.Errorf("no mismatch reported:\n%s", s.stdout.String())
	}

	// A key with a passphrase can't be used without a terminal or an agent.
	s.Key("id_locked", "jane@acme.com", "a passphrase")
	s.mustRun("edit", "work", "--auth-key", "~/.ssh/id_locked", "--sign-with-auth-key", "--github-user", "jane-acme")
	s.mustFail(1, "test", "work")
	if !strings.Contains(s.stdout.String(), "isn't loaded in your agent; load it with `ssh-add ~/.ssh/id_locked`") {
		t.Errorf("locked key not explained:\n%s", s.stdout.String())
	}
}

func TestWhoamiLogin(t *testing.T) {
	s := newSandbox(t)
	s.FakeSSH(greeting)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work")
	repo := s.GitInit("repo")

	for url, want := range map[string]string{
		"git@github.com:acme/api.git":          "Login     ✓ github.com: logged in as jane-acme",
		"ssh://git@github.com:22/acme/api.git": "Login     ✓ github.com: logged in as jane-acme",
		"https://github.com/acme/api.git":      "↳ switch it to SSH: git remote set-url origin git@github.com:acme/api.git",
		"/srv/git/api.git":                     "the remote uses a local path",
	} {
		_, _ = git.Run(repo, "remote", "remove", "origin")
		if _, err := git.Run(repo, "remote", "add", "origin", url); err != nil {
			t.Fatal(err)
		}
		if out := s.mustRun("whoami", repo); !strings.Contains(out, want) {
			t.Errorf("remote %s: whoami is missing %q:\n%s", url, want, out)
		}
	}
	if out := s.mustRun("whoami", repo); !strings.Contains(out, "Auth key  ~/.ssh/id_work (no passphrase)") {
		t.Errorf("auth key status missing:\n%s", out)
	}
	// A file in the repo is answered for the repo it's in, login included.
	_, _ = git.Run(repo, "remote", "set-url", "origin", "git@github.com:acme/api.git")
	s.Write("repo/README.md", "hi\n")
	if out := s.mustRun("whoami", filepath.Join(repo, "README.md")); !strings.Contains(out, "Login     ✓ github.com") {
		t.Errorf("whoami on a file doesn't log in:\n%s", out)
	}

	_ = os.Remove(s.Path("ssh-calls"))
	_, _ = git.Run(repo, "remote", "set-url", "origin", "git@github.com:acme/api.git")
	if out := s.mustRun("whoami", repo, "--offline"); strings.Contains(out, "Login") || s.Exists("ssh-calls") {
		t.Errorf("--offline still logged in:\n%s", out)
	}
}

func TestLsShowsKeys(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key")
	out := s.mustRun("ls")
	rows := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(strings.TrimPrefix(line, "★")); len(fields) > 2 {
			rows[fields[0]] = fields[1:]
		}
	}
	if got := strings.Join(rows["work"], " "); got != "jane@acme.com auth, signing —" {
		t.Errorf("work row = %q\n%s", got, out)
	}
	if got := strings.Join(rows["personal"], " "); got != "jane@personal.dev — —" {
		t.Errorf("personal row = %q\n%s", got, out)
	}
}

func TestEditWithOnlyKeyFlags(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com")
	s.mustRun("edit", "work", "--auth-key", "~/.ssh/id_work")
	s.mustRun("edit", "work", "--auth-key", "")
	if got := s.GitConfig(s.GitInit("repo"), accounts.KeySSHCommand); got != "ssh" {
		t.Errorf("core.sshCommand = %q after --auth-key \"\"", got)
	}
}
