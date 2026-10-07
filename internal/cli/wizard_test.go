package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/tui"
)

// script joins scripted wizard answers, one per prompt. An empty answer
// takes the prompt's default.
func script(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

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
	s.stdin = script(
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
	s.stdin = script(
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
	s.stdin = script(
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
	s.stdin = script("work", "Jane Doe", "jane@acme.com", "", "", "", "", "", "0", "n")
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
	s.stdin = script("2") // of side and work, pick work
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
	if got, quit := a.runAction(tui.Action{Kind: tui.SetDefault, ID: "work"}); got != "✓ work is now the default account" || quit {
		t.Errorf("default: status %q", got)
	}

	s.stdin = script("~/projects/work", "") // the folder, then Enter after the warning that it doesn't exist yet
	a = s.newApp(s.Home)
	if got, quit := a.runAction(tui.Action{Kind: tui.Bind, ID: "work"}); got != "✓ Bound to work: ~/projects/work/" || quit {
		t.Errorf("bind: status %q", got)
	}

	s.stdin = script("1") // the new default after deleting work: personal
	a = s.newApp(s.Home)
	if got, quit := a.runAction(tui.Action{Kind: tui.Delete, ID: "work"}); got != "✓ Deleted account work" || quit {
		t.Errorf("delete: status %q", got)
	}
	if acc := loadAccount(t, s, "personal"); !acc.Default {
		t.Error("personal isn't the default after deleting work")
	}
	// The status comes from the result, not from the wording of the output:
	// an error shows as it is, and waits for Enter.
	s.stdin = script("")
	a = s.newApp(s.Home)
	if got, _ := a.runAction(tui.Action{Kind: tui.SetDefault, ID: "ghost"}); got != "✗ no account named ghost" {
		t.Errorf("failed action: status %q", got)
	}
	if !strings.Contains(s.stdout.String(), "Press Enter") {
		t.Error("the browser didn't wait for Enter after an error")
	}
}

func TestAddWizardMultiAccountImportAccepted(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_work", "jane@work.com", "")

	s.Write(".gitconfig-work", `[user]
	name = Jane Work
	email = jane@work.com
	signingkey = ~/.ssh/id_work.pub
[gpg]
	format = ssh
[commit]
	gpgsign = true
[tag]
	gpgsign = true
[core]
	sshCommand = ssh -i ~/.ssh/id_work
`)
	s.Write(".gitconfig-oss", `[user]
	name = Jane OSS
	email = jane@oss.org
`)
	s.Write(".gitconfig", `[user]
	name = Jane Personal
	email = jane@personal.dev

[includeIf "gitdir:~/work/"]
	path = ~/.gitconfig-work

[includeIf "gitdir:~/oss/"]
	path = ~/.gitconfig-oss
`)

	s.tty = true
	s.stdin = script("")
	s.mustRun("add")

	if !s.Exists(".gitconfig.doppel.bak") {
		t.Fatal(".gitconfig.doppel.bak was not created")
	}
	bak := s.Read(".gitconfig.doppel.bak")
	if !strings.Contains(bak, `path = ~/.gitconfig-work`) || !strings.Contains(bak, `path = ~/.gitconfig-oss`) {
		t.Errorf("backup missing original includes:\n%s", bak)
	}

	globalCfg := s.Read(".gitconfig")
	if strings.Contains(globalCfg, "includeIf") || strings.Contains(globalCfg, ".gitconfig-work") || strings.Contains(globalCfg, ".gitconfig-oss") {
		t.Errorf("global config still contains old includes:\n%s", globalCfg)
	}
	if !strings.Contains(globalCfg, "index.gitconfig") {
		t.Errorf("global config missing doppel index include:\n%s", globalCfg)
	}

	personal := loadAccount(t, s, "personal")
	if personal.Name != "Jane Personal" || personal.Email != "jane@personal.dev" || !personal.Default {
		t.Errorf("personal account = %+v", personal)
	}

	work := loadAccount(t, s, "work")
	if work.Name != "Jane Work" || work.Email != "jane@work.com" || work.Default || !slices.Equal(work.Folders, []string{"~/work/"}) {
		t.Errorf("work account = %+v", work)
	}
	if work.SigningKey != "~/.ssh/id_work.pub" || !work.SignCommits || !work.SignTags {
		t.Errorf("work signing = %q, commits %v, tags %v", work.SigningKey, work.SignCommits, work.SignTags)
	}
	if work.AuthKey != "~/.ssh/id_work" {
		t.Errorf("work auth = %q, want ~/.ssh/id_work", work.AuthKey)
	}

	oss := loadAccount(t, s, "oss")
	if oss.Name != "Jane OSS" || oss.Email != "jane@oss.org" || oss.Default || !slices.Equal(oss.Folders, []string{"~/oss/"}) {
		t.Errorf("oss account = %+v", oss)
	}

	workRepo := s.GitInit("work/proj")
	if out := commitAndVerify(t, workRepo); !strings.Contains(out, `Good "git" signature for jane@work.com`) {
		t.Errorf("work repo commit signature:\n%s", out)
	}

	ossRepo := s.GitInit("oss/lib")
	if who := s.GitConfig(ossRepo, "user.email"); who != "jane@oss.org" {
		t.Errorf("oss repo user.email = %q, want jane@oss.org", who)
	}

	otherRepo := s.GitInit("other/repo")
	if who := s.GitConfig(otherRepo, "user.email"); who != "jane@personal.dev" {
		t.Errorf("other repo user.email = %q, want jane@personal.dev", who)
	}
}

func TestAddWizardMultiAccountImportDeclined(t *testing.T) {
	s := newSandbox(t)
	s.Write(".gitconfig-work", `[user]
	name = Jane Work
	email = jane@work.com
`)
	s.Write(".gitconfig", `[user]
	name = Jane Personal
	email = jane@personal.dev

[includeIf "gitdir:~/work/"]
	path = ~/.gitconfig-work
`)

	s.tty = true
	s.stdin = script(
		"n",
		"custom",
		"Custom User",
		"custom@corp.com",
		"",
		"",
		"~/custom",
		"",
		"",
		"0",
		"",
	)
	s.mustRun("add")

	content := s.Read(".gitconfig")
	if !strings.Contains(content, `path = ~/.gitconfig-work`) {
		t.Errorf(".gitconfig should have kept old includeIf when declined:\n%s", content)
	}

	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "custom" {
		t.Errorf("loaded accounts = %v, want only [custom]", list)
	}
}

func TestInitialImportHelper(t *testing.T) {
	s := newSandbox(t)
	s.Write(".gitconfig-work", `[user]
	name = Jane Work
	email = jane@work.com
`)
	s.Write(".gitconfig", `[user]
	name = Jane Personal
	email = jane@personal.dev

[includeIf "gitdir:~/work/"]
	path = ~/.gitconfig-work
`)

	s.tty = true
	s.stdin = script("n")
	a := s.newApp(s.Home)
	imported, err := a.initialImport()
	if err != nil {
		t.Fatal(err)
	}
	if imported || !a.importDeclined {
		t.Errorf("imported = %v, importDeclined = %v, want false, true", imported, a.importDeclined)
	}

	s.stdin = script("")
	a = s.newApp(s.Home)
	imported, err = a.initialImport()
	if err != nil {
		t.Fatal(err)
	}
	if !imported {
		t.Error("imported = false, want true")
	}
	list, err := accounts.Load(s.Env())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("len(list) = %d, want 2", len(list))
	}
}
