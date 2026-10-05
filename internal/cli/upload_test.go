package cli

import (
	"os"
	"strings"
	"testing"
)

// fakeGH stands in for gh. Its state lives in files in the sandbox:
// gh-hosts (hosts gh is signed in to), gh-users (signed-in users),
// gh-scopes-<user>, gh-active, and gh-keys-<user>-<kind> (the keys GitHub
// has). Each call is recorded in gh-calls, with the GH_HOST it ran against.
// Like gh, it matches user names exactly. A file named gh-old makes it a gh
// too old to know `auth status --json`, and gh-version sets the version it
// reports.
const fakeGH = `echo "GH_HOST=$GH_HOST $*" >> "$HOME/gh-calls"
u="${GH_TOKEN#token-}"
case "$1 $2" in
"--version ")
  echo "gh version $(cat "$HOME/gh-version" 2>/dev/null || echo 2.102.0) (2026-09-30)" ;;
"auth token")
  h=""; user=""; shift 2
  while [ $# -gt 0 ]; do
    case "$1" in --hostname) h="$2"; shift 2 ;; --user) user="$2"; shift 2 ;; *) shift ;; esac
  done
  grep -qx "$h" "$HOME/gh-hosts" 2>/dev/null || { echo "not logged in to $h" >&2; exit 1; }
  if [ -z "$user" ]; then echo "token-active"; exit 0; fi
  grep -qx "$user" "$HOME/gh-users" 2>/dev/null || { echo "no oauth token found for $user" >&2; exit 1; }
  echo "token-$user" ;;
"auth status")
  h=""; json=""; shift 2
  while [ $# -gt 0 ]; do
    case "$1" in --hostname) h="$2"; shift 2 ;; --json) json=1; shift 2 ;; *) shift ;; esac
  done
  if [ -z "$json" ]; then
    grep -qx "$h" "$HOME/gh-hosts" 2>/dev/null || { echo "You are not logged into any accounts on $h" >&2; exit 1; }
    echo "Logged in to $h"; exit 0
  fi
  [ -e "$HOME/gh-old" ] && { echo "unknown flag: --json" >&2; exit 1; }
  printf '{"hosts":{'
  if grep -qx "$h" "$HOME/gh-hosts" 2>/dev/null; then
    printf '"%s":[' "$h"; first=1
    while read -r name; do
      [ -n "$name" ] || continue
      [ $first = 1 ] || printf ','
      first=0; active=false
      [ "$name" = "$(cat "$HOME/gh-active" 2>/dev/null)" ] && active=true
      printf '{"state":"success","active":%s,"host":"%s","login":"%s","scopes":"%s"}' "$active" "$h" "$name" "$(cat "$HOME/gh-scopes-$name" 2>/dev/null)"
    done < "$HOME/gh-users"
    printf ']'
  fi
  printf '}}\n' ;;
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
	s.Write("gh-hosts", "github.com\n")
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
		if !strings.Contains(s.stderr.String(), "gh isn't signed in to github.com as jane-acme") || !strings.Contains(out, "https://github.com/settings/ssh/new") {
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
	if stderr := s.mustFail(1, "upload", "work"); !strings.Contains(stderr, "is GitHub as far as doppel can tell") ||
		!strings.Contains(stderr, "gh auth login -h <host>") || !strings.Contains(stderr, "doppel export work") {
		t.Errorf("no hint to sign gh in or export: %s", stderr)
	}
}

func TestUploadToGitHubEnterprise(t *testing.T) {
	t.Run("a GitHub Enterprise Server gh is signed in to", func(t *testing.T) {
		s := githubSandbox(t)
		s.mustRun("edit", "work", "--host", "git.acme.com")
		// Until gh is signed in there, doppel can't tell it's GitHub.
		s.mustFail(1, "upload", "work")
		s.Write("gh-hosts", "github.com\ngit.acme.com\n")
		out := s.mustRun("upload", "work")
		if !strings.Contains(out, "Added ~/.ssh/id_work.pub to jane-acme on git.acme.com's authentication keys") {
			t.Errorf("upload output:\n%s", out)
		}
		if calls := s.Read("gh-calls"); !strings.Contains(calls, "GH_HOST=git.acme.com ssh-key add") {
			t.Errorf("keys weren't added on git.acme.com:\n%s", calls)
		}
	})
	t.Run("GitHub Enterprise Cloud with data residency", func(t *testing.T) {
		s := githubSandbox(t)
		s.Write("gh-hosts", "acme.ghe.com\n")
		s.mustRun("edit", "work", "--host", "acme.ghe.com")
		s.mustRun("upload", "work")
		if calls := s.Read("gh-calls"); !strings.Contains(calls, "GH_HOST=acme.ghe.com ssh-key add") {
			t.Errorf("keys weren't added on acme.ghe.com:\n%s", calls)
		}
	})
	t.Run("github.com and ssh.github.com are one GitHub", func(t *testing.T) {
		s := githubSandbox(t)
		s.mustRun("edit", "work", "--host", "github.com", "--host", "ssh.github.com")
		s.mustRun("upload", "work")
		if n := strings.Count(s.Read("gh-calls"), "ssh-key add"); n != 2 { // one key, added as each kind once
			t.Errorf("%d keys added, want 2:\n%s", n, s.Read("gh-calls"))
		}
	})
	t.Run("export gives GitHub steps for an Enterprise Server", func(t *testing.T) {
		s := githubSandbox(t)
		s.Write("gh-hosts", "github.com\ngit.acme.com\n")
		s.mustRun("edit", "work", "--host", "git.acme.com")
		if out := s.mustRun("export", "work", "--no-copy"); !strings.Contains(out, "https://git.acme.com/settings/ssh/new") {
			t.Errorf("export steps:\n%s", out)
		}
	})
}

func TestGitHubHostDetectionNeverReadsAToken(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "current gh", true: "gh without auth status --json"}[old], func(t *testing.T) {
			s := githubSandbox(t)
			if old {
				s.Write("gh-old", "")
			}
			s.Write("gh-hosts", "github.com\ngit.acme.com\n")
			s.mustRun("edit", "work", "--host", "git.acme.com", "--host", "codeberg.org")
			// A second account on the same hosts: doctor checks both.
			s.mustRun("add", "client", "--name", "Jane", "--email", "jane@client.dev", "--host", "codeberg.org", "--host", "git.acme.com", "--auth-key", "")
			// Each command asks gh about a host at most once, and never for a token.
			checkCalls := func(command string) {
				t.Helper()
				calls := strings.Split(strings.TrimSpace(s.Read("gh-calls")), "\n")
				seen := map[string]bool{}
				for _, c := range calls {
					if strings.Contains(c, "auth token") {
						t.Errorf("%s read a token to find out which hosts are GitHub:\n%s", command, strings.Join(calls, "\n"))
					}
					if seen[c] {
						t.Errorf("%s asked gh the same thing twice: %s", command, c)
					}
					seen[c] = true
				}
				s.Write("gh-calls", "")
			}
			s.Write("gh-calls", "")
			if out := s.mustRun("export", "work", "--no-copy"); !strings.Contains(out, "https://git.acme.com/settings/ssh/new") {
				t.Errorf("export doesn't treat git.acme.com as GitHub:\n%s", out)
			}
			checkCalls("export")
			s.run("doctor")
			checkCalls("doctor")
		})
	}
}

func TestUploadNeedsAGHThatCanPickTheAccount(t *testing.T) {
	s := githubSandbox(t)
	s.Write("gh-version", "2.39.2")
	out := s.mustRun("upload", "work")
	if got := s.stderr.String(); !strings.Contains(got, "doppel needs gh 2.40 or newer to use the account's own token, and this gh is 2.39.2") {
		t.Errorf("warning:\n%s", got)
	}
	if !strings.Contains(out, "https://github.com/settings/ssh/new") {
		t.Errorf("no steps to add the keys by hand:\n%s", out)
	}
	if calls := s.Read("gh-calls"); strings.Contains(calls, "auth token") || strings.Contains(calls, "ssh-key add") {
		t.Errorf("an old gh was still asked for a token or to add keys:\n%s", calls)
	}
}

func TestUploadUsesGHsSpellingOfTheUser(t *testing.T) {
	s := githubSandbox(t)
	s.mustRun("edit", "work", "--github-user", "Jane-Acme")
	pub := strings.Join(strings.Fields(s.Read(".ssh/id_work.pub"))[:2], " ")

	// gh matches names exactly, and signs in as jane-acme.
	out := s.mustRun("upload", "work")
	if !strings.Contains(out, "Added ~/.ssh/id_work.pub to jane-acme's authentication keys") {
		t.Errorf("upload output:\n%s", out)
	}
	if got := strings.TrimSpace(s.Read("gh-keys-jane-acme-authentication")); got != pub {
		t.Errorf("GitHub's keys = %q, want the work key", got)
	}
	if !strings.Contains(out, "doppel edit work --github-user jane-acme") {
		t.Errorf("no hint to correct the stored spelling:\n%s", out)
	}
	if got := loadAccount(t, s, "work").GitHubUser; got != "Jane-Acme" {
		t.Errorf("without a terminal, the stored user changed to %q", got)
	}

	t.Run("a terminal offers to correct it", func(t *testing.T) {
		s.tty = true
		s.stdin = "y\n"
		s.mustRun("upload", "work")
		if got := loadAccount(t, s, "work").GitHubUser; got != "jane-acme" {
			t.Errorf("stored GitHub user = %q after accepting", got)
		}
		s.stdin = ""
		if out := s.mustRun("upload", "work"); strings.Contains(out, "spells the user") {
			t.Errorf("offered again although the spelling is right:\n%s", out)
		}
	})
	t.Run("declining keeps it", func(t *testing.T) {
		s.mustRun("edit", "work", "--github-user", "JANE-ACME")
		s.tty = true
		s.stdin = "n\n"
		s.mustRun("upload", "work")
		if got := loadAccount(t, s, "work").GitHubUser; got != "JANE-ACME" {
			t.Errorf("stored GitHub user = %q after declining", got)
		}
	})
}

func TestUploadWorksForAnAccountGHIsNotUsing(t *testing.T) {
	s := githubSandbox(t)
	s.Write("gh-users", "jane-personal\njane-acme\n")
	s.Write("gh-active", "jane-personal\n")
	out := s.mustRun("upload", "work")
	if !strings.Contains(out, "Added ~/.ssh/id_work.pub to jane-acme's authentication keys") {
		t.Errorf("upload output:\n%s", out)
	}
	if s.Exists("gh-keys-jane-personal-authentication") {
		t.Error("a key went to gh's active account instead")
	}
}

func TestUploadSaysWhyGHRefusedAndWhatItKnows(t *testing.T) {
	t.Run("the accounts gh has", func(t *testing.T) {
		s := githubSandbox(t)
		s.Write("gh-users", "jane-personal\nsomeone-else\n")
		s.mustRun("upload", "work")
		if got := s.stderr.String(); !strings.Contains(got, "gh isn't signed in to github.com as jane-acme; gh has jane-personal, someone-else on github.com") {
			t.Errorf("stderr:\n%s", got)
		}
	})
	t.Run("gh has none on the host", func(t *testing.T) {
		s := githubSandbox(t)
		s.Write("gh-users", "")
		s.mustRun("upload", "work")
		if got := s.stderr.String(); !strings.Contains(got, "gh has no accounts on github.com") {
			t.Errorf("stderr:\n%s", got)
		}
	})
	t.Run("a gh too old to list accounts gives its own reason", func(t *testing.T) {
		s := githubSandbox(t)
		s.Write("gh-old", "")
		s.Write("gh-users", "someone-else\n")
		s.mustRun("upload", "work")
		if got := s.stderr.String(); !strings.Contains(got, "gh isn't signed in to github.com as jane-acme (no oauth token found for jane-acme)") {
			t.Errorf("stderr:\n%s", got)
		}
	})
}

func TestUploadWithAnOlderGH(t *testing.T) {
	s := githubSandbox(t)
	s.Write("gh-old", "")
	out := s.mustRun("upload", "work")
	if !strings.Contains(out, "Added ~/.ssh/id_work.pub to jane-acme's authentication keys") {
		t.Errorf("upload output:\n%s", out)
	}
}
