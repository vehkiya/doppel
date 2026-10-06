package cli

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/testenv"
	"github.com/vehkiya/doppel/internal/ui"
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

	goos       string   // the system the next command thinks it runs on; "" for the real one
	passphrase string   // the passphrase generated keys get
	keychained []string // the keys whose passphrases went to the macOS Keychain
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
	return s.newApp(cwd).run(args)
}

// newApp builds the app a command runs with: the sandbox's environment,
// s.stdin as input, output captured, and stand-ins for key generation and
// the clipboard.
func (s *sandbox) newApp(cwd string) *app {
	s.T.Helper()
	s.stdout.Reset()
	s.stderr.Reset()
	env := s.Env()
	if s.goos != "" {
		env.GOOS = s.goos
	}
	return &app{
		env:         env,
		cwd:         cwd,
		stdin:       bufio.NewReader(strings.NewReader(s.stdin)),
		stdout:      ui.Writer(&s.stdout),
		stderr:      ui.Writer(&s.stderr),
		interactive: s.tty,
		accessible:  true,
		// Generated keys get s.passphrase, empty unless a test sets one, as
		// there's nobody to type one.
		generate: func(path, comment string) error {
			passphrase := s.passphrase
			return keys.Generate(path, comment, &passphrase, nil, io.Discard, io.Discard)
		},
		keychain: func(path string) error {
			s.keychained = append(s.keychained, path)
			return nil
		},
		copy: func(text string) error {
			s.copied = append(s.copied, text)
			return nil
		},
	}
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
