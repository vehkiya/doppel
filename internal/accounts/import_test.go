package accounts

import (
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
