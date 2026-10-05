// Package git runs the git command, and reads and writes Git config files
// through it.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Git 2.34 added SSH commit signing, which every account file configures.
const MinMajor, MinMinor = 2, 34

// exitError is a git command that ran but exited with an error.
type exitError struct {
	args   []string
	code   int
	stderr string
}

func (e *exitError) Error() string {
	msg := fmt.Sprintf("git %s: exit status %d", strings.Join(e.args, " "), e.code)
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}

// ExitCode returns the exit status of a failed git command, or -1 when
// git didn't run at all.
func ExitCode(err error) int {
	var exitErr *exitError
	if errors.As(err, &exitErr) {
		return exitErr.code
	}
	return -1
}

// Run runs git with args in dir ("" for the current directory), with the
// user's own Git config in effect, and returns its standard output.
func Run(dir string, args ...string) (string, error) {
	return runEnv(dir, nil, args...)
}

// RunAlone runs git without the user's global and system config. doppel
// uses it for commands that only touch one named file, so a broken or
// unusual global config can neither stop doppel nor change what it writes.
func RunAlone(args ...string) (string, error) {
	return runEnv("", aloneEnv, args...)
}

// aloneEnv keeps the user's global and system config out of a git command.
var aloneEnv = []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}

func runEnv(dir string, env []string, args ...string) (string, error) {
	return runInput(dir, env, nil, args...)
}

// runInput runs git with input on its standard input (none when nil).
func runInput(dir string, env []string, input []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // fixed binary; arguments are built by doppel
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), &exitError{args: args, code: exitErr.ExitCode(), stderr: strings.TrimSpace(stderr.String())}
		}
		return "", fmt.Errorf("running git: %w", err)
	}
	return stdout.String(), nil
}

// ConfigFile runs `git config --file path args...`.
func ConfigFile(path string, args ...string) error {
	_, err := RunAlone(append([]string{"config", "--file", path}, args...)...)
	return err
}

// ConfigEntry is one value from `git config --list`.
type ConfigEntry struct {
	Scope, Origin string // set only with --show-scope --show-origin
	Key, Value    string // Key is lowercased by Git, except for subsections
}

// ParseConfigList parses `git config --list --null` output, which may also
// carry --show-scope and --show-origin fields. A key written without a value
// is a boolean true to Git, so its value is "true".
func ParseConfigList(out string, withOrigin bool) []ConfigEntry {
	out = strings.TrimSuffix(out, "\x00")
	if out == "" {
		return nil
	}
	fields := strings.Split(out, "\x00")
	step := 1
	if withOrigin {
		step = 3
	}
	var entries []ConfigEntry
	for i := 0; i+step <= len(fields); i += step {
		var e ConfigEntry
		kv := fields[i]
		if withOrigin {
			e.Scope, e.Origin, kv = fields[i], fields[i+1], fields[i+2]
		}
		key, value, hasValue := strings.Cut(kv, "\n")
		if !hasValue {
			value = "true"
		}
		e.Key, e.Value = key, value
		entries = append(entries, e)
	}
	return entries
}

// ReadConfigFile returns every value in a Git config file, keyed by the
// lowercased name Git reports. A missing file has no values.
func ReadConfigFile(path string) (map[string][]string, error) {
	values := map[string][]string{}
	if _, err := os.Stat(path); err != nil {
		return values, nil
	}
	out, err := RunAlone("config", "--file", path, "--list", "--null")
	if err != nil {
		return nil, err
	}
	for _, e := range ParseConfigList(out, false) {
		values[e.Key] = append(values[e.Key], e.Value)
	}
	return values, nil
}

// ReadConfig returns every value in Git config text, as ReadConfigFile does
// for a file. Git reads the text from its standard input, so no temporary
// file holds it.
func ReadConfig(data []byte) (map[string][]string, error) {
	out, err := runInput("", aloneEnv, data, "config", "--file", "-", "--list", "--null")
	if err != nil {
		return nil, err
	}
	values := map[string][]string{}
	for _, e := range ParseConfigList(out, false) {
		values[e.Key] = append(values[e.Key], e.Value)
	}
	return values, nil
}

// maxIncludeDepth is how deeply Git lets config files include each other.
const maxIncludeDepth = 10

// ReadConfigFileIncludes returns every value in a Git config file and the
// files it includes, in the order Git reads them, each with the file it
// came from (Origin). A missing file has no values.
//
// Git parses each file, but doppel follows the includes itself, so that skip
// can leave out an included file, with everything it includes in turn:
// doppel skips its own files, so a damaged index can't stop it from
// rewriting it. Only [include] paths are followed; conditional includes
// depend on the repo, and outside one they don't apply.
func ReadConfigFileIncludes(path string, skip func(path string) bool) ([]ConfigEntry, error) {
	return readIncludes(path, skip, 0)
}

func readIncludes(path string, skip func(string) bool, depth int) ([]ConfigEntry, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	out, err := RunAlone("config", "--file", path, "--list", "--null")
	if err != nil {
		return nil, err
	}
	var entries []ConfigEntry
	for _, e := range ParseConfigList(out, false) {
		e.Origin = path
		entries = append(entries, e)
		if e.Key != "include.path" {
			continue
		}
		included := includePath(path, e.Value)
		if included == "" || skip(included) {
			continue
		}
		if depth == maxIncludeDepth {
			return nil, fmt.Errorf("%s: config files include each other more than %d deep", included, maxIncludeDepth)
		}
		more, err := readIncludes(included, skip, depth+1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, more...)
	}
	return entries, nil
}

// includePath resolves an include.path value as Git does: "~/" is the home
// directory, and a relative path is relative to the including file's folder.
func includePath(from, value string) string {
	switch {
	case value == "":
		return ""
	case value == "~" || strings.HasPrefix(value, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, value[1:])
	case filepath.IsAbs(value):
		return value
	}
	return filepath.Join(filepath.Dir(from), value)
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// parseVersion reads the major and minor version from `git version`
// output such as "git version 2.39.5 (Apple Git-154)".
func parseVersion(out string) (major, minor int, ok bool) {
	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// Check fails when git is missing or older than doppel supports.
func Check() error {
	out, err := RunAlone("version")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("git not found: doppel needs Git %d.%d or newer", MinMajor, MinMinor)
		}
		return err
	}
	major, minor, ok := parseVersion(out)
	if ok && (major < MinMajor || (major == MinMajor && minor < MinMinor)) {
		return fmt.Errorf("%s is too old: doppel needs Git %d.%d or newer for SSH signing", strings.TrimSpace(out), MinMajor, MinMinor)
	}
	return nil
}

// Version returns the installed Git's version, such as "2.56.0".
func Version() string {
	out, err := RunAlone("version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "git version"))
}
