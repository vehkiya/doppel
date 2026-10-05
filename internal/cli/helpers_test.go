package cli

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/testenv"
)

const accountsDir = ".config/doppel/accounts/"

// sandbox runs doppel inside a testenv.Sandbox and captures its output.
type sandbox struct {
	*testenv.Sandbox
	stdin  string // what the next command reads from stdin
	tty    bool   // whether the next command can ask confirmations
	stdout bytes.Buffer
	stderr bytes.Buffer
	copied []string // what the commands put on the clipboard
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	return &sandbox{Sandbox: testenv.New(t)}
}

// run runs doppel with args from the home directory and returns its exit status.
func (s *sandbox) run(args ...string) int {
	s.T.Helper()
	return s.runIn(s.Home, args...)
}

func (s *sandbox) runIn(cwd string, args ...string) int {
	s.T.Helper()
	s.stdout.Reset()
	s.stderr.Reset()
	a := &app{
		env:         s.Env(),
		cwd:         cwd,
		stdin:       bufio.NewReader(strings.NewReader(s.stdin)),
		stdout:      &s.stdout,
		stderr:      &s.stderr,
		interactive: s.tty,
		// Generated keys get an empty passphrase, as there's nobody to type one.
		generate: func(path, comment string) error {
			empty := ""
			return keys.Generate(path, comment, &empty, nil, io.Discard, io.Discard)
		},
		copy: func(text string) error {
			s.copied = append(s.copied, text)
			return nil
		},
	}
	return a.run(args)
}

// mustRun runs doppel and fails the test unless it exits 0. It returns stdout.
func (s *sandbox) mustRun(args ...string) string {
	s.T.Helper()
	if code := s.run(args...); code != 0 {
		s.T.Fatalf("doppel %s: exit status %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, s.stdout.String(), s.stderr.String())
	}
	return s.stdout.String()
}

// mustFail runs doppel and fails the test unless it exits with code. It
// returns stderr.
func (s *sandbox) mustFail(code int, args ...string) string {
	s.T.Helper()
	if got := s.run(args...); got != code {
		s.T.Fatalf("doppel %s: exit status %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), got, code, s.stdout.String(), s.stderr.String())
	}
	return s.stderr.String()
}

func (s *sandbox) addAccount(id, email string, extra ...string) {
	s.T.Helper()
	s.mustRun(append([]string{"add", id, "--name", "Jane Doe", "--email", email}, extra...)...)
}
