// Package keys inspects, generates and tests the SSH keys accounts use to
// log in to Git hosts and to sign commits. Paths passed in are absolute;
// callers expand "~/" first.
package keys

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/proc"
)

// ReadPublic returns the "<type> <base64>" part of a public key, read from
// the key's .pub file or from a literal key. The comment is dropped.
func ReadPublic(key Ref) (string, error) {
	line, err := ReadPublicLine(key)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(line)
	return fields[0] + " " + fields[1], nil
}

// ReadPublicLine returns a public key as written, comment included.
func ReadPublicLine(key Ref) (string, error) {
	line, isLiteral := key.Literal()
	if !isLiteral {
		data, err := os.ReadFile(filepath.Clean(key.PublicPath())) //nolint:gosec // a key path the user chose
		if err != nil {
			return "", err
		}
		line, _, _ = strings.Cut(strings.TrimSpace(string(data)), "\n")
	}
	line = strings.TrimSpace(line)
	fields := strings.Fields(line)
	if len(fields) < 2 || !isKeyType(fields[0]) {
		return "", fmt.Errorf("%s doesn't hold an SSH public key", key.Display())
	}
	if _, err := base64.StdEncoding.DecodeString(fields[1]); err != nil {
		return "", fmt.Errorf("%s doesn't hold an SSH public key", key.Display())
	}
	return line, nil
}

func isKeyType(t string) bool {
	return strings.HasPrefix(t, "ssh-") || strings.HasPrefix(t, "ecdsa-") || strings.HasPrefix(t, "sk-")
}

// Fingerprint returns a key's SHA256 fingerprint as OpenSSH prints it.
func Fingerprint(key Ref) (string, error) {
	pub, err := ReadPublic(key)
	if err != nil {
		return "", err
	}
	blob, err := base64.StdEncoding.DecodeString(strings.Fields(pub)[1])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

// Protection is how a private key is kept.
type Protection int

const (
	Unknown     Protection = iota // the key couldn't be read
	Encrypted                     // protected by a passphrase
	Unencrypted                   // anyone who copies the file can use it
	HardwareKey                   // a FIDO security-key handle, useless without the device
	AgentOnly                     // only the public half is on disk; an agent holds the private key
)

func (p Protection) String() string {
	switch p {
	case Encrypted:
		return "passphrase"
	case Unencrypted:
		return "no passphrase"
	case HardwareKey:
		return "security key"
	case AgentOnly:
		return "agent only"
	}
	return "unknown"
}

// CheckProtection reports how the private key behind key is kept, whichever
// half key names: a signing key named by its .pub is checked through the
// private key next to it. A literal, or a .pub with no private key beside
// it, lives in an agent.
func CheckProtection(key Ref) Protection {
	if key.IsLiteral() {
		return AgentOnly
	}
	if pub, err := ReadPublic(key); err == nil && strings.HasPrefix(pub, "sk-") {
		return HardwareKey
	}
	if _, err := os.Stat(key.PrivatePath()); err != nil {
		if _, err := os.Stat(key.PublicPath()); err == nil {
			return AgentOnly
		}
		return Unknown
	}
	// Loading the key with an empty passphrase only works when it has none.
	cmd, finish := proc.Command(proc.Local, "ssh-keygen", "-y", "-P", "", "-f", key.PrivatePath())
	if err := finish(cmd.Run()); err != nil {
		return Encrypted
	}
	return Unencrypted
}

// InAgent reports whether ssh-agent holds key. running is false when there's
// no agent to ask.
func InAgent(key Ref) (loaded, running bool) {
	fp, err := Fingerprint(key)
	if err != nil {
		return false, false
	}
	cmd, finish := proc.Command(proc.Local, "ssh-add", "-l")
	out, err := cmd.Output()
	err = finish(err)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return strings.Contains(string(out), fp), true
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		return false, true // the agent is running but holds no keys
	}
	return false, false
}

// DefaultPath is where doppel generates an account's key: ~/.ssh/id_ed25519_<id>,
// or ~/.ssh/id_ed25519_<id>_signing for a separate signing key.
func DefaultPath(sshDir, id string, signing bool) string {
	name := "id_ed25519_" + id
	if signing {
		name += "_signing"
	}
	return filepath.Join(sshDir, name)
}

// Generate creates an Ed25519 key pair at path, refusing to overwrite an
// existing key. With a nil passphrase, ssh-keygen asks for one on the
// terminal through stdin, stdout and stderr.
func Generate(path, comment string, passphrase *string, stdin io.Reader, stdout, stderr io.Writer) error {
	for _, p := range []string{path, path + ".pub"} {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("%s already exists; doppel never overwrites keys", p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	args := []string{"-t", "ed25519", "-C", comment, "-f", path}
	if passphrase != nil {
		args = append(args, "-q", "-N", *passphrase)
	}
	// ssh-keygen may ask for a passphrase, so it has no time limit.
	cmd := exec.Command("ssh-keygen", args...) //nolint:gosec // fixed binary; arguments built by doppel
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh-keygen: %w", err)
	}
	return nil
}

// Discover lists the keys in sshDir an account could use, sorted: private
// keys, and public keys whose private half isn't on disk because an agent
// holds it.
func Discover(sshDir string) []Ref {
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}
	var found []Ref
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(sshDir, name)
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "-cert.pub") {
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if strings.HasSuffix(name, ".pub") {
			if _, err := os.Stat(strings.TrimSuffix(path, ".pub")); err == nil {
				continue // listed through its private key
			}
			if _, err := ReadPublic(Ref(path)); err == nil {
				found = append(found, Ref(path))
			}
			continue
		}
		if isPrivateKey(path) {
			found = append(found, Ref(path))
		}
	}
	slices.Sort(found)
	return found
}

// isPrivateKey reports whether a file starts like an OpenSSH or PEM private key.
func isPrivateKey(path string) bool {
	f, err := os.Open(filepath.Clean(path)) //nolint:gosec // a file in the user's ~/.ssh
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	first, _, _ := strings.Cut(string(head[:n]), "\n")
	return strings.HasPrefix(first, "-----BEGIN ") && strings.HasSuffix(strings.TrimSpace(first), "PRIVATE KEY-----")
}
