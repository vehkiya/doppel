package store

import (
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/paths"
)

func TestRenderSigners(t *testing.T) {
	block := func(entries ...string) string {
		return signersBegin + "\n" + strings.Join(entries, "\n") + "\n" + signersEnd + "\n"
	}
	teammate := "bob@acme.com namespaces=\"git\" ssh-ed25519 BOB\n"
	cases := []struct {
		name, text string
		entries    []string
		want       string
	}{
		{"new file", "", []string{"a"}, block("a")},
		{"appended after other entries", teammate, []string{"a"}, teammate + "\n" + block("a")},
		{"no trailing newline", strings.TrimSuffix(teammate, "\n"), []string{"a"}, teammate + "\n" + block("a")},
		{"replaced in place", "# top\n" + block("old") + teammate, []string{"a", "b"}, "# top\n" + block("a", "b") + teammate},
		{"removed with its blank line", teammate + "\n" + block("a"), nil, teammate},
		{"removed from the middle", "# top\n" + block("a") + teammate, nil, "# top\n" + teammate},
		{"nothing to do", teammate, nil, teammate},
		{"only doppel's block", block("a"), nil, ""},
	}
	for _, c := range cases {
		if got := renderSigners(c.text, c.entries); got != c.want {
			t.Errorf("%s:\n%q\nwant:\n%q", c.name, got, c.want)
		}
	}
}

func TestRenderIndexPointsGitAtTheSignersFile(t *testing.T) {
	env := &paths.Env{Home: "/home/jane", ConfigDir: "/home/jane/.config", GOOS: "linux"}
	list := []*accounts.Account{{ID: "work", Default: true}}
	got := string(renderIndex(env, list, "/home/jane/.ssh/allowed_signers"))
	if !strings.Contains(got, "[gpg \"ssh\"]\n\tallowedSignersFile = ~/.ssh/allowed_signers\n") {
		t.Errorf("index doesn't set allowedSignersFile:\n%s", got)
	}
	if got := string(renderIndex(env, list, "")); strings.Contains(got, "allowedSignersFile") {
		t.Errorf("index sets allowedSignersFile when it shouldn't:\n%s", got)
	}
}

func TestTrustedSigners(t *testing.T) {
	text := "bob@acme.com namespaces=\"git\" ssh-ed25519 BOB\n\n" + signersBegin + "\n" +
		"jane@acme.com namespaces=\"git\" ssh-ed25519 NEW\n" +
		"jane@old.dev namespaces=\"git\",valid-before=\"20260101120000\" ssh-ed25519 OLD\n" +
		signersEnd + "\n"
	got := trustedSigners(text)
	if len(got) != 1 || got[0] != (signer{"jane@acme.com", "ssh-ed25519 NEW"}) {
		t.Errorf("trustedSigners = %+v; want only doppel's entry without a time limit", got)
	}
}
