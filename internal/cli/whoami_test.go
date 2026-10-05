package cli

import (
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/git"
)

func TestWhoami(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work")
	api := s.GitInit("projects/work/api")

	out := s.mustRun("whoami", "projects/work/api")
	for _, want := range []string{"work (folder ~/projects/work/)", "jane@acme.com", "your default SSH keys", "off"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami output is missing %q:\n%s", want, out)
		}
	}
	if s.stderr.Len() > 0 {
		t.Errorf("unexpected warnings:\n%s", s.stderr.String())
	}

	if _, err := git.Run(api, "config", "user.email", "override@example.com"); err != nil {
		t.Fatal(err)
	}
	s.runIn(api, "whoami")
	if !strings.Contains(s.stderr.String(), "user.email = override@example.com comes from .git/config (local config)") {
		t.Errorf("no warning about the repo's own email:\n%s", s.stderr.String())
	}

	// A global setting added below doppel's include overrides every account.
	s.Write(".gitconfig", s.Read(".gitconfig")+"[user]\n\tname = Late Global\n")
	s.run("whoami", "projects/work/api")
	if !strings.Contains(s.stderr.String(), "user.name = Late Global comes from "+s.Path(".gitconfig")) {
		t.Errorf("no warning about the global setting after the include:\n%s", s.stderr.String())
	}

	out = s.mustRun("whoami", "~/projects/work/not-cloned-yet")
	if !strings.Contains(out, "doesn't exist yet") || !strings.Contains(out, "work (folder ~/projects/work/)") {
		t.Errorf("whoami for a future clone:\n%s", out)
	}
	out = s.mustRun("whoami", s.Mkdir("notes"))
	if !strings.Contains(out, "not a Git repository") || !strings.Contains(out, "personal (default account)") {
		t.Errorf("whoami outside a repo:\n%s", out)
	}
}

func TestWhoamiWithoutAccounts(t *testing.T) {
	s := newSandbox(t)
	s.GitInit("repo")
	out := s.mustRun("whoami", "repo")
	if !strings.Contains(out, "none") || !strings.Contains(out, "doppel bind") || !strings.Contains(out, "Auth key  your default SSH keys") {
		t.Errorf("whoami with no accounts:\n%s", out)
	}
}

func TestWhoamiForAFutureCloneInsideARepo(t *testing.T) {
	s := newSandbox(t)
	s.GitInit(".") // the home directory is itself a repo, as with yadm
	s.Mkdir("work")
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/work")

	out := s.mustRun("whoami", "~/work/newclone")
	if !strings.Contains(out, "work (folder ~/work/)") {
		t.Errorf("whoami for a clone target:\n%s", out)
	}
	// The existing folder belongs to the home repo, but a new repo there
	// would get the work account.
	out = s.mustRun("whoami", "~/work")
	if !strings.Contains(out, "personal (default account)") || !strings.Contains(out, "New repos work (folder ~/work/)") {
		t.Errorf("whoami for a plain folder inside the home repo:\n%s", out)
	}
	if got := s.GitConfig(s.GitInit("work/newclone"), "user.email"); got != "jane@acme.com" {
		t.Errorf("the clone got %q", got)
	}
}
