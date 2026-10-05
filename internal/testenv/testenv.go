// Package testenv gives tests a temporary home directory to run doppel and
// real git in, isolated from the developer's own Git setup.
package testenv

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
)

// Sandbox is a temporary home directory. HOME and XDG_CONFIG_HOME point at
// it, and every environment variable that could leak in the developer's own
// Git or SSH setup is cleared for the duration of the test.
type Sandbox struct {
	T    *testing.T
	Home string
}

// New creates a sandbox for t.
func New(t *testing.T) *Sandbox {
	t.Helper()
	// Resolve the temp dir: on macOS it sits behind the /var -> /private/var symlink.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, key := range []string{
		"GIT_CONFIG_GLOBAL", "GIT_DIR", "GIT_WORK_TREE", "GIT_SSH_COMMAND", "GIT_SSH",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_CEILING_DIRECTORIES",
		// Keep tests away from the developer's ssh-agent and passphrase dialogs.
		"SSH_AUTH_SOCK", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE",
	} {
		t.Setenv(key, "") // registers the original value for restoring
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	return &Sandbox{T: t, Home: home}
}

// Env loads doppel's view of the sandbox's environment.
func (s *Sandbox) Env() *paths.Env {
	s.T.Helper()
	env, err := paths.Load()
	if err != nil {
		s.T.Fatal(err)
	}
	return env
}

// Path returns the absolute path of rel inside the sandbox.
func (s *Sandbox) Path(rel string) string { return filepath.Join(s.Home, rel) }

// Write creates a file, and any missing parent folders.
func (s *Sandbox) Write(rel, content string) {
	s.T.Helper()
	path := s.Path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		s.T.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		s.T.Fatal(err)
	}
}

// Read returns a file's content.
func (s *Sandbox) Read(rel string) string {
	s.T.Helper()
	data, err := os.ReadFile(s.Path(rel))
	if err != nil {
		s.T.Fatal(err)
	}
	return string(data)
}

// Exists reports whether rel exists, without following a final symlink.
func (s *Sandbox) Exists(rel string) bool {
	_, err := os.Lstat(s.Path(rel))
	return err == nil
}

// Mkdir creates a folder and returns its path.
func (s *Sandbox) Mkdir(rel string) string {
	s.T.Helper()
	path := s.Path(rel)
	if err := os.MkdirAll(path, 0700); err != nil {
		s.T.Fatal(err)
	}
	return path
}

// GitInit creates a repo at rel and returns its path.
func (s *Sandbox) GitInit(rel string) string {
	s.T.Helper()
	dir := s.Mkdir(rel)
	if _, err := git.Run(dir, "init", "-q"); err != nil {
		s.T.Fatal(err)
	}
	return dir
}

// GitConfig returns the value Git uses for key in dir, or "" when unset.
func (s *Sandbox) GitConfig(dir, key string) string {
	s.T.Helper()
	out, err := git.Run(dir, "config", "--get", key)
	if err != nil && git.ExitCode(err) != 1 {
		s.T.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// SetConfig sets key in a Git config file relative to the sandbox.
func (s *Sandbox) SetConfig(rel, key, value string) {
	s.T.Helper()
	if err := git.ConfigFile(s.Path(rel), key, value); err != nil {
		s.T.Fatal(err)
	}
}

// Key generates an Ed25519 key at ~/.ssh/<name> and returns its path.
func (s *Sandbox) Key(name, email, passphrase string) string {
	s.T.Helper()
	path := s.Path(".ssh/" + name)
	if err := keys.Generate(path, email, &passphrase, nil, io.Discard, io.Discard); err != nil {
		s.T.Fatal(err)
	}
	return path
}

// FakeCommand puts a shell script named name first on PATH, standing in for
// a real command such as ssh.
func (s *Sandbox) FakeCommand(name, script string) {
	s.T.Helper()
	bin := s.Mkdir("fake-bin")
	path := filepath.Join(bin, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil { //nolint:gosec // the fake command has to be executable
		s.T.Fatal(err)
	}
	if !strings.HasPrefix(os.Getenv("PATH"), bin+string(os.PathListSeparator)) {
		s.T.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
}

// FakeSSH stands in for ssh: it prints reply the way a Git host greets a
// login (on stderr, then exits 1, since hosts refuse a shell), and records
// its arguments in ~/ssh-calls.
func (s *Sandbox) FakeSSH(reply string) {
	s.T.Helper()
	s.FakeCommand("ssh", `echo "$@" >> "$HOME/ssh-calls"
cat >&2 <<'REPLY'
`+reply+`
REPLY
exit 1`)
}

// OnlyCommands replaces PATH with a folder holding just the named commands,
// plus any fakes, to test what happens when a tool isn't installed.
func (s *Sandbox) OnlyCommands(names ...string) {
	s.T.Helper()
	bin := s.Mkdir("only-bin")
	for _, name := range names {
		real, err := exec.LookPath(name)
		if err != nil {
			s.T.Fatalf("%s isn't installed: %v", name, err)
		}
		if err := os.Symlink(real, filepath.Join(bin, name)); err != nil {
			s.T.Fatal(err)
		}
	}
	path := bin
	if fake := s.Path("fake-bin"); s.Exists("fake-bin") {
		path = fake + string(os.PathListSeparator) + bin
	}
	s.T.Setenv("PATH", path)
}
