// Package update checks for new doppel releases and installs them. It only
// installs a release whose checksums.txt carries a valid signature from a
// key built into doppel, and whose archive matches those checksums.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vehkiya/doppel/internal/atomicfile"
)

const (
	repo           = "vehkiya/doppel"
	checksumsAsset = "checksums.txt"
	signatureAsset = "checksums.txt.sig"
	cacheTTL       = 6 * time.Hour

	// NoCheckEnv turns off the browser's background update check when set.
	NoCheckEnv = "DOPPEL_NO_UPDATE_CHECK"

	maxReleaseInfoSize = 1 << 20
	maxChecksumsSize   = 1 << 20
	maxSignatureSize   = 4 << 10
	maxArchiveSize     = 64 << 20
	maxBinarySize      = 128 << 20
)

// TrustedKeys are the base64 Ed25519 public keys allowed to sign a release's
// checksums.txt; the release workflow signs with DOPPEL_SIGNING_KEY. To
// rotate, add the new key here and ship a release still signed with the old
// one before switching the secret: installed binaries only trust the keys
// they were built with.
var TrustedKeys = []string{
	"8X0lpZPfUxaAOalCYMGbqNR3GqRayJqlm2Wnnw9m1C8=",
}

// releasesAPIURL and executablePath can be swapped in tests.
var (
	releasesAPIURL = "https://api.github.com/repos/" + repo + "/releases/latest"
	executablePath = currentExecutable
)

// SetReleasesURLForTest swaps releasesAPIURL for tests and returns a restore func.
func SetReleasesURLForTest(url string) func() {
	old := releasesAPIURL
	releasesAPIURL = url
	return func() { releasesAPIURL = old }
}

// SetExecutablePathForTest swaps executablePath for tests and returns a restore func.
func SetExecutablePathForTest(fn func() (string, error)) func() {
	old := executablePath
	executablePath = fn
	return func() { executablePath = old }
}

// Asset is one downloadable file in a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Release is a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Latest asks GitHub for the latest release, and whether it's newer than
// currentVersion.
func Latest(currentVersion string) (*Release, bool, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, releasesAPIURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", fmt.Sprintf("doppel/%s (%s/%s)", currentVersion, runtime.GOOS, runtime.GOARCH))
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxReleaseInfoSize)
	if err != nil {
		return nil, false, err
	}
	var rel Release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, false, err
	}
	return &rel, IsNewer(currentVersion, rel.TagName), nil
}

// cache remembers the last check, so the browser asks GitHub at most every
// cacheTTL and stays clear of its rate limits.
type cache struct {
	CheckedAt     int64  `json:"checked_at"`
	LatestVersion string `json:"latest_version"`
}

// CachePath returns the path to the update check cache file.
func CachePath() (string, error) {
	return cachePath()
}

func cachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "doppel", "update-check.json"), nil
}

func writeCache(latest string) {
	path, err := cachePath()
	if err != nil {
		return
	}
	data, err := json.Marshal(cache{CheckedAt: time.Now().Unix(), LatestVersion: latest})
	if err != nil {
		return
	}
	_ = atomicfile.Write(path, data, 0600)
}

// LatestCached is Latest through a cache of the last check, for the
// browser's background check.
func LatestCached(currentVersion string) (latest string, newer bool, err error) {
	if path, err := cachePath(); err == nil {
		if data, err := os.ReadFile(filepath.Clean(path)); err == nil { //nolint:gosec // doppel's own cache file
			var c cache
			if json.Unmarshal(data, &c) == nil && c.LatestVersion != "" && time.Since(time.Unix(c.CheckedAt, 0)) < cacheTTL {
				return c.LatestVersion, IsNewer(currentVersion, c.LatestVersion), nil
			}
		}
	}
	rel, newer, err := Latest(currentVersion)
	if err != nil {
		return "", false, err
	}
	writeCache(rel.TagName)
	return rel.TagName, newer, nil
}

// CheckDisabled reports whether the background check should be skipped:
// when the user turned it off, or for a development build with no release
// to compare with.
func CheckDisabled(currentVersion string) bool {
	return os.Getenv(NoCheckEnv) != "" || currentVersion == "dev"
}

