package accounts

import (
	"slices"
	"testing"

	"github.com/vehkiya/doppel/internal/testenv"
)

func TestFromGlobalReadsIncludedFilesButNotDoppels(t *testing.T) {
	s := testenv.New(t)
	s.Write(".gitconfig.local", "[user]\n\tname = Jane Doe\n\temail = jane@personal.dev\n")
	s.Write(".config/doppel/index.gitconfig", "[user]\n\temail = someone@doppel.example\n")
	s.Write(".gitconfig", "[include]\n\tpath = ~/.gitconfig.local\n[include]\n\tpath = ~/.config/doppel/index.gitconfig\n")

	acc, source, ok := FromGlobal(s.Env())
	if !ok {
		t.Fatal("no identity found in the included file")
	}
	if acc.Name != "Jane Doe" || acc.Email != "jane@personal.dev" {
		t.Errorf("identity = %q <%q>, want Jane Doe <jane@personal.dev>", acc.Name, acc.Email)
	}
	if want := s.Path(".gitconfig.local"); source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
}

func TestDiscoverMultiAccount(t *testing.T) {
	s := testenv.New(t)
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
	sshCommand = ssh -i ~/.ssh/id_work -o IdentitiesOnly=yes
`)
	s.Write(".gitconfig-oss", `[user]
	email = jane@oss.org
`)
	s.Write(".gitconfig", `[user]
	name = Jane Personal
	email = jane@personal.dev

[includeIf "gitdir:~/projects/work/"]
	path = ~/.gitconfig-work

[includeIf "gitdir:~/oss/"]
	path = ~/.gitconfig-oss

[includeIf "gitdir/i:~/oss-extra/"]
	path = ~/.gitconfig-oss

[includeIf "onbranch:main"]
	path = ~/.gitconfig-other
`)

	disc, err := Discover(s.Env())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(disc.Accounts) != 3 {
		t.Fatalf("discovered %d accounts, want 3", len(disc.Accounts))
	}

	personal := disc.Accounts[0].Account
	if personal.ID != "personal" || !personal.Default || personal.Name != "Jane Personal" || personal.Email != "jane@personal.dev" {
		t.Errorf("personal = %+v, want ID personal, Default true", personal)
	}
	if len(personal.Folders) != 0 {
		t.Errorf("personal folders = %v, want none", personal.Folders)
	}

	work := disc.Accounts[1].Account
	if work.ID != "work" || work.Default || work.Name != "Jane Work" || work.Email != "jane@work.com" {
		t.Errorf("work = %+v, want ID work, Default false", work)
	}
	if !slices.Equal(work.Folders, []string{"~/projects/work/"}) {
		t.Errorf("work folders = %v, want [~/projects/work/]", work.Folders)
	}
	if work.SigningKey != "~/.ssh/id_work.pub" || !work.SignCommits || !work.SignTags {
		t.Errorf("work signing key = %q, signCommits = %v, signTags = %v", work.SigningKey, work.SignCommits, work.SignTags)
	}
	if work.AuthKey != "~/.ssh/id_work" {
		t.Errorf("work auth key = %q, want ~/.ssh/id_work", work.AuthKey)
	}

	// OSS account inherited name from personal base identity
	oss := disc.Accounts[2].Account
	if oss.ID != "oss" || oss.Name != "Jane Personal" || oss.Email != "jane@oss.org" {
		t.Errorf("oss = %+v, want ID oss, name Jane Personal, email jane@oss.org", oss)
	}
	if len(oss.Folders) != 2 || !slices.Contains(oss.Folders, "~/oss/") || !slices.Contains(oss.Folders, "~/oss-extra/") {
		t.Errorf("oss folders = %v, want [~/oss/ ~/oss-extra/]", oss.Folders)
	}
	if len(disc.Accounts[2].Cleanups) != 2 {
		t.Errorf("oss cleanups = %d, want 2", len(disc.Accounts[2].Cleanups))
	}
}

func TestDiscoverNoBaseIdentity(t *testing.T) {
	s := testenv.New(t)
	s.Write(".gitconfig-client", `[user]
	name = Alice
	email = alice@client.com
`)
	s.Write(".gitconfig", `
[includeIf "gitdir:~/client/"]
	path = ~/.gitconfig-client
`)

	disc, err := Discover(s.Env())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(disc.Accounts) != 1 {
		t.Fatalf("discovered %d accounts, want 1", len(disc.Accounts))
	}
	acc := disc.Accounts[0].Account
	if acc.ID != "client" || !acc.Default {
		t.Errorf("acc = %+v, want ID client, Default true", acc)
	}
}

func TestDiscoverIgnoresNonIdentityIncludes(t *testing.T) {
	s := testenv.New(t)
	s.Write(".gitconfig-colors", `[color]
	ui = auto
`)
	s.Write(".gitconfig", `[user]
	name = Alice
	email = alice@example.com
[includeIf "gitdir:~/misc/"]
	path = ~/.gitconfig-colors
`)

	disc, err := Discover(s.Env())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(disc.Accounts) != 1 {
		t.Fatalf("discovered %d accounts, want 1 (base identity only)", len(disc.Accounts))
	}
}

func TestDeriveIDAndSanitize(t *testing.T) {
	used := map[string]bool{"work": true}
	cases := []struct {
		file    string
		folders []string
		want    string
	}{
		{"/home/user/.gitconfig-work", []string{"~/work/"}, "work-2"},
		{"/home/user/work.gitconfig", nil, "work-2"},
		{"/home/user/.gitconfig_client", nil, "client"},
		{"/home/user/.config/git/corp.gitconfig", nil, "corp"},
		{"/home/user/.gitconfig", []string{"~/projects/client-alpha/"}, "client-alpha"},
		{"/home/user/weird---name.gitconfig", nil, "weird-name"},
	}
	for _, c := range cases {
		got := deriveID(c.file, c.folders, used)
		if got != c.want {
			t.Errorf("deriveID(%q, %v) = %q, want %q", c.file, c.folders, got, c.want)
		}
	}
}
