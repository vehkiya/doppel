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
	acc := &Account{
		ID: "work", Name: "Jane Doe", Email: "jane@acme.com", Hosts: []string{"github.com", "gitlab.com"},
		GitHubUser: "jane-acme", Folders: []string{"~/work/", "~/clients/"}, Default: true,
		AuthKey: "~/.ssh/id_work", SigningKey: "~/.ssh/id_work_signing.pub", SignCommits: true, SignTags: false,
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
	want := []string{KeyName, KeyEmail, KeySigningKey, KeyGPGFormat, KeyCommitSign, KeyTagSign, KeySSHCommand}
	if got := IdentityKeys(); !slices.Equal(got, want) {
		t.Errorf("IdentityKeys = %v, want %v", got, want)
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
