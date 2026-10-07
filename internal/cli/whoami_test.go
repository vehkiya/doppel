package cli

import (
	"encoding/json"
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

func TestWhoamiJSON(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev", "--name", "Jane Personal")
	s.addAccount("work", "jane@acme.com", "--name", "Jane Work", "--folder", "~/projects/work")
	api := s.GitInit("projects/work/api")

	// 1. In a repo with an account applied.
	out := s.mustRun("whoami", "projects/work/api", "--json")
	var id whoamiJSON
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("failed to parse whoami --json: %v\n%s", err, out)
	}
	if !id.InRepo || id.Account != "work" || id.Rule != "folder ~/projects/work/" ||
		id.Email != "jane@acme.com" || id.Name != "Jane Work" || !id.Exists ||
		len(id.Overrides) != 0 || id.Login != nil {
		t.Errorf("whoamiJSON = %+v", id)
	}

	// 2. With overrides: override should be in the JSON, and stderr should be empty.
	if _, err := git.Run(api, "config", "user.email", "override@example.com"); err != nil {
		t.Fatal(err)
	}
	out = s.mustRun("whoami", "projects/work/api", "--json")
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("failed to parse whoami --json: %v\n%s", err, out)
	}
	if len(id.Overrides) != 1 || !strings.Contains(id.Overrides[0], "user.email = override@example.com") {
		t.Errorf("whoamiJSON overrides = %v", id.Overrides)
	}
	if s.stderr.Len() > 0 {
		t.Errorf("unexpected stderr when --json is passed:\n%s", s.stderr.String())
	}

	// 3. Outside a repository.
	out = s.mustRun("whoami", s.Mkdir("notes"), "--json")
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("failed to parse whoami --json: %v\n%s", err, out)
	}
	if id.InRepo || id.Account != "" || id.NewRepoAccount != "personal" || id.NewRepoRule != "default account" {
		t.Errorf("outside repo whoamiJSON = %+v", id)
	}

	// 4. In a repository without any doppel accounts.
	s2 := newSandbox(t)
	s2.GitInit("bare-repo")
	out = s2.mustRun("whoami", "bare-repo", "--json")
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("failed to parse whoami --json without accounts: %v\n%s", err, out)
	}
	if !id.InRepo || id.Account != "" || id.Rule != "" {
		t.Errorf("whoamiJSON without accounts = %+v", id)
	}
}

func TestWhoamiStaleIndex(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.GitInit("projects/work/api")

	// Initially, index is fresh.
	out := s.mustRun("whoami", "projects/work/api", "--json")
	var id whoamiJSON
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatal(err)
	}
	if id.StaleIndex {
		t.Error("expected fresh index, got StaleIndex = true")
	}

	// Drop a new account file into accounts/.
	s.Write(".config/doppel/accounts/work.gitconfig", `[doppel]
	account = work
	folder = ~/projects/work/
[user]
	name = Jane Work
	email = jane@acme.com
[core]
	sshCommand = ssh
[commit]
	gpgsign = false
[tag]
	gpgsign = false
`)

	// whoami in repo detects stale index and suggests doctor --fix.
	s.run("whoami", "projects/work/api")
	if !strings.Contains(s.stderr.String(), "doppel's index is out of date; run `doppel doctor --fix`") {
		t.Errorf("whoami didn't warn about stale index:\n%s", s.stderr.String())
	}
	s.stderr.Reset()

	// whoami --json reports stale_index = true.
	out = s.mustRun("whoami", "projects/work/api", "--json")
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatal(err)
	}
	if !id.StaleIndex {
		t.Error("expected StaleIndex = true after dropping account file")
	}

	// whoami outside repo also warns.
	s.run("whoami", s.Mkdir("notes"))
	if !strings.Contains(s.stderr.String(), "doppel's index is out of date; run `doppel doctor --fix`") {
		t.Errorf("whoami outside repo didn't warn about stale index:\n%s", s.stderr.String())
	}
	s.stderr.Reset()

	// Running doctor --fix updates the index.
	s.mustRun("doctor", "--fix")

	// Now whoami is fresh again.
	s.run("whoami", "projects/work/api")
	if strings.Contains(s.stderr.String(), "doppel's index is out of date") {
		t.Errorf("whoami still warned after doctor --fix:\n%s", s.stderr.String())
	}
	out = s.mustRun("whoami", "projects/work/api", "--json")
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatal(err)
	}
	if id.StaleIndex || id.Account != "work" {
		t.Errorf("after fix: StaleIndex = %v, Account = %s", id.StaleIndex, id.Account)
	}
}
