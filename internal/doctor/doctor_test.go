package doctor_test

import (
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/doctor"
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
