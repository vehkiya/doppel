package doctor_test

import (
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/doctor"
	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/hosts"
	"github.com/vehkiya/doppel/internal/testenv"
)

// Check reports findings rather than printing them: each in its area, with
// its fix, and without writing anything.
func TestCheckReturnsFindings(t *testing.T) {
	s := testenv.New(t)
	env := s.Env()
	env.GOOS = "linux"
	s.Key("id_work", "jane@acme.com", "")
	list := []*accounts.Account{
		{ID: "personal", Name: "Jane", Email: "jane@personal.dev", Hosts: []string{"github.com"}, AuthKey: "~/.ssh/id_missing", Default: true},
		{ID: "work", Name: "Jane", Email: "jane@acme.com", Hosts: []string{"gitlab.com"}, AuthKey: "~/.ssh/id_work", Folders: []string{"~/work/"}},
	}
	findings := doctor.Check(doctor.Options{Env: env, Accounts: list, GitHub: &hosts.GitHub{}})

	find := func(area, message string) *doctor.Finding {
		for i, f := range findings {
			if f.Area == area && f.Message == message {
				return &findings[i]
			}
		}
		t.Errorf("no finding %q in %q among:\n%+v", message, area, findings)
		return nil
	}
	if f := find("Account personal", "Auth key ~/.ssh/id_missing doesn't exist"); f != nil && (f.Severity != doctor.Problem || f.Fix == "") {
		t.Errorf("missing key: %+v", f)
	}
	if f := find("Account work", "Auth key ~/.ssh/id_work has no passphrase, so anyone who copies it can use it"); f != nil &&
		(f.Severity != doctor.Warning || f.Fix != "Add a passphrase: ssh-keygen -p -f ~/.ssh/id_work") {
		t.Errorf("key without a passphrase: %+v", f)
	}
	if f := find("Git config", "~/.gitconfig doesn't include doppel's accounts, so Git doesn't use them"); f != nil && f.Severity != doctor.Problem {
		t.Errorf("missing include: %+v", f)
	}
	if doctor.Count(findings, doctor.Problem) < 2 {
		t.Errorf("Count(Problem) = %d", doctor.Count(findings, doctor.Problem))
	}
	if s.Exists(".config/doppel/index.gitconfig") {
		t.Error("Check wrote doppel's files without Fix")
	}

	findings = doctor.Check(doctor.Options{Env: env, Accounts: list, GitHub: &hosts.GitHub{}, Fix: true})
	if !s.Exists(".config/doppel/index.gitconfig") {
		t.Errorf("Fix didn't write doppel's files: %+v", findings)
	}
}

func TestCheckHTTPSAndHybridAccounts(t *testing.T) {
	s := testenv.New(t)
	env := s.Env()
	env.GOOS = "linux"

	// Create repos with HTTPS and SSH remotes.
	httpsRepo := s.GitInit("https-work/repo1")
	if _, err := git.Run(httpsRepo, "remote", "add", "origin", "https://github.com/corp/repo1.git"); err != nil {
		t.Fatal(err)
	}

	sshRepo := s.GitInit("https-work/repo2")
	if _, err := git.Run(sshRepo, "remote", "add", "origin", "git@github.com:corp/repo2.git"); err != nil {
		t.Fatal(err)
	}

	hybridRepo1 := s.GitInit("hybrid-work/repo1")
	if _, err := git.Run(hybridRepo1, "remote", "add", "origin", "https://github.com/client/repo1.git"); err != nil {
		t.Fatal(err)
	}

	hybridRepo2 := s.GitInit("hybrid-work/repo2")
	if _, err := git.Run(hybridRepo2, "remote", "add", "origin", "git@github.com:client/repo2.git"); err != nil {
		t.Fatal(err)
	}

	list := []*accounts.Account{
		{
			ID: "https-acc", Name: "Jane", Email: "jane@corp.com", Hosts: []string{"github.com"},
			Protocol: accounts.ProtocolHTTPS, HTTPSUser: "jane-corp",
			Folders: []string{"~/https-work/"},
		},
		{
			ID: "hybrid-acc", Name: "Jane", Email: "jane@client.com", Hosts: []string{"github.com"},
			Protocol: accounts.ProtocolBoth, HTTPSUser: "jane-client",
			Folders: []string{"~/hybrid-work/"},
		},
	}

	findings := doctor.Check(doctor.Options{Env: env, Accounts: list, GitHub: &hosts.GitHub{}})

	// https-acc should warn about repo2 (SSH remote in HTTPS-only account), but not repo1.
	var httpsWarnings []string
	var hybridWarnings []string
	for _, f := range findings {
		if f.Area == "Account https-acc" {
			httpsWarnings = append(httpsWarnings, f.Message)
		}
		if f.Area == "Account hybrid-acc" {
			hybridWarnings = append(hybridWarnings, f.Message)
		}
	}

	hasSSHWarning := false
	hasHTTPSWarning := false
	for _, msg := range httpsWarnings {
		if msg == "~/https-work/repo2 fetches over SSH, but https-acc is configured for HTTPS only" {
			hasSSHWarning = true
		}
		if msg == "~/https-work/repo1 fetches over HTTPS, which doppel's keys don't cover" {
			hasHTTPSWarning = true
		}
	}
	if !hasSSHWarning {
		t.Errorf("https-acc should warn about SSH repo2: %v", httpsWarnings)
	}
	if hasHTTPSWarning {
		t.Errorf("https-acc should NOT warn about HTTPS repo1: %v", httpsWarnings)
	}

	// hybrid-acc should have neither SSH nor HTTPS remote warnings.
	for _, msg := range hybridWarnings {
		if msg == "~/hybrid-work/repo1 fetches over HTTPS, which doppel's keys don't cover" ||
			msg == "~/hybrid-work/repo2 fetches over SSH, but hybrid-acc is configured for HTTPS only" {
			t.Errorf("hybrid-acc should accept both remotes without warning: %s", msg)
		}
	}
}

