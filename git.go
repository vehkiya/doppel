package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Git 2.34 added SSH commit signing, which every account file configures.
const minGitMajor, minGitMinor = 2, 34

// gitError is a git command that ran but exited with an error.
type gitError struct {
	args   []string
	code   int
	stderr string
}

func (e *gitError) Error() string {
	msg := fmt.Sprintf("git %s: exit status %d", strings.Join(e.args, " "), e.code)
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}

// gitExitCode returns the exit status of a failed git command, or -1 when
// git didn't run at all.
func gitExitCode(err error) int {
	var gerr *gitError
	if errors.As(err, &gerr) {
		return gerr.code
	}
	return -1
}

// runGit runs git with args in dir ("" for the current directory) and
// returns its standard output.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // fixed binary; arguments are built by doppel
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), &gitError{args: args, code: exitErr.ExitCode(), stderr: strings.TrimSpace(stderr.String())}
		}
		return "", fmt.Errorf("running git: %w", err)
	}
	return stdout.String(), nil
}

// configFile runs `git config --file path args...`.
func configFile(path string, args ...string) error {
	_, err := runGit("", append([]string{"config", "--file", path}, args...)...)
	return err
}

// configEntry is one value from `git config --list`.
type configEntry struct {
	Scope, Origin string // set only with --show-scope --show-origin
	Key, Value    string // Key is lowercased by Git, except for subsections
}

// parseConfigList parses `git config --list --null` output, which may also
// carry --show-scope and --show-origin fields. A key written without a value
// is a boolean true to Git, so its value is "true".
func parseConfigList(out string, withOrigin bool) []configEntry {
	out = strings.TrimSuffix(out, "\x00")
	if out == "" {
		return nil
	}
	fields := strings.Split(out, "\x00")
	step := 1
	if withOrigin {
		step = 3
	}
	var entries []configEntry
	for i := 0; i+step <= len(fields); i += step {
		var e configEntry
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

// readConfigFile returns every value in a Git config file, keyed by the
// lowercased name Git reports. A missing file has no values.
func readConfigFile(path string) (map[string][]string, error) {
	values := map[string][]string{}
	if !fileExists(path) {
		return values, nil
	}
	out, err := runGit("", "config", "--file", path, "--list", "--null")
	if err != nil {
		return nil, err
	}
	for _, e := range parseConfigList(out, false) {
		values[e.Key] = append(values[e.Key], e.Value)
	}
	return values, nil
}

var gitVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// parseGitVersion reads the major and minor version from `git version`
// output such as "git version 2.39.5 (Apple Git-154)".
func parseGitVersion(out string) (major, minor int, ok bool) {
	m := gitVersionPattern.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// checkGit fails when git is missing or older than doppel supports.
func checkGit() error {
	out, err := runGit("", "version")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("git not found: doppel needs Git %d.%d or newer", minGitMajor, minGitMinor)
		}
		return err
	}
	major, minor, ok := parseGitVersion(out)
	if ok && (major < minGitMajor || (major == minGitMajor && minor < minGitMinor)) {
		return fmt.Errorf("%s is too old: doppel needs Git %d.%d or newer for SSH signing", strings.TrimSpace(out), minGitMajor, minGitMinor)
	}
	return nil
}
