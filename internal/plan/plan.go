// Package plan stages file changes so doppel can preview them or write them
// together, with backups and atomic replacement.
package plan

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Plan stages file changes so a command can preview them (--dry-run) or
// apply them together. Files are edited as copies in a temporary directory;
// Apply then backs up and atomically replaces each file that changed.
type Plan struct {
	dir   string
	files map[string]*plannedFile
	order []string
}

type plannedFile struct {
	path    string // the real file
	staged  string // its working copy
	old     []byte
	existed bool
	remove  bool
}

// Change is one file a plan will create, modify or remove.
type Change struct {
	Path     string
	Old, New []byte
	Existed  bool // the file exists now
	Exists   bool // the file will exist afterwards
}

// New starts an empty plan. Close discards it.
func New() (*Plan, error) {
	dir, err := os.MkdirTemp("", "doppel-plan-")
	if err != nil {
		return nil, fmt.Errorf("creating staging directory: %w", err)
	}
	return &Plan{dir: dir, files: map[string]*plannedFile{}}, nil
}

// Close discards the staged copies.
func (p *Plan) Close() { _ = os.RemoveAll(p.dir) }

// Stage returns the working copy of path, creating it from the current file.
// The copy doesn't exist when the file doesn't; `git config --file` creates it.
func (p *Plan) Stage(path string) (string, error) {
	if f, ok := p.files[path]; ok {
		f.remove = false
		return f.staged, nil
	}
	f := &plannedFile{
		path:   path,
		staged: filepath.Join(p.dir, fmt.Sprintf("%d-%s", len(p.order), filepath.Base(path))),
	}
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // doppel's own and Git config files
	switch {
	case err == nil:
		f.old, f.existed = data, true
		if err := os.WriteFile(f.staged, data, 0600); err != nil { //nolint:gosec // working copy inside the plan's own temp directory
			return "", err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	p.files[path] = f
	p.order = append(p.order, path)
	return f.staged, nil
}

// SetContent replaces path's planned content.
func (p *Plan) SetContent(path string, data []byte) error {
	staged, err := p.Stage(path)
	if err != nil {
		return err
	}
	return os.WriteFile(staged, data, 0600) //nolint:gosec // working copy inside the plan's own temp directory
}

// Remove plans to delete path.
func (p *Plan) Remove(path string) error {
	if _, err := p.Stage(path); err != nil {
		return err
	}
	p.files[path].remove = true
	return nil
}

// Content returns path's planned content and whether it will exist.
func (p *Plan) Content(path string) ([]byte, bool, error) {
	read := path
	if f, ok := p.files[path]; ok {
		if f.remove {
			return nil, false, nil
		}
		read = f.staged
	}
	data, err := os.ReadFile(filepath.Clean(read)) //nolint:gosec // doppel's own and Git config files
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Changes lists the files whose content the plan changes, in staging order.
func (p *Plan) Changes() ([]Change, error) {
	var changes []Change
	for _, path := range p.order {
		f := p.files[path]
		data, exists, err := p.Content(path)
		if err != nil {
			return nil, err
		}
		if exists == f.existed && bytes.Equal(data, f.old) {
			continue
		}
		changes = append(changes, Change{Path: path, Old: f.old, New: data, Existed: f.existed, Exists: exists})
	}
	return changes, nil
}

// File operations Apply performs, swapped by tests to inject failures.
var (
	writeFile  = atomicWrite
	removeFile = os.Remove
)

// StaleError means a file changed after the plan read it, so writing the
// plan would undo whatever changed it.
type StaleError struct{ Path string }

func (e *StaleError) Error() string {
	return e.Path + " changed since it was read; run the command again"
}

// undoStep puts one thing Apply did back. what says, for the error message,
// how to do it by hand.
type undoStep struct {
	what string
	undo func() error
}

// Apply writes every change. It first checks that each file still has the
// content the plan read, then backs up every file it will replace or remove
// before touching any of them, writes files in staging order, and removes
// files last.
//
// If a step fails, Apply puts back everything it already did, in reverse:
// files it replaced get their old content, files it created are deleted, and
// the backups return to how they were. Nothing is left half-done, so the
// command can simply be run again once the cause is fixed.
func (p *Plan) Apply() ([]Change, error) {
	changes, err := p.Changes()
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		data, err := os.ReadFile(filepath.Clean(c.Path)) //nolint:gosec // doppel's own and Git config files
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if c.Existed {
				return nil, &StaleError{Path: c.Path}
			}
		case err != nil:
			return nil, err
		case !c.Existed || !bytes.Equal(data, c.Old):
			return nil, &StaleError{Path: c.Path}
		}
	}

	var done []undoStep
	fail := func(err error) ([]Change, error) { return nil, rollBack(err, done) }

	for _, c := range changes {
		if !c.Existed {
			continue
		}
		backup := BackupPath(c.Path)
		previous, rerr := os.ReadFile(filepath.Clean(backup)) //nolint:gosec // doppel's own hidden backup
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return fail(fmt.Errorf("backing up %s: %w", c.Path, rerr))
		}
		hadBackup := rerr == nil
		if err := writeFile(backup, c.Old, 0600); err != nil {
			return fail(fmt.Errorf("backing up %s: %w", c.Path, err))
		}
		done = append(done, undoStep{what: "restore " + backup, undo: func() error {
			if hadBackup {
				return writeFile(backup, previous, 0600)
			}
			return removeFile(backup)
		}})
	}
	for _, c := range changes {
		if !c.Exists {
			continue
		}
		mode := fileMode(c.Path)
		if err := writeFile(c.Path, c.New, mode); err != nil {
			return fail(err)
		}
		if c.Existed {
			done = append(done, undoStep{what: "restore " + c.Path + " from " + BackupPath(c.Path), undo: func() error { return writeFile(c.Path, c.Old, mode) }})
		} else {
			done = append(done, undoStep{what: "delete " + c.Path, undo: func() error { return removeFile(symlinkTarget(c.Path)) }})
		}
	}
	for _, c := range changes {
		if c.Exists {
			continue
		}
		mode := fileMode(c.Path)
		if err := removeFile(c.Path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fail(err)
		}
		done = append(done, undoStep{what: "restore " + c.Path + " from " + BackupPath(c.Path), undo: func() error { return writeFile(c.Path, c.Old, mode) }})
	}
	return changes, nil
}

