package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/tui"
)

// answers joins scripted wizard answers, one per prompt. An empty answer
// takes the prompt's default.
func answers(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func loadAccount(t *testing.T, s *sandbox, id string) *accounts.Account {
	t.Helper()
	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.Find(list, id)
	if acc == nil {
		t.Fatalf("no account %s", id)
	}
	return acc
}

func TestAddWizard(t *testing.T) {
	s := newSandbox(t)
	s.tty = true
	s.stdin = answers(
		"work", "Jane Doe", "jane@acme.com", // identity
		"",                // hosts: github.com
		"jane-acme",       // GitHub username
		"~/projects/work", // folders (the first account is the default without asking)
		"",                // auth key: generate a new one
		"",                // signing: with the auth key
		"0",               // sign commits and tags
		"",                // save
	)
	s.mustRun("add")

	acc := loadAccount(t, s, "work")
	if acc.Name != "Jane Doe" || acc.Email != "jane@acme.com" || acc.GitHubUser != "jane-acme" || !acc.Default ||
		!slices.Equal(acc.Folders, []string{"~/projects/work/"}) || !slices.Equal(acc.Hosts, []string{"github.com"}) {
		t.Errorf("account = %+v", acc)
	}
	if acc.AuthKey != "~/.ssh/id_ed25519_work" || acc.SigningKey != "~/.ssh/id_ed25519_work.pub" || !acc.SignCommits || !acc.SignTags {
		t.Errorf("keys = %q, %q, commits %v, tags %v", acc.AuthKey, acc.SigningKey, acc.SignCommits, acc.SignTags)
	}
	if out := commitAndVerify(t, s.GitInit("projects/work/api")); !strings.Contains(out, `Good "git" signature for jane@acme.com`) {
		t.Errorf("commit isn't verified:\n%s", out)
	}
}

func TestAddWizardStartsFromTheGlobalIdentity(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_old", "jane@personal.dev", "")
	s.Write(".gitconfig", `[user]
	name = Jane Doe
	email = jane@personal.dev
	signingkey = ~/.ssh/id_old.pub
[gpg]
	format = ssh
[commit]
	gpgsign = true
[core]
	sshCommand = ssh -i ~/.ssh/id_old -o IdentitiesOnly=yes
`)
	s.tty = true
	s.stdin = answers(
		"",                 // start from ~/.gitconfig: yes
		"personal", "", "", // ID; name and email come prefilled
		"", "", "", // hosts, GitHub username, folders
		"", "", // keep the auth key, keep the signing key
		"0", // sign as before: commits only
		"",  // save
	)
	s.mustRun("add")

	acc := loadAccount(t, s, "personal")
	if acc.Name != "Jane Doe" || acc.Email != "jane@personal.dev" || !acc.Default {
		t.Errorf("identity = %+v", acc)
	}
	if acc.AuthKey != "~/.ssh/id_old" || acc.SigningKey != "~/.ssh/id_old.pub" || !acc.SignCommits || acc.SignTags {
		t.Errorf("keys = %q, %q, commits %v, tags %v", acc.AuthKey, acc.SigningKey, acc.SignCommits, acc.SignTags)
	}
	if out := commitAndVerify(t, s.GitInit("repo")); !strings.Contains(out, `Good "git" signature for jane@personal.dev`) {
		t.Errorf("commit isn't verified:\n%s", out)
	}
}

func TestEditWizard(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com", "--folder", "~/projects/work", "--auth-key", "~/.ssh/id_work")
	s.tty = true
	s.stdin = answers(
		"", "jane@acme.io", // name stays, new email
		"", "", "", // hosts, GitHub username, folders stay
		"", // not the default
		"", // keep the auth key
		"", // still no signing
		"", // save
	)
	s.mustRun("edit", "work")

	acc := loadAccount(t, s, "work")
	if acc.Email != "jane@acme.io" || acc.Name != "Jane Doe" || acc.Default || acc.AuthKey != "~/.ssh/id_work" ||
		acc.SigningKey != "" || !slices.Equal(acc.Folders, []string{"~/projects/work/"}) {
		t.Errorf("account after the wizard = %+v", acc)
	}
}

func TestWizardCancel(t *testing.T) {
	s := newSandbox(t)
	s.tty = true
	s.stdin = answers("work", "Jane Doe", "jane@acme.com", "", "", "", "", "", "0", "n")
	if stderr := s.mustFail(1, "add"); !strings.Contains(stderr, "cancelled") {
		t.Errorf("stderr: %s", stderr)
	}
	if s.Exists(accountsDir+"work.gitconfig") || s.Exists(".ssh/id_ed25519_work") {
		t.Error("a cancelled wizard left files behind")
	}
}

func TestWizardOnlyRunsWithoutFlags(t *testing.T) {
	s := newSandbox(t)
	s.tty = true
	// Flags in a terminal still mean "no questions".
	s.mustRun("add", "work", "--name", "Jane Doe", "--email", "jane@acme.com")
	if out := s.stdout.String(); strings.Contains(out, "Identity") {
		t.Errorf("the wizard ran although flags were given:\n%s", out)
	}
	// Without a terminal, missing flags are an error rather than questions.
	s.tty = false
	s.mustFail(2, "add")
	s.mustFail(2, "edit", "work")
}

func TestRmAsksForANewDefault(t *testing.T) {
	s := newSandbox(t)
	for _, id := range []string{"personal", "side", "work"} {
		s.addAccount(id, "jane@"+id+".dev")
	}
	s.tty = true
	s.stdin = answers("2") // of side and work, pick work
	s.mustRun("rm", "personal", "--yes")
	if acc := loadAccount(t, s, "work"); !acc.Default {
		t.Error("work isn't the default after picking it")
	}
}

func TestBrowserActions(t *testing.T) {
	s := newSandbox(t)
	s.addAccount("personal", "jane@personal.dev")
	s.addAccount("work", "jane@acme.com")
	s.tty = true

	a := s.newApp(s.Home)
	if got := a.runAction(tui.Action{Kind: tui.SetDefault, ID: "work"}); got != "✓ work is now the default account" {
		t.Errorf("default: status %q", got)
	}

	s.stdin = answers("~/projects/work", "") // the folder, then Enter after the warning that it doesn't exist yet
	a = s.newApp(s.Home)
	if got := a.runAction(tui.Action{Kind: tui.Bind, ID: "work"}); got != "✓ Bound to work: ~/projects/work/" {
		t.Errorf("bind: status %q", got)
	}

	s.stdin = answers("1") // the new default after deleting work: personal
	a = s.newApp(s.Home)
	if got := a.runAction(tui.Action{Kind: tui.Delete, ID: "work"}); got != "✓ Deleted account work" {
		t.Errorf("delete: status %q", got)
	}
	if acc := loadAccount(t, s, "personal"); !acc.Default {
		t.Error("personal isn't the default after deleting work")
	}
}

func TestStatusOf(t *testing.T) {
	cases := []struct{ out, problems, want string }{
		{"✓ Added account work\n  created ~/x\n", "", "✓ Added account work"},
		{"", "⚠ a warning\n✗ something failed\n", "✗ something failed"},
		{"Dry run\n", "", ""},
	}
	for _, c := range cases {
		if got := statusOf(c.out, c.problems); got != c.want {
			t.Errorf("statusOf(%q, %q) = %q, want %q", c.out, c.problems, got, c.want)
		}
	}
}
