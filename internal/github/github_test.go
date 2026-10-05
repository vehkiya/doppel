package github

import (
	"slices"
	"testing"
)

func TestMissingScopes(t *testing.T) {
	cases := []struct {
		granted []string
		kinds   []Kind
		want    []string
	}{
		{[]string{"repo", "admin:public_key", "admin:ssh_signing_key"}, []Kind{Authentication, Signing}, nil},
		{[]string{"write:public_key", "write:ssh_signing_key"}, []Kind{Authentication, Signing}, nil},
		{[]string{"repo"}, []Kind{Authentication, Signing}, []string{"admin:public_key", "admin:ssh_signing_key"}},
		{[]string{"admin:public_key"}, []Kind{Authentication, Signing, Signing}, []string{"admin:ssh_signing_key"}},
		{nil, []Kind{Signing}, []string{"admin:ssh_signing_key"}},
	}
	for _, c := range cases {
		if got := MissingScopes(c.granted, c.kinds...); !slices.Equal(got, c.want) {
			t.Errorf("MissingScopes(%v, %v) = %v, want %v", c.granted, c.kinds, got, c.want)
		}
	}
}

func TestKnownHost(t *testing.T) {
	cases := map[string]string{
		"github.com":      "github.com",
		"GitHub.com":      "github.com",
		"ssh.github.com":  "github.com", // the port-443 SSH endpoint
		"acme.ghe.com":    "acme.ghe.com",
		"gitlab.com":      "",
		"git.acme.com":    "", // could be GitHub Enterprise Server; only gh can tell
		"gist.github.com": "",
	}
	for host, want := range cases {
		got, ok := KnownHost(host)
		if got != want || ok != (want != "") {
			t.Errorf("KnownHost(%q) = %q, %v; want %q", host, got, ok, want)
		}
	}
}
