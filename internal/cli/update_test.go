package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vehkiya/doppel/internal/update"
	"github.com/vehkiya/doppel/internal/version"
)

func TestUpdateUsage(t *testing.T) {
	s := newSandbox(t)
	if code := s.run("update", "extra"); code != 2 {
		t.Errorf("update extra arg exit code = %d, want 2", code)
	}
	if !strings.Contains(s.stderr.String(), "Usage: doppel update") {
		t.Errorf("expected usage message, got:\n%s", s.stderr.String())
	}
}

func TestUpdateCheck(t *testing.T) {
	s := newSandbox(t)
	oldVer := version.Version
	version.Version = "v0.9.0"
	t.Cleanup(func() { version.Version = oldVer })

	rel := &update.Release{TagName: "v1.0.0"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	}))
	t.Cleanup(srv.Close)

	restoreURL := update.SetReleasesURLForTest(srv.URL)
	t.Cleanup(restoreURL)

	if code := s.run("update", "--check"); code != 0 {
		t.Fatalf("update --check exit code = %d, want 0", code)
	}
	if !strings.Contains(s.stdout.String(), "doppel v1.0.0 is available (installed: v0.9.0)") {
		t.Errorf("expected update available message, got:\n%s", s.stdout.String())
	}

	// Up to date case
	s.stdout.Reset()
	rel.TagName = "v0.9.0"
	if code := s.run("update", "--check"); code != 0 {
		t.Fatalf("update --check exit code = %d, want 0", code)
	}
	if !strings.Contains(s.stdout.String(), "doppel is up to date (v0.9.0)") {
		t.Errorf("expected up to date message, got:\n%s", s.stdout.String())
	}
}

func TestUpdatePerformOutput(t *testing.T) {
	s := newSandbox(t)
	oldVer := version.Version
	version.Version = "v0.9.0"
	t.Cleanup(func() { version.Version = oldVer })

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	oldKeys := update.TrustedKeys
	update.TrustedKeys = []string{pubKeyB64}
	t.Cleanup(func() { update.TrustedKeys = oldKeys })

	installed := filepath.Join(t.TempDir(), "doppel")
	if err := os.WriteFile(installed, []byte("old binary"), 0755); err != nil { //nolint:gosec // stand-in executable binary requires 0755 permissions
		t.Fatal(err)
	}
	restoreExe := update.SetExecutablePathForTest(func() (string, error) {
		return installed, nil
	})
	t.Cleanup(restoreExe)

	archiveName := fmt.Sprintf("doppel_v1.0.0_%s_%s", runtime.GOOS, runtime.GOARCH)
	archiveData := testArchive(t, archiveName, []byte("new binary"))
	checksums := []byte(fmt.Sprintf("%x  %s.tar.gz\n", sha256.Sum256(archiveData), archiveName))
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, checksums)))

	files := map[string][]byte{
		archiveName + ".tar.gz": archiveData,
		"checksums.txt":         checksums,
		"checksums.txt.sig":     sig,
	}

	rel := &update.Release{TagName: "v1.0.0"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "api/latest" {
			_ = json.NewEncoder(w).Encode(rel)
			return
		}
		data, ok := files[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	for name := range files {
		rel.Assets = append(rel.Assets, update.Asset{
			Name:               name,
			BrowserDownloadURL: srv.URL + "/" + name,
		})
	}
	restoreURL := update.SetReleasesURLForTest(srv.URL + "/api/latest")
	t.Cleanup(restoreURL)

	if code := s.run("update"); code != 0 {
		t.Fatalf("update exit code = %d, want 0, stderr:\n%s", code, s.stderr.String())
	}

	out := s.stdout.String()
	if !strings.Contains(out, "UPDATED") {
		t.Errorf("output missing UPDATED badge:\n%s", out)
	}
	if !strings.Contains(out, "Successfully updated doppel to v1.0.0") {
		t.Errorf("output missing success message:\n%s", out)
	}
	if !strings.Contains(out, "Restart doppel to apply the update.") {
		t.Errorf("output missing restart notice:\n%s", out)
	}
}

func TestStartupUpdateCheck(t *testing.T) {
	s := newSandbox(t)
	oldVer := version.Version
	version.Version = "v0.9.0"
	t.Cleanup(func() { version.Version = oldVer })

	// Point release API to a dummy test server so tests never contact real GitHub
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&update.Release{TagName: "v1.0.0"})
	}))
	t.Cleanup(srv.Close)
	restoreURL := update.SetReleasesURLForTest(srv.URL)
	t.Cleanup(restoreURL)

	// Write cache with a newer version
	cacheFile, err := update.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0700); err != nil {
		t.Fatal(err)
	}
	cacheJSON, _ := json.Marshal(map[string]any{
		"checked_at":     time.Now().Unix(),
		"latest_version": "v1.0.0",
	})
	if err := os.WriteFile(cacheFile, cacheJSON, 0600); err != nil {
		t.Fatal(err)
	}

	// 1. With interactive TTY, running "ls" prints update notification to stderr
	s.tty = true
	if code := s.run("ls"); code != 0 {
		t.Fatalf("ls exit code = %d, want 0", code)
	}
	if !strings.Contains(s.stderr.String(), "doppel v1.0.0 is available (installed: v0.9.0). Run `doppel update` to install it.") {
		t.Errorf("expected update notification on stderr, got:\n%s", s.stderr.String())
	}

	// 2. Non-interactive (tty = false) skips notification
	s.stderr.Reset()
	s.tty = false
	if code := s.run("ls"); code != 0 {
		t.Fatalf("ls exit code = %d, want 0", code)
	}
	if strings.Contains(s.stderr.String(), "is available") {
		t.Errorf("unexpected update notification in non-interactive run:\n%s", s.stderr.String())
	}

	// 3. DOPPEL_NO_UPDATE_CHECK=1 disables check even with tty
	s.stderr.Reset()
	s.tty = true
	t.Setenv("DOPPEL_NO_UPDATE_CHECK", "1")
	if code := s.run("ls"); code != 0 {
		t.Fatalf("ls exit code = %d, want 0", code)
	}
	if strings.Contains(s.stderr.String(), "is available") {
		t.Errorf("unexpected update notification when DOPPEL_NO_UPDATE_CHECK=1:\n%s", s.stderr.String())
	}
}

func testArchive(t *testing.T, dir string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range map[string][]byte{dir + "/doppel": binary} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
