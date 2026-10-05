package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox runs doppel and real git against a temporary home directory, with
// every environment variable that could leak in the developer's own Git
// setup cleared.
type sandbox struct {
	t      *testing.T
	home   string
	stdin  string // what the next command reads from stdin
	tty    bool   // whether the next command can ask confirmations
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newSandbox(t *testing.T) *sandbox {
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
	} {
		t.Setenv(key, "") // registers the original value for restoring
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	return &sandbox{t: t, home: home}
}

func (s *sandbox) env() *Env {
	s.t.Helper()
	env, err := loadEnv()
	if err != nil {
		s.t.Fatal(err)
	}
	return env
}

// run runs doppel with args from the home directory and returns its exit status.
func (s *sandbox) run(args ...string) int {
	s.t.Helper()
	return s.runIn(s.home, args...)
}

func (s *sandbox) runIn(cwd string, args ...string) int {
	s.t.Helper()
	s.stdout.Reset()
	s.stderr.Reset()
	a := &app{
		env:         s.env(),
		cwd:         cwd,
		stdin:       bufio.NewReader(strings.NewReader(s.stdin)),
		stdout:      &s.stdout,
		stderr:      &s.stderr,
		interactive: s.tty,
	}
	return a.run(args)
}

// mustRun runs doppel and fails the test unless it exits 0. It returns stdout.
func (s *sandbox) mustRun(args ...string) string {
	s.t.Helper()
	if code := s.run(args...); code != 0 {
		s.t.Fatalf("doppel %s: exit status %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, s.stdout.String(), s.stderr.String())
	}
	return s.stdout.String()
}

// mustFail runs doppel and fails the test unless it exits with code. It
// returns stderr.
func (s *sandbox) mustFail(code int, args ...string) string {
	s.t.Helper()
	if got := s.run(args...); got != code {
		s.t.Fatalf("doppel %s: exit status %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), got, code, s.stdout.String(), s.stderr.String())
	}
	return s.stderr.String()
}

func (s *sandbox) path(rel string) string { return filepath.Join(s.home, rel) }

func (s *sandbox) write(rel, content string) {
	s.t.Helper()
	path := s.path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) read(rel string) string {
	s.t.Helper()
	data, err := os.ReadFile(s.path(rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(data)
}

func (s *sandbox) exists(rel string) bool {
	_, err := os.Lstat(s.path(rel))
	return err == nil
}

func (s *sandbox) mkdir(rel string) string {
	s.t.Helper()
	path := s.path(rel)
	if err := os.MkdirAll(path, 0700); err != nil {
		s.t.Fatal(err)
	}
	return path
}

// gitInit creates a repo at rel and returns its path.
func (s *sandbox) gitInit(rel string) string {
	s.t.Helper()
	dir := s.mkdir(rel)
	if _, err := runGit(dir, "init", "-q"); err != nil {
		s.t.Fatal(err)
	}
	return dir
}

// gitConfig returns the value Git uses for key in dir, or "" when unset.
func (s *sandbox) gitConfig(dir, key string) string {
	s.t.Helper()
	out, err := runGit(dir, "config", "--get", key)
	if err != nil && gitExitCode(err) != 1 {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// setConfig sets a key in a config file relative to the home directory.
func (s *sandbox) setConfig(rel, key, value string) {
	s.t.Helper()
	if err := configFile(s.path(rel), key, value); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) addAccount(id, email string, extra ...string) {
	s.t.Helper()
	s.mustRun(append([]string{"add", id, "--name", "Jane Doe", "--email", email}, extra...)...)
}
