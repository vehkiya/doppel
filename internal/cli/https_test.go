package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/git"
)

func TestAddAndEditHTTPSAndHybrid(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_personal", "jane@personal.dev", "")
	s.Key("id_client", "jane@client.com", "")
	s.Key("id_work", "jane@corp.internal", "")

	// 1. Add default SSH account
	s.addAccount("personal", "jane@personal.dev", "--auth-key", "~/.ssh/id_personal")

	// 2. Add HTTPS-only account
	s.addAccount("work", "jane@corp.internal", "--protocol", "https", "--https-user", "jane-corp", "--credential-helper", "osxkeychain", "--folder", "~/work")

	// 3. Add hybrid (both) account
	s.addAccount("client", "jane@client.com", "--protocol", "both", "--https-user", "jane-client", "--auth-key", "~/.ssh/id_client", "--folder", "~/client")

	// Check Git evaluation and leak prevention
	workRepo := s.GitInit("work/api")
	if got := s.GitConfig(workRepo, accounts.KeyCredentialUser); got != "jane-corp" {
		t.Errorf("work credential.username = %q, want jane-corp", got)
	}
	if got := s.GitConfig(workRepo, accounts.KeyCredentialHelperGit); got != "osxkeychain" {
		t.Errorf("work credential.helper = %q, want osxkeychain", got)
	}
	// core.sshCommand must be reset to "ssh", preventing default personal account's key from leaking
	if got := s.GitConfig(workRepo, accounts.KeySSHCommand); got != "ssh" {
		t.Errorf("work core.sshCommand = %q, want ssh", got)
	}

	clientRepo := s.GitInit("client/app")
	if got := s.GitConfig(clientRepo, accounts.KeyCredentialUser); got != "jane-client" {
		t.Errorf("client credential.username = %q, want jane-client", got)
	}
	if got := s.GitConfig(clientRepo, accounts.KeySSHCommand); !strings.Contains(got, "id_client") {
		t.Errorf("client core.sshCommand = %q, want id_client", got)
	}

	// In default personal repo: credential.username must be empty (reset), not leaking from work or client
	notesRepo := s.GitInit("notes")
	if got := s.GitConfig(notesRepo, accounts.KeyCredentialUser); got != "" {
		t.Errorf("personal credential.username leaked: %q, want empty", got)
	}
	if got := s.GitConfig(notesRepo, accounts.KeySSHCommand); !strings.Contains(got, "id_personal") {
		t.Errorf("personal core.sshCommand = %q, want id_personal", got)
	}

	// 4. Test ls table view
	lsOut := s.mustRun("ls")
	if !strings.Contains(lsOut, "https") {
		t.Errorf("ls output missing https:\n%s", lsOut)
	}

	// 5. Test ls --json
	jsonOut := s.mustRun("ls", "--json")
	var list []accountJSON
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatalf("failed to unmarshal ls --json: %v", err)
	}
	byID := map[string]accountJSON{}
	for _, acc := range list {
		byID[acc.ID] = acc
	}
	if byID["work"].Protocol != "https" || byID["work"].HTTPSUser != "jane-corp" || byID["work"].CredentialHelper != "osxkeychain" {
		t.Errorf("work accountJSON = %+v", byID["work"])
	}
	if byID["client"].Protocol != "both" || byID["client"].HTTPSUser != "jane-client" {
		t.Errorf("client accountJSON = %+v", byID["client"])
	}
	if byID["personal"].Protocol != "ssh" {
		t.Errorf("personal accountJSON = %+v", byID["personal"])
	}

	// 6. Test editing protocol
	s.mustRun("edit", "work", "--protocol", "both", "--auth-key", "~/.ssh/id_work")
	if got := s.GitConfig(workRepo, accounts.KeySSHCommand); !strings.Contains(got, "id_work") {
		t.Errorf("work core.sshCommand after edit to both = %q, want id_work", got)
	}

	s.mustRun("edit", "client", "--protocol", "https")
	if got := s.GitConfig(clientRepo, accounts.KeySSHCommand); got != "ssh" {
		t.Errorf("client core.sshCommand after edit to https = %q, want ssh", got)
	}
}

