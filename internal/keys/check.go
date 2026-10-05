package keys

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// LoginResult is what a host said when a key tried to log in.
type LoginResult struct {
	Host     string
	User     string // the account the host recognized, when it said
	Accepted bool   // the host accepted the key
	Problem  string // why the login failed, or why the reply wasn't understood
}

// Greetings Git hosts send on `ssh -T`, with the username they recognized.
var greetings = []*regexp.Regexp{
	regexp.MustCompile(`Hi there, ([^!\s]+)!`),                            // Gitea, Forgejo, Codeberg
	regexp.MustCompile(`Hi ([^!\s]+)! You've successfully authenticated`), // GitHub
	regexp.MustCompile(`Welcome to GitLab, @([^!\s]+)!`),                  // GitLab
	regexp.MustCompile(`logged in as ([^\s.]+)`),                          // Bitbucket
}

// ParseGreeting reads a host's `ssh -T` reply. ok is false when the reply
// isn't a successful login doppel recognizes.
func ParseGreeting(reply string) (user string, ok bool) {
	for _, g := range greetings {
		if m := g.FindStringSubmatch(reply); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// Login runs `ssh -T git@host` with key (ssh's own keys when key is "") and
// reads the greeting. batch stops ssh from asking anything (a passphrase,
// whether to trust an unknown host), so it fails instead of waiting.
func Login(host, key string, batch bool) LoginResult {
	args := []string{"-T", "-o", "ConnectTimeout=10"}
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	if key != "" {
		args = append(args, "-i", key, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, "git@"+host)
	// Hosts refuse a shell, so ssh exits with an error even when the key
	// works; the reply says what happened.
	out, _ := exec.Command("ssh", args...).CombinedOutput() //nolint:gosec // fixed binary; arguments built by doppel
	reply := strings.TrimSpace(string(out))

	res := LoginResult{Host: host}
	if user, ok := ParseGreeting(reply); ok {
		res.User, res.Accepted = user, true
		return res
	}
	switch {
	case strings.Contains(reply, "Permission denied"):
		res.Problem = "the host rejected the key"
	case strings.Contains(reply, "Host key verification failed"):
		res.Problem = "the host's key isn't trusted yet; run `ssh git@" + host + "` once to check and accept it"
	case strings.Contains(reply, "Could not resolve hostname"), strings.Contains(reply, "timed out"), strings.Contains(reply, "Connection refused"):
		res.Problem = "couldn't reach the host"
	case reply == "":
		res.Problem = "no reply from the host"
	default:
		first, _, _ := strings.Cut(reply, "\n")
		res.Problem = "unexpected reply: " + first
	}
	return res
}

// SignCheck signs a test message with key and verifies it against the
// allowed_signers file for email, the way Git signs and verifies commits.
func SignCheck(key, email, allowedSigners string) error {
	dir, err := os.MkdirTemp("", "doppel-sign-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if literal, ok := strings.CutPrefix(key, LiteralPrefix); ok {
		// A literal key can only sign through an agent; ssh-keygen needs it in a file.
		key = filepath.Join(dir, "key.pub")
		if err := os.WriteFile(key, []byte(literal+"\n"), 0600); err != nil {
			return err
		}
	}
	msg := filepath.Join(dir, "message")
	if err := os.WriteFile(msg, []byte("doppel signing check\n"), 0600); err != nil {
		return err
	}

	sign := exec.Command("ssh-keygen", "-Y", "sign", "-n", "git", "-f", key, msg) //nolint:gosec // fixed binary; arguments built by doppel
	var stderr bytes.Buffer
	sign.Stderr = &stderr
	if err := sign.Run(); err != nil {
		return fmt.Errorf("couldn't sign: %s", lastLine(stderr.String(), err))
	}

	input, err := os.Open(msg) //nolint:gosec // the temp file created above
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	verify := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowedSigners, "-I", email, "-n", "git", "-s", msg+".sig") //nolint:gosec // fixed binary; arguments built by doppel
	verify.Stdin = input
	var out bytes.Buffer
	verify.Stdout, verify.Stderr = &out, &out
	if err := verify.Run(); err != nil {
		return fmt.Errorf("signed, but Git couldn't verify it as %s: %s", email, lastLine(out.String(), err))
	}
	return nil
}

func lastLine(s string, fallback error) string {
	s = strings.TrimSpace(s)
	if s == "" {
		var exitErr *exec.ExitError
		if errors.As(fallback, &exitErr) {
			return exitErr.Error()
		}
		return fallback.Error()
	}
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

var opensshVersion = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)

// OpenSSHVersion returns the installed OpenSSH's major and minor version.
func OpenSSHVersion() (major, minor int, ok bool) {
	out, _ := exec.Command("ssh", "-V").CombinedOutput() // ssh -V prints to stderr
	m := opensshVersion.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, false
	}
	_, _ = fmt.Sscan(m[1], &major)
	_, _ = fmt.Sscan(m[2], &minor)
	return major, minor, true
}

// HostIdentityFiles lists the keys an ssh config file adds for host on top
// of the ones every host gets. If an account's own key were rejected, ssh
// would fall back to these and could log in as someone else. ssh -G reads
// the config without connecting, so Include, Match and wildcards are
// handled as ssh handles them.
func HostIdentityFiles(sshConfig, host string) []string {
	files := func(h string) []string {
		out, err := exec.Command("ssh", "-G", "-F", sshConfig, h).Output() //nolint:gosec // fixed binary; arguments built by doppel
		if err != nil {
			return nil
		}
		var list []string
		for _, line := range strings.Split(string(out), "\n") {
			if f, ok := strings.CutPrefix(line, "identityfile "); ok {
				list = append(list, strings.TrimSpace(f))
			}
		}
		return list
	}
	everyone := files("doppel-no-such-host.invalid")
	var extra []string
	for _, f := range files(host) {
		if !slices.Contains(everyone, f) {
			extra = append(extra, f)
		}
	}
	return extra
}
