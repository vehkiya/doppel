package accounts

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
)

func TestSSHCommand(t *testing.T) {
	cases := map[keys.Ref]string{
		"":                    "ssh",
		"~/.ssh/id_ed25519_w": "ssh -i ~/.ssh/id_ed25519_w -o IdentitiesOnly=yes",
		"~/my keys/id":        "ssh -i '~/my keys/id' -o IdentitiesOnly=yes",
		"/keys/it's":          `ssh -i '/keys/it'\''s' -o IdentitiesOnly=yes`,
	}
	for key, want := range cases {
		if got := SSHCommand(key); got != want {
			t.Errorf("sshCommand(%q) = %q, want %q", key, got, want)
		}
	}
}

// Every field an account file holds comes back as it was saved: a managed
// key whose value and read disagree would lose a setting on the next save.
func TestManagedKeysRoundTrip(t *testing.T) {
	acc := &Account{ //nolint:gosec // test account settings, not a hardcoded credential
		ID: "work", Name: "Jane Doe", Email: "jane@acme.com", Protocol: ProtocolSSH,
		HTTPSUser: "jane-acme", CredentialHelper: "!gh auth git-credential",
		Hosts:      []string{"github.com", "gitlab.com"},
		GitHubUser: "jane-acme", Folders: []string{"~/work/", "~/clients/"}, Default: true,
		AuthKey: "~/.ssh/id_work", SigningKey: "~/.ssh/id_work_signing.pub", SignCommits: true, SignTags: false,
		Retired: []RetiredSigner{{Until: "20260101120000", Email: "jane@old.dev", Key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"}},
	}
	values := map[string][]string{}
	for _, s := range acc.Settings() {
		values[strings.ToLower(s.Key)] = s.Values
	}
	if got := fromConfig("work", "", values); !reflect.DeepEqual(got, acc) {
		t.Errorf("read back\n%+v\nwant\n%+v", got, acc)
	}

	var settings []string
	for _, s := range acc.Settings() {
		settings = append(settings, s.Key)
	}
	var managed []string
	for _, k := range Managed {
		managed = append(managed, k.Key)
	}
	if !slices.Equal(settings, managed) {
		t.Errorf("Settings writes %v, want every managed key: %v", settings, managed)
	}
	want := []string{KeyName, KeyEmail, KeySigningKey, KeyGPGFormat, KeyCommitSign, KeyTagSign, KeySSHCommand, KeyCredentialUser}
	if got := IdentityKeys(); !slices.Equal(got, want) {
		t.Errorf("IdentityKeys = %v, want %v", got, want)
	}
}

func TestValidateRetiredSigners(t *testing.T) {
	good := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
	cases := map[string]string{
		"20260101120000 jane@old.dev " + good:                    "",
		"20260101 jane@old.dev " + good:                          "should be <YYYYMMDDHHMMSS>",
		"20261399120000 jane@old.dev " + good:                    "isn't a valid time",
		`20260101120000 "jane@old.dev",cert-authority ` + good:   "isn't a valid email address",
		"20260101120000 jane@old.dev ssh-ed25519 not-base64!":    "isn't an SSH public key",
		"20260101120000 jane@old.dev " + good + " and-a-comment": "should be <YYYYMMDDHHMMSS>",
	}
	for value, want := range cases {
		acc := &Account{ID: "work", Name: "Jane", Email: "jane@acme.com", File: "/x/work.gitconfig", Retired: []RetiredSigner{parseRetired(value)}}
		err := acc.Validate()
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", value, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "doppel.retiredSigner")):
			t.Errorf("%q: %v, want an error about %q", value, err, want)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	for _, bad := range []string{"jane", "jane doe@x.io", "<jane@x.io>", `"jane"@x.io`, "jane@x.io,*", "*@x.io", "jane?@x.io", "!jane@x.io"} {
		if ValidateEmail(bad) == nil {
			t.Errorf("ValidateEmail(%q) accepted it", bad)
		}
	}
	for _, good := range []string{"jane@acme.com", "jane.doe+git@mail.acme.co.uk", "1234+jane@users.noreply.github.com"} {
		if err := ValidateEmail(good); err != nil {
			t.Errorf("ValidateEmail(%q) = %v", good, err)
		}
	}
}

func TestValidateAll(t *testing.T) {
	env := &paths.Env{Home: "/home/jane", GOOS: "linux"}
	acc := func(id string, def bool, folders ...string) *Account {
		return &Account{ID: id, Name: "Jane", Email: id + "@x.io", Default: def, Folders: folders}
	}
	cases := []struct {
		accounts []*Account
		want     string
	}{
		{[]*Account{acc("a", true), acc("b", false)}, ""},
		{[]*Account{acc("a", true), acc("b", true)}, "all marked as the default"},
		{[]*Account{acc("a", false), acc("a", false)}, "two accounts named a"},
		{[]*Account{acc("a", false, "~/x/"), acc("b", false, "~/x/")}, "bound to both a and b"},
		{[]*Account{acc("a", false, "~/x/", "~/x/")}, "lists ~/x/ twice"},
	}
	for _, c := range cases {
		err := ValidateAll(env, c.accounts)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("unexpected error: %v", err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("error = %v, want one mentioning %q", err, c.want)
		}
	}
}

func TestParseProtocol(t *testing.T) {
	cases := map[string]Protocol{
		"":       ProtocolSSH,
		"ssh":    ProtocolSSH,
		"SSH":    ProtocolSSH,
		"https":  ProtocolHTTPS,
		"HTTPS":  ProtocolHTTPS,
		"http":   ProtocolHTTPS,
		"both":   ProtocolBoth,
		"hybrid": ProtocolBoth,
		"all":    ProtocolBoth,
	}
	for input, want := range cases {
		got, err := ParseProtocol(input)
		if err != nil || got != want {
			t.Errorf("ParseProtocol(%q) = %q, %v; want %q, nil", input, got, err, want)
		}
	}
	for _, bad := range []string{"ftp", "git", "invalid"} {
		if _, err := ParseProtocol(bad); err == nil {
			t.Errorf("ParseProtocol(%q) accepted invalid protocol", bad)
		}
	}
}

func TestProtocolSettingsAndResets(t *testing.T) {
	sshAcc := &Account{
		ID: "ssh-acc", Name: "Jane", Email: "jane@ssh.io", Protocol: ProtocolSSH,
		AuthKey: "~/.ssh/id_work",
	}
	httpsAcc := &Account{ //nolint:gosec // test account settings, not a hardcoded credential
		ID: "https-acc", Name: "Jane", Email: "jane@https.io", Protocol: ProtocolHTTPS,
		AuthKey: "~/.ssh/id_work", HTTPSUser: "jane-https", CredentialHelper: "!gh auth git-credential",
	}
	hybridAcc := &Account{
		ID: "hybrid-acc", Name: "Jane", Email: "jane@hybrid.io", Protocol: ProtocolBoth,
		AuthKey: "~/.ssh/id_work", HTTPSUser: "jane-hybrid",
	}

	settingMap := func(a *Account) map[string]string {
		m := map[string]string{}
		for _, s := range a.Settings() {
			if len(s.Values) > 0 {
				m[s.Key] = s.Values[0]
			}
		}
		return m
	}

	// SSH account must have SSH command and reset credential.username & credential.helper.
	sshMap := settingMap(sshAcc)
	if !strings.HasPrefix(sshMap[KeySSHCommand], "ssh -i") {
		t.Errorf("sshAcc core.sshCommand = %q, want ssh -i ...", sshMap[KeySSHCommand])
	}
	if sshMap[KeyCredentialUser] != "" {
		t.Errorf("sshAcc credential.username = %q, want empty string reset", sshMap[KeyCredentialUser])
	}
	if sshMap[KeyCredentialHelperGit] != "" {
		t.Errorf("sshAcc credential.helper = %q, want empty string reset", sshMap[KeyCredentialHelperGit])
	}

	// HTTPS account must reset core.sshCommand to plain "ssh" even if AuthKey is set,
	// and write credential.username and credential.helper.
	httpsMap := settingMap(httpsAcc)
	if httpsMap[KeySSHCommand] != "ssh" {
		t.Errorf("httpsAcc core.sshCommand = %q, want 'ssh' reset", httpsMap[KeySSHCommand])
	}
	if httpsMap[KeyCredentialUser] != "jane-https" {
		t.Errorf("httpsAcc credential.username = %q, want 'jane-https'", httpsMap[KeyCredentialUser])
	}
	if httpsMap[KeyCredentialHelperGit] != "!gh auth git-credential" {
		t.Errorf("httpsAcc credential.helper = %q, want '!gh auth git-credential'", httpsMap[KeyCredentialHelperGit])
	}

	// Hybrid account must have both core.sshCommand and credential.username.
	hybridMap := settingMap(hybridAcc)
	if !strings.HasPrefix(hybridMap[KeySSHCommand], "ssh -i") {
		t.Errorf("hybridAcc core.sshCommand = %q, want ssh -i ...", hybridMap[KeySSHCommand])
	}
	if hybridMap[KeyCredentialUser] != "jane-hybrid" {
		t.Errorf("hybridAcc credential.username = %q, want 'jane-hybrid'", hybridMap[KeyCredentialUser])
	}
}
