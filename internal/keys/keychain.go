package keys

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/proc"
)

// KeychainSupported reports whether the ssh on PATH is Apple's, the only one
// that can keep passphrases in the macOS Keychain. Other builds, such as
// Homebrew's, refuse the UseKeychain option. Nothing connects.
func KeychainSupported() bool {
	cmd, finish := proc.Command(proc.Local, "ssh", "-G", "-F", os.DevNull, "-o", "UseKeychain=yes", noSuchHost)
	return finish(cmd.Run()) == nil
}

// AddToKeychain loads key into the agent with Apple's ssh-add and keeps its
// passphrase in the macOS Keychain. ssh-add asks for the passphrase on the
// terminal through stdin, stdout and stderr.
func AddToKeychain(key string, stdin io.Reader, stdout, stderr io.Writer) error {
	// ssh-add asks for the passphrase, so it has no time limit.
	cmd := exec.Command("ssh-add", "--apple-use-keychain", key) //nolint:gosec // fixed binary; arguments built by doppel
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh-add: %w", err)
	}
	return nil
}

// AddsKeysToAgent reports whether an ssh config file sets AddKeysToAgent for
// host, so ssh loads a key into the agent once its passphrase is given.
func AddsKeysToAgent(sshConfig, host string) bool {
	for _, line := range resolvedConfig(sshConfig, host) {
		if v, ok := strings.CutPrefix(line, "addkeystoagent "); ok {
			v = strings.TrimSpace(v)
			return v != "false" && v != "no"
		}
	}
	return false
}

// UsesKeychain reports whether an ssh config file sets UseKeychain for
// host, so Apple's ssh reads key passphrases from the Keychain. ssh -G
// doesn't print UseKeychain, so doppel reads the file itself, as ssh does:
// the first value that applies wins, and Host blocks and Include count. A
// Match block is taken to apply, so an unusual setup isn't reported as
// missing the setting.
func UsesKeychain(sshConfig, host string) bool {
	v, _ := scanUseKeychain(sshConfig, strings.ToLower(host), filepath.Dir(sshConfig), true, 0)
	return v
}

// scanUseKeychain reads one config file. active says whether the lines
// before the first Host or Match apply; an Include in a block that doesn't
// apply never applies either, as in ssh.
func scanUseKeychain(file, host, sshDir string, active bool, depth int) (value, found bool) {
	if depth > 16 { // ssh's own limit, which also stops Include loops
		return false, false
	}
	f, err := os.Open(filepath.Clean(file)) //nolint:gosec // the user's ssh config, or a file it includes
	if err != nil {
		return false, false
	}
	defer func() { _ = f.Close() }()
	never := !active
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, args := configLine(sc.Text())
		switch key {
		case "host":
			active = !never && hostMatches(host, args)
		case "match":
			active = !never
		case "include":
			if !active {
				continue
			}
			for _, pattern := range args {
				if strings.HasPrefix(pattern, "~/") {
					pattern = filepath.Join(filepath.Dir(sshDir), pattern[2:])
				} else if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(sshDir, pattern)
				}
				matches, _ := filepath.Glob(pattern)
				for _, m := range matches {
					if v, ok := scanUseKeychain(m, host, sshDir, true, depth+1); ok {
						return v, true
					}
				}
			}
		case "usekeychain":
			if active && len(args) > 0 {
				v := strings.ToLower(args[0])
				return v == "yes" || v == "true", true
			}
		}
	}
	return false, false
}

// configLine splits an ssh config line into its lowercased keyword and its
// arguments: "Key value", "Key=value" and quoted arguments.
func configLine(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil
	}
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return strings.ToLower(line), nil
	}
	key := strings.ToLower(line[:end])
	rest := strings.TrimLeft(line[end:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	var args []string
	for rest != "" {
		var arg string
		if rest[0] == '"' {
			arg, rest, _ = strings.Cut(rest[1:], `"`)
		} else {
			i := strings.IndexAny(rest, " \t")
			if i < 0 {
				i = len(rest)
			}
			arg, rest = rest[:i], rest[i:]
		}
		if strings.HasPrefix(arg, "#") {
			break
		}
		args = append(args, arg)
		rest = strings.TrimLeft(rest, " \t")
	}
	return key, args
}

// hostMatches matches host against a Host line's patterns as ssh does: one
// pattern must match, and none of the negated ones (!pattern) may.
func hostMatches(host string, patterns []string) bool {
	matched := false
	for _, p := range patterns {
		for _, alt := range strings.Split(strings.ToLower(p), ",") {
			negated := strings.HasPrefix(alt, "!")
			ok, _ := path.Match(strings.TrimPrefix(alt, "!"), host)
			if ok && negated {
				return false
			}
			matched = matched || ok
		}
	}
	return matched
}