func TestWhoamiHTTPSAndHybrid(t *testing.T) {
	s := newSandbox(t)
	s.Key("id_personal", "jane@personal.dev", "")
	s.addAccount("personal", "jane@personal.dev", "--auth-key", "~/.ssh/id_personal")
	s.addAccount("work", "jane@corp.internal", "--protocol", "https", "--https-user", "jane-corp", "--folder", "~/work")
	s.addAccount("hybrid", "jane@client.com", "--protocol", "both", "--https-user", "jane-client", "--auth-key", "~/.ssh/id_personal", "--folder", "~/hybrid")

	workRepo := s.GitInit("work/api")
	if _, err := git.Run(workRepo, "remote", "add", "origin", "https://gitlab.corp.internal/org/api.git"); err != nil {
		t.Fatal(err)
	}

	// 1. whoami in HTTPS repo under HTTPS account
	out := s.mustRun("whoami", "work/api")
	if !strings.Contains(out, "Auth      HTTPS (jane-corp)") {
		t.Errorf("whoami output missing HTTPS auth:\n%s", out)
	}
	if !strings.Contains(out, "HTTPS auth: configured for jane-corp") {
		t.Errorf("whoami output missing login status:\n%s", out)
	}
	// Shouldn't suggest switching remote to SSH
	if strings.Contains(out, "switch it to SSH") {
		t.Errorf("whoami incorrectly suggested switching to SSH for HTTPS account:\n%s", out)
	}

	// 2. whoami --json in HTTPS repo
	jsonOut := s.mustRun("whoami", "work/api", "--json")
	var id whoamiJSON
	if err := json.Unmarshal([]byte(jsonOut), &id); err != nil {
		t.Fatalf("failed to parse whoami --json: %v", err)
	}
	if id.Protocol != "https" || id.HTTPSUser != "jane-corp" {
		t.Errorf("whoamiJSON = %+v, want protocol=https and https_user=jane-corp", id)
	}

	// 3. HTTPS repo under SSH-only account flags conversion suggestion
	notesRepo := s.GitInit("notes")
	if _, err := git.Run(notesRepo, "remote", "add", "origin", "https://github.com/jane/notes.git"); err != nil {
		t.Fatal(err)
	}
	notesOut := s.mustRun("whoami", "notes")
	if !strings.Contains(notesOut, "switch it to SSH") {
		t.Errorf("whoami for SSH account didn't suggest switching to SSH:\n%s", notesOut)
	}

	// 4. SSH repo under HTTPS-only account flags conversion to HTTPS
	sshWork := s.GitInit("work/ssh-repo")
	if _, err := git.Run(sshWork, "remote", "add", "origin", "git@gitlab.corp.internal:org/ssh-repo.git"); err != nil {
		t.Fatal(err)
	}
	sshWorkOut := s.mustRun("whoami", "work/ssh-repo")
	if !strings.Contains(sshWorkOut, "switch it to HTTPS") {
		t.Errorf("whoami for HTTPS account with SSH remote didn't suggest switching to HTTPS:\n%s", sshWorkOut)
	}

	// 5. Hybrid account whoami shows Auth key and HTTPS user
	s.GitInit("hybrid/repo")
	hybridOut := s.mustRun("whoami", "hybrid/repo", "--offline")
	if !strings.Contains(hybridOut, "Auth key") || !strings.Contains(hybridOut, "HTTPS user jane-client") {
		t.Errorf("hybrid whoami output missing auth key or HTTPS user:\n%s", hybridOut)
	}
}

func TestCompletionProtocol(t *testing.T) {
	s := newSandbox(t)
	out := s.mustRun("__complete", "add", "--protocol", "")
	for _, want := range []string{"ssh", "https", "both", ":words"} {
		if !strings.Contains(out, want) {
			t.Errorf("__complete add --protocol missing %q:\n%s", want, out)
		}
	}
}
