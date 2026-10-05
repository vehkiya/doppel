package accounts

import (
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/paths"
)

func TestSSHCommand(t *testing.T) {
	cases := map[string]string{
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