func TestCheckIDESettings(t *testing.T) {
	s := testenv.New(t)
	env := s.Env()
	env.GOOS = "linux"

	list := []*accounts.Account{
		{ID: "personal", Name: "Jane", Email: "jane@personal.dev", Hosts: []string{"github.com"}, Default: true, SignCommits: true, SigningKey: "~/.ssh/id_sign.pub"},
		{ID: "work", Name: "Jane", Email: "jane@acme.com", Hosts: []string{"github.com"}, Protocol: accounts.ProtocolBoth, HTTPSUser: "jane-acme", Folders: []string{"~/work/"}},
	}

	// 1. Initially no IDEs installed: 0 IDE findings.
	findings := doctor.Check(doctor.Options{Env: env, Accounts: list, GitHub: &hosts.GitHub{}})
	for _, f := range findings {
		if f.Area == "IDE settings" {
			t.Errorf("expected 0 IDE findings initially, got: %+v", f)
		}
	}

	// 2. JetBrains with IDEA_SSH (problem).
	s.Write(".config/JetBrains/IntelliJIdea2024.1/options/git.xml", `<application>
  <component name="Git.Application.Settings">
    <option name="SSH_EXECUTABLE" value="IDEA_SSH" />
  </component>
</application>`)

	// VS Code with conflicting credentials and disabled signing (warnings).
	s.Write(".config/Code/User/settings.json", `// VS Code config
{
  /* Comments test */
  "git.useBuiltinCredentialProvider": true,
  "git.enableCommitSigning": false,
}`)

	findings = doctor.Check(doctor.Options{Env: env, Accounts: list, GitHub: &hosts.GitHub{}})

	var ideFindings []doctor.Finding
	for _, f := range findings {
		if f.Area == "IDE settings" {
			ideFindings = append(ideFindings, f)
		}
	}

	if len(ideFindings) != 3 {
		t.Fatalf("expected 3 IDE findings, got %d: %+v", len(ideFindings), ideFindings)
	}

	// Finding 1: IntelliJ IDEA problem
	fJB := ideFindings[0]
	if fJB.Severity != doctor.Problem || !strings.Contains(fJB.Message, "built-in SSH executable") || !strings.Contains(fJB.Fix, "Native") {
		t.Errorf("unexpected JetBrains finding: %+v", fJB)
	}

	// Finding 2: VS Code credential provider warning
	fVSCred := ideFindings[1]
	if fVSCred.Severity != doctor.Warning || !strings.Contains(fVSCred.Message, "git.useBuiltinCredentialProvider") {
		t.Errorf("unexpected VS Code cred finding: %+v", fVSCred)
	}

	// Finding 3: VS Code signing warning
	fVSSign := ideFindings[2]
	if fVSSign.Severity != doctor.Warning || !strings.Contains(fVSSign.Message, "git.enableCommitSigning") {
		t.Errorf("unexpected VS Code signing finding: %+v", fVSSign)
	}

	// 3. Fix settings and verify OK findings.
	s.Write(".config/JetBrains/IntelliJIdea2024.1/options/git.xml", `<application>
  <component name="Git.Application.Settings">
    <option name="SSH_EXECUTABLE" value="NATIVE_SSH" />
  </component>
</application>`)

	s.Write(".config/Code/User/settings.json", `{
  "git.useBuiltinCredentialProvider": false,
  "git.enableCommitSigning": true
}`)

	findings = doctor.Check(doctor.Options{Env: env, Accounts: list, GitHub: &hosts.GitHub{}})
	ideFindings = nil
	for _, f := range findings {
		if f.Area == "IDE settings" {
			ideFindings = append(ideFindings, f)
		}
	}

	if len(ideFindings) != 3 {
		t.Fatalf("expected 3 OK IDE findings, got %d: %+v", len(ideFindings), ideFindings)
	}
	for _, f := range ideFindings {
		if f.Severity != doctor.OK {
			t.Errorf("expected OK finding, got: %+v", f)
		}
	}
}