// IsNewer compares versions such as v0.4.1 and v0.5.0. A release is newer
// than a pre-release of the same version, such as v0.5.0-rc1, or a Go
// pseudo-version like v0.0.0-20261005165755-2446bc0d13e5 from a local build.
func IsNewer(current, latest string) bool {
	curr := strings.TrimPrefix(strings.TrimSpace(current), "v")
	lat := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if lat == "" {
		return false
	}
	if curr == "dev" || curr == "none" || curr == "" {
		return true
	}
	c, l := semverParts(curr), semverParts(lat)
	for i := range 3 {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return isPrerelease(curr) && !isPrerelease(lat)
}

func isPrerelease(v string) bool {
	v, _, _ = strings.Cut(v, "+")
	return strings.Contains(v, "-")
}

func semverParts(v string) [3]int {
	var parts [3]int
	v, _, _ = strings.Cut(v, "-")
	for i, s := range strings.SplitN(v, ".", 3) {
		parts[i], _ = strconv.Atoi(strings.SplitN(s, "+", 2)[0])
	}
	return parts
}

// Perform installs the latest release over the running binary, unless it's
// already current (force reinstalls anyway). It reports the version it
// installed, or "" when there was nothing to do.
func Perform(currentVersion string, out io.Writer, force bool) (string, error) {
	_, _ = fmt.Fprintf(out, "Checking for the latest release of https://github.com/%s...\n", repo)
	rel, newer, err := Latest(currentVersion)
	if err != nil {
		return "", fmt.Errorf("couldn't check for updates: %w", err)
	}
	if !newer && !force {
		_, _ = fmt.Fprintf(out, "doppel is up to date (%s)\n", currentVersion)
		return "", nil
	}
	_, _ = fmt.Fprintf(out, "Latest release: %s (installed: %s)\n", rel.TagName, currentVersion)

	binary, err := fetchVerifiedBinary(rel, runtime.GOOS, runtime.GOARCH, TrustedKeys, out)
	if err != nil {
		return "", err
	}
	path, err := executablePath()
	if err != nil {
		return "", err
	}
	if err := replaceExecutable(path, binary); err != nil {
		return "", err
	}
	writeCache(rel.TagName)
	return rel.TagName, nil
}

// fetchVerifiedBinary downloads the release archive for goos/goarch and
// returns the doppel binary inside it, but only after checksums.txt has a
// valid signature from one of keys and the archive matches its checksum.
func fetchVerifiedBinary(rel *Release, goos, goarch string, keys []string, out io.Writer) ([]byte, error) {
	archiveName := fmt.Sprintf("doppel_%s_%s_%s.tar.gz", rel.TagName, goos, goarch)
	var archiveURL, checksumsURL, signatureURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case archiveName:
			archiveURL = a.BrowserDownloadURL
		case checksumsAsset:
			checksumsURL = a.BrowserDownloadURL
		case signatureAsset:
			signatureURL = a.BrowserDownloadURL
		}
	}
	switch {
	case archiveURL == "":
		return nil, fmt.Errorf("release %s has no build for %s/%s", rel.TagName, goos, goarch)
	case checksumsURL == "":
		return nil, fmt.Errorf("release %s has no %s; refusing to install an unverified binary", rel.TagName, checksumsAsset)
	case signatureURL == "":
		return nil, fmt.Errorf("release %s is not signed (no %s); refusing to install an unverified binary", rel.TagName, signatureAsset)
	}

	_, _ = fmt.Fprintln(out, "Verifying the release signature...")
	sums, err := download(checksumsURL, maxChecksumsSize)
	if err != nil {
		return nil, fmt.Errorf("couldn't download checksums: %w", err)
	}
	sig, err := download(signatureURL, maxSignatureSize)
	if err != nil {
		return nil, fmt.Errorf("couldn't download the signature: %w", err)
	}
	if err := verifySignature(sums, sig, keys); err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}
	_, _ = fmt.Fprintln(out, "✓ Signature verified")

	_, _ = fmt.Fprintf(out, "Downloading %s...\n", archiveName)
	archive, err := download(archiveURL, maxArchiveSize)
	if err != nil {
		return nil, fmt.Errorf("couldn't download the release: %w", err)
	}
	if err := verifyChecksum(archive, archiveName, string(sums)); err != nil {
		return nil, fmt.Errorf("checksum verification failed: %w", err)
	}
	_, _ = fmt.Fprintln(out, "✓ Checksum verified")
	return extractBinary(archive, "doppel")
}

// verifySignature checks that sigFile holds a base64 Ed25519 signature of
// data made by one of keys (base64 public keys).
func verifySignature(data, sigFile []byte, keys []string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigFile)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("malformed signature")
	}
	for _, k := range keys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
			return nil
		}
	}
	return errors.New("signature doesn't match any trusted release key")
}

// verifyChecksum checks data against the sha256sum-format entry for name.
// A missing entry is an error: doppel never installs an unverified binary.
func verifyChecksum(data []byte, name, checksums string) error {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || filepath.Base(strings.TrimPrefix(fields[1], "*")) != name {
			continue
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != strings.ToLower(fields[0]) {
			return fmt.Errorf("expected %s, got %s", strings.ToLower(fields[0]), got)
		}
		return nil
	}
	return fmt.Errorf("no checksum listed for %s", name)
}

// extractBinary returns the file called name from a .tar.gz archive.
func extractBinary(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s inside the release archive", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == name {
			return readLimited(tr, maxBinarySize)
		}
	}
}

func download(url string, limit int64) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "doppel-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return readLimited(resp.Body, limit)
}

// readLimited reads all of r, failing when it holds more than limit bytes.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("more than %d bytes", limit)
	}
	return data, nil
}

// currentExecutable is the running binary's path, symlinks resolved.
func currentExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("couldn't find the running doppel: %w", err)
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if strings.Contains(path, "go-build") {
		return "", errors.New("can't update a temporary binary run with `go run`")
	}
	return path, nil
}

// replaceExecutable swaps the binary at path for binary, atomically: a
// running binary can be replaced on Linux and macOS, and the next run uses
// the new one.
func replaceExecutable(path string, binary []byte) error {
	err := atomicfile.Write(path, binary, 0755)
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("no permission to replace %s; update with the tool that installed it, or run `sudo doppel update`", path)
	}
	return err
}
