// Package atomicfile replaces files atomically, so a reader never sees one
// half-written.
package atomicfile

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Write writes data to path through a temporary file in the same folder and
// a rename, creating the folder if needed. path is replaced as it is: a
// caller that wants to keep a symlink passes the file it points to.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".tmp."+filepath.Base(path)+"."+hex.EncodeToString(suffix))

	f, err := os.OpenFile(filepath.Clean(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) //nolint:gosec // a temp file next to the target
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("setting permissions on temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	ok = true
	return nil
}
