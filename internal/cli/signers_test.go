package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
)

// commitAt makes a commit dated at, signed with signingKey ("" for the
// account's), and returns what Git says about its signature.
func commitAt(t *testing.T, repo string, at time.Time, signingKey string) string {
	t.Helper()
	date := at.Format(time.RFC3339)
	t.Setenv("GIT_AUTHOR_DATE", date)
	t.Setenv("GIT_COMMITTER_DATE", date)
	args := []string{"commit", "-q", "--allow-empty", "-m", "at " + date}
	if signingKey != "" {
		args = append([]string{"-c", "user.signingkey=" + signingKey}, args...)
	}
	if _, err := git.Run(repo, args...); err != nil {
		t.Fatal(err)
	}
	return showSignature(t, repo, "HEAD")
}

func showSignature(t *testing.T, repo, rev string) string {
	t.Helper()
	out, err := git.Run(repo, "log", "--show-signature", "-1", rev)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// needsKeyLifetimes skips a test that needs signatures checked at the time
// they were made: Git 2.35 passes ssh-keygen the commit's time, and OpenSSH
// 8.8 reads valid-before.
func needsKeyLifetimes(t *testing.T) {
	t.Helper()
	var major, minor int
	if _, err := fmt.Sscanf(git.Version(), "%d.%d", &major, &minor); err != nil || major < 2 || (major == 2 && minor < 35) {
		t.Skipf("Git %s doesn't check signatures at the commit's time", git.Version())
	}
	if major, minor, ok := keys.OpenSSHVersion(); !ok || major < 8 || (major == 8 && minor < 8) {
		t.Skipf("OpenSSH %d.%d can't read valid-before", major, minor)
	}
}

const good = `Good "git" signature for jane@acme.com`

// Rotating the signing key keeps older commits verified, and stops
// trusting the old key for anything signed after the rotation.
func TestOldSignaturesVerifyAfterRotatingTheKey(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_old", "jane@acme.com", "")
	s.Key("id_new", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--signing-key", "~/.ssh/id_old")
	repo := s.GitInit("repo")
	oldPub := strings.Join(strings.Fields(s.Read(".ssh/id_old.pub"))[:2], " ")

	if out := commitAt(t, repo, time.Now().Add(-time.Hour), ""); !strings.Contains(out, good) {
		t.Fatalf("commit before the rotation isn't verified:\n%s", out)
	}
	before, err := git.Run(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	before = strings.TrimSpace(before)

	s.mustRun("edit", "work", "--signing-key", "~/.ssh/id_new")
	acc := loadAccount(t, s, "work")
	if len(acc.Retired) != 1 || acc.Retired[0].Email != "jane@acme.com" || acc.Retired[0].Key != oldPub {
		t.Fatalf("retired signers = %+v", acc.Retired)
	}
	signers := s.Read(".ssh/allowed_signers")
	if want := `jane@acme.com namespaces="git",valid-before="` + acc.Retired[0].Until + `" ` + oldPub; !strings.Contains(signers, want) {
		t.Errorf("allowed_signers doesn't keep the old key until the rotation:\n%s", signers)
	}

	// Regenerating doppel's files keeps the history: it lives in the account file.
	if err := os.Remove(s.Path(".ssh/allowed_signers")); err != nil {
		t.Fatal(err)
	}
	s.mustRun("doctor", "--fix")
	if got := s.Read(".ssh/allowed_signers"); got != signers {
		t.Errorf("doctor --fix wrote\n%s\nwant\n%s", got, signers)
	}
	s.mustRun("doctor")
	if strings.Contains(s.stdout.String(), "don't match the accounts") {
		t.Errorf("doctor finds the files out of date after --fix:\n%s", s.stdout.String())
	}

	needsKeyLifetimes(t)
	if out := showSignature(t, repo, before); !strings.Contains(out, good) {
		t.Errorf("commit before the rotation isn't verified after it:\n%s", out)
	}
	if out := commitAt(t, repo, time.Now().Add(time.Hour), s.Path(".ssh/id_old.pub")); strings.Contains(out, good) || !strings.Contains(out, "No principal matched") {
		t.Errorf("the old key is still trusted after the rotation:\n%s", out)
	}
	if out := commitAt(t, repo, time.Now().Add(time.Hour), ""); !strings.Contains(out, good) {
		t.Errorf("commit with the new key isn't verified:\n%s", out)
	}
}

func TestChangingTheEmailRetiresTheOldOne(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--signing-key", "~/.ssh/id_work")
	s.Key("id_new", "jane@acme.com", "")

	// Both at once: the old pair is still found through the account file.
	s.mustRun("edit", "work", "--email", "jane@acme.io", "--signing-key", "~/.ssh/id_new")
	acc := loadAccount(t, s, "work")
	if len(acc.Retired) != 1 || acc.Retired[0].Email != "jane@acme.com" {
		t.Errorf("retired signers = %+v", acc.Retired)
	}

	// Stopping signing retires the current one too, and an unchanged save
	// retires nothing more.
	s.mustRun("edit", "work", "--no-signing")
	s.mustRun("edit", "work", "--name", "Jane D.")
	acc = loadAccount(t, s, "work")
	if len(acc.Retired) != 2 || acc.Retired[1].Email != "jane@acme.io" {
		t.Errorf("retired signers = %+v", acc.Retired)
	}
	if signers := s.Read(".ssh/allowed_signers"); strings.Count(signers, "valid-before") != 2 {
		t.Errorf("allowed_signers:\n%s", signers)
	}
}

// Deleting an account stops trusting its keys altogether, even for another
// account with the same email.
func TestRemovingAnAccountDropsItsSigners(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_a", "jane@acme.com", "")
	s.Key("id_b", "jane@acme.com", "")
	s.addAccount("old", "jane@acme.com", "--signing-key", "~/.ssh/id_a")
	s.addAccount("work", "jane@acme.com", "--signing-key", "~/.ssh/id_b")
	s.mustRun("rm", "old", "--yes")

	signers := s.Read(".ssh/allowed_signers")
	if strings.Contains(signers, strings.Fields(s.Read(".ssh/id_a.pub"))[1]) {
		t.Errorf("the deleted account's key is still trusted:\n%s", signers)
	}
	if acc := loadAccount(t, s, "work"); len(acc.Retired) != 0 {
		t.Errorf("work took on the deleted account's signer: %+v", acc.Retired)
	}
}

// A retired signer edited by hand into something that isn't one is refused
// before it reaches allowed_signers.
func TestABadRetiredSignerIsRefused(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("work", "jane@acme.com")
	s.SetConfig(accountsDir+"work.gitconfig", accounts.KeyRetired, `20260101120000 jane@acme.com,* ssh-ed25519 AAAA`)
	if stderr := s.mustFail(1, "edit", "work", "--name", "Jane"); !strings.Contains(stderr, "doppel.retiredSigner") {
		t.Errorf("edit with a bad retired signer: %s", stderr)
	}
}