// rollBack undoes done, newest first, and says in the error what it did.
func rollBack(cause error, done []undoStep) error {
	var stuck []string
	for i := len(done) - 1; i >= 0; i-- {
		if err := done[i].undo(); err != nil {
			stuck = append(stuck, fmt.Sprintf("%s (%v)", done[i].what, err))
		}
	}
	switch {
	case len(stuck) > 0:
		return fmt.Errorf("%w; doppel couldn't put everything back, so finish by hand: %s", cause, strings.Join(stuck, "; "))
	case len(done) > 0:
		return fmt.Errorf("%w; doppel put back every file it had already written, so nothing changed", cause)
	}
	return fmt.Errorf("%w; nothing was changed", cause)
}

// fileMode is the permission bits of path, or 0644 for a file to be created.
func fileMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0644
}

// BackupPath is where the previous version of a file is kept: a hidden file
// alongside it, which Git never reads.
func BackupPath(path string) string {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, ".") {
		name = "." + name
	}
	return filepath.Join(filepath.Dir(path), name+".doppel.bak")
}

// symlinkTarget returns the file a write to path should replace: path
// itself, or the file a symlink at path points to. That includes a dangling
// link, such as a dotfiles manager's link whose target doesn't exist yet,
// so the link is kept rather than replaced by a regular file.
func symlinkTarget(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	for range 40 { // the same limit the kernel puts on a chain of links
		target, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return path
}

// atomicWrite writes data to targetPath through a temporary file and a
// rename, so the file is never left half-written. When targetPath is a
// symlink, the file it points to is replaced and the link is kept.
func atomicWrite(targetPath string, data []byte, perm os.FileMode) error {
	targetPath = symlinkTarget(targetPath)

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	randBytes := make([]byte, 4)
	if _, err := rand.Read(randBytes); err != nil {
		return err
	}
	tmpName := filepath.Join(dir, fmt.Sprintf(".tmp.%s.%s", filepath.Base(targetPath), hex.EncodeToString(randBytes)))

	tmpFile, err := os.OpenFile(filepath.Clean(tmpName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) //nolint:gosec // temp file next to the target
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("setting permissions on temp file: %w", err)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("replacing %s: %w", targetPath, err)
	}

	success = true
	return nil
}
