package cli

import (
	"os"
	"strings"
	"testing"
)

// fakeGH stands in for gh. Its state lives in files in the sandbox:
// gh-users (signed-in users, one per line), gh-scopes-<user>, gh-active,
// and gh-keys-<user>-<kind> (the keys GitHub has). Calls go to gh-calls.
const fakeGH = `echo "$@" >> "$HOME/gh-calls"
u="${GH_TOKEN#token-}"
case "$1 $2" in
"auth token")
  grep -qx "$6" "$HOME/gh-users" 2>/dev/null || { echo "no oauth token found for $6" >&2; exit 1; }
  echo "token-$6" ;;
"api -i")
  printf 'HTTP/2.0 200 OK\nX-Oauth-Scopes: %s\n\n{"login":"%s"}\n' "$(cat "$HOME/gh-scopes-$u" 2>/dev/null)" "$u" ;;
"api --paginate")
  case "$3" in user/keys) kind=authentication ;; *) kind=signing ;; esac
  cat "$HOME/gh-keys-$u-$kind" 2>/dev/null || true ;;
"api user")
  cat "$HOME/gh-active" ;;
"ssh-key add")
  cut -d' ' -f1,2 "$3" >> "$HOME/gh-keys-$u-$7" ;;
*)
  echo "fake gh: unexpected $*" >&2; exit 2 ;;
esac`

// githubSandbox has a work account whose one key logs in and signs, and a
// fake gh signed in as its GitHub user with every scope it needs.
func githubSandbox(t *testing.T) *sandbox {
	t.Helper()
	s := newSandbox(t)
	s.FakeCommand("gh", fakeGH)
	s.Write("gh-users", "jane-acme\n")
	s.Write("gh-scopes-jane-acme", "admin:public_key, admin:ssh_signing_key, repo")
	s.Write("gh-active", "jane-acme\n")
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--sign-with-auth-key", "--github-user", "jane-acme")
	return s
}

func TestUploadAddsKeys(t *testing.T) {
	s := githubSandbox(t)
	pub := strings.Join(strings.Fields(s.Read(".ssh/id_work.pub"))[:2], " ")

	out := s.mustRun("upload", "work")
	for _, want := range []string{"Added ~/.ssh/id_work.pub to jane-acme's authentication keys", "Added ~/.ssh/id_work.pub to jane-acme's signing keys"} {
		if !strings.Contains(out, want) {
			t.Errorf("upload output is missing %q:\n%s", want, out)
		}
	}
	for _, kind := range []string{"authentication", "signing"} {
		if got := strings.TrimSpace(s.Read("gh-keys-jane-acme-" + kind)); got != pub {
			t.Errorf("GitHub's %s keys = %q, want the work key", kind, got)
		}
	}
	if calls := s.Read("gh-calls"); !strings.Contains(calls, "--title doppel: work (") {
		t.Errorf("keys weren't titled after the account:\n%s", calls)
	}

	// Uploading again finds them already there.
	out = s.mustRun("upload", "work")
	if strings.Count(out, "is already one of jane-acme's") != 2 || strings.Contains(out, "Added") {
		t.Errorf("second upload:\n%s", out)
	}
}

func TestUploadOnlyTheSigningKey(t *testing.T) {
	s := githubSandbox(t)
	s.mustRun("upload", "work", "--signing")
	if s.Exists("gh-keys-jane-acme-authentication") || !s.Exists("gh-keys-jane-acme-signing") {
		t.Error("--signing uploaded the wrong kinds of key")
	}
}

func TestUploadWithMissingScopes(t *testing.T) {
	s := githubSandbox(t)
	s.Write("gh-scopes-jane-acme", "repo, read:org")
	stderr := s.mustFail(1, "upload", "work")
	if !strings.Contains(stderr, "gh auth refresh -h github.com -s admin:public_key,admin:ssh_signing_key") {
		t.Errorf("no refresh command:\n%s", stderr)
	}
	if s.Exists("gh-keys-jane-acme-authentication") {
		t.Error("uploaded although the token lacks the scope")
	}

	// gh only refreshes its active account, so for another one it switches there and back.
	s.Write("gh-active", "jane-personal\n")
	stderr = s.mustFail(1, "upload", "work")
	if !strings.Contains(stderr, "gh auth switch -h github.com -u jane-acme && gh auth refresh -h github.com -s admin:public_key,admin:ssh_signing_key && gh auth switch -h github.com -u jane-personal") {
		t.Errorf("no switch-and-refresh command:\n%s", stderr)
	}
}

func TestUploadFallsBackToExport(t *testing.T) {
	t.Run("gh isn't signed in as the user", func(t *testing.T) {
		s := githubSandbox(t)
		s.Write("gh-users", "someone-else\n")
		out := s.mustRun("upload", "work")
		if !strings.Contains(s.stderr.String(), "gh isn't signed in to GitHub as jane-acme") || !strings.Contains(out, "https://github.com/settings/ssh/new") {
			t.Errorf("no fallback to manual steps:\nstdout:\n%s\nstderr:\n%s", out, s.stderr.String())
		}
	})
	t.Run("gh isn't installed", func(t *testing.T) {
		s := githubSandbox(t)
		if err := os.Remove(s.Path("fake-bin/gh")); err != nil {
			t.Fatal(err)
		}
		s.OnlyCommands("git", "ssh", "ssh-keygen")
		out := s.mustRun("upload", "work")
		if !strings.Contains(s.stderr.String(), "gh isn't installed") || !strings.Contains(out, "https://github.com/settings/ssh/new") {
			t.Errorf("no fallback to manual steps:\nstdout:\n%s\nstderr:\n%s", out, s.stderr.String())
		}
	})
}

func TestUploadNeedsAGitHubAccount(t *testing.T) {
	s := githubSandbox(t)
	s.mustRun("edit", "work", "--github-user", "")
	if stderr := s.mustFail(1, "upload", "work"); !strings.Contains(stderr, "--github-user <user>") {
		t.Errorf("no hint to set the GitHub user: %s", stderr)
	}
	s.mustRun("edit", "work", "--host", "gitlab.com")
	if stderr := s.mustFail(1, "upload", "work"); !strings.Contains(stderr, "doesn't use github.com") || !strings.Contains(stderr, "doppel export work") {
		t.Errorf("no hint to export: %s", stderr)
	}
}
