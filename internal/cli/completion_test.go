package cli

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// completions runs `doppel __complete words...` and returns the candidate
// words (descriptions dropped) and the closing directive.
func (s *sandbox) completions(words ...string) ([]string, string) {
	s.T.Helper()
	out := s.mustRun(append([]string{completeCommand}, words...)...)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	var cands []string
	for _, l := range lines[:len(lines)-1] {
		word, _, _ := strings.Cut(l, "\t")
		cands = append(cands, word)
	}
	return cands, lines[len(lines)-1]
}

func TestShellCompletion(t *testing.T) {
	s := newSandbox(t)
	s.Mkdir("projects/work")
	s.Key("id_work", "jane@acme.com", "")
	s.addAccount("work", "jane@acme.com", "--auth-key", "~/.ssh/id_work", "--folder", "~/projects/work", "--host", "gitlab.acme.com")
	s.addAccount("personal", "jane@personal.dev")

	cases := []struct {
		words      []string
		want       []string // candidates that must be there
		notWant    []string
		directive  string
		exactCount int // when not 0, the number of candidates
	}{
		{[]string{""}, []string{"add", "edit", "completion", "whoami"}, []string{completeCommand, "list", "remove"}, ":words", 0},
		{[]string{"edit", ""}, []string{"personal", "work"}, nil, ":words", 2},
		{[]string{"rename", "work", ""}, nil, nil, ":words", 0},
		{[]string{"edit", "work", "--"}, []string{"--name", "--folder", "--auth-key", "--dry-run"}, []string{"--none"}, ":words", 0},
		// a flag used already isn't offered again, unless it repeats
		{[]string{"edit", "work", "--name", "J", "--folder", "x", "--"}, []string{"--folder", "--email"}, []string{"--name"}, ":words", 0},
		{[]string{"add", "x", "--folder", ""}, nil, nil, ":dirs", 0},
		{[]string{"add", "x", "--auth-key", ""}, nil, nil, ":files", 0},
		{[]string{"edit", "work", "-signing-key", ""}, nil, nil, ":files", 0},
		{[]string{"add", "x", "--host", ""}, []string{"github.com", "gitlab.acme.com"}, nil, ":words", 2},
		// a flag's value given with = doesn't take the next word
		{[]string{"edit", "--name=J", ""}, []string{"work"}, nil, ":words", 0},
		// a bool flag doesn't take a value either
		{[]string{"edit", "--dry-run", ""}, []string{"work"}, nil, ":words", 0},
		{[]string{"bind", ""}, []string{"work"}, nil, ":words", 0},
		{[]string{"bind", "work", ""}, nil, nil, ":dirs", 0},
		{[]string{"unbind", ""}, []string{"~/projects/work/"}, nil, ":words", 1},
		{[]string{"whoami", ""}, nil, nil, ":dirs", 0},
		{[]string{"remove", ""}, []string{"work"}, nil, ":words", 0}, // aliases complete too
		{[]string{"update", "--"}, []string{"--check", "--force"}, nil, ":words", 0},
		{[]string{"ls", "--"}, []string{"--json"}, nil, ":words", 1},
		{[]string{"whoami", "--"}, []string{"--json", "--offline"}, nil, ":words", 2},
		{[]string{"completion", ""}, []string{"zsh", "bash", "fish"}, nil, ":words", 3},
		{[]string{"doctor", ""}, nil, nil, ":words", 0},
		{[]string{"nosuchcommand", ""}, nil, nil, ":words", 0},
	}
	for _, c := range cases {
		got, directive := s.completions(c.words...)
		if directive != c.directive {
			t.Errorf("%q: directive %s, want %s", c.words, directive, c.directive)
		}
		for _, w := range c.want {
			if !slices.Contains(got, w) {
				t.Errorf("%q: %q missing from %q", c.words, w, got)
			}
		}
		for _, w := range c.notWant {
			if slices.Contains(got, w) {
				t.Errorf("%q: %q offered in %q", c.words, w, got)
			}
		}
		if c.exactCount != 0 && len(got) != c.exactCount {
			t.Errorf("%q: %d candidates %q, want %d", c.words, len(got), got, c.exactCount)
		}
	}

	// Accounts come with their emails, for shells that show descriptions.
	if out := s.mustRun(completeCommand, "edit", ""); !strings.Contains(out, "work\tjane@acme.com\n") {
		t.Errorf("no description for work:\n%s", out)
	}
}

// Completion learns each command's flags from the command itself, which
// only works while every command parses its flags before doing anything.
func TestEveryCommandGivesItsFlags(t *testing.T) {
	s := newSandbox(t)
	a := s.newApp(s.Home)
	names := []string{"update", "upgrade", "completion"}
	for name := range a.commands() {
		names = append(names, name)
	}
	for _, name := range names {
		if a.flagsOf(name) == nil {
			t.Errorf("%s doesn't hand completion its flags: does it call parseCommand first?", name)
		}
	}
	if s.stdout.Len() != 0 || s.stderr.Len() != 0 {
		t.Errorf("collecting flags ran a command:\nstdout: %s\nstderr: %s", s.stdout.String(), s.stderr.String())
	}
	for name := range commandSummaries {
		if name != "version" && name != "help" && a.flagsOf(name) == nil {
			t.Errorf("completion offers %s, which isn't a command", name)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	s := newSandbox(t)
	check := map[string][]string{"zsh": {"zsh", "-n"}, "bash": {"bash", "-n"}, "fish": {"fish", "--no-execute"}}
	for shell, cmd := range check {
		script := s.mustRun("completion", shell)
		if !strings.Contains(script, "doppel __complete") {
			t.Errorf("%s script doesn't ask doppel:\n%s", shell, script)
		}
		if _, err := exec.LookPath(cmd[0]); err != nil {
			t.Logf("%s isn't installed; not checking its syntax", shell)
			continue
		}
		run := exec.Command(cmd[0], cmd[1:]...) //nolint:gosec // a fixed shell
		run.Stdin = strings.NewReader(script)
		if out, err := run.CombinedOutput(); err != nil {
			t.Errorf("%s says the script is invalid: %v\n%s", shell, err, out)
		}
	}
	if stderr := s.mustFail(1, "completion", "powershell"); !strings.Contains(stderr, "zsh, bash and fish") {
		t.Errorf("unknown shell: %s", stderr)
	}
	s.mustFail(2, "completion")
}
