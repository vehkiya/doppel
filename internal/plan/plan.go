// Package plan stages file changes so doppel can preview them or write them
// together, with backups and atomic replacement.
package plan

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vehkiya/doppel/internal/atomicfile"
)

// Plan stages file changes so a command can preview them (--dry-run) or
// apply them together. Files are edited as copies in a staging directory;
// Apply then backs up and atomically replaces each file that changed.
type Plan struct {
	stagingDir string
	dir        string   // this plan's own directory in stagingDir, once it has one
	created    []string // directories made for it, shallowest first
	files      map[string]*plannedFile
	order      []string
}

type plannedFile struct {
	path    string // the real file
	staged  string // its working copy
	old     []byte
	existed bool
	remove  bool
	force   bool
}

// Change is one file a plan will create, modify or remove.
type Change struct {
	Path     string
	Old, New []byte
	Existed  bool // the file exists now
	Exists   bool // the file will exist afterwards
}

// staleAfter is how old a leftover plan directory must be before a new
// plan deletes it. A plan lasts for one command, so an older one belongs to
// a command that was killed before it could clean up.
const staleAfter = time.Hour

// New starts an empty plan. Its working copies go in a private (0700)
// directory of its own inside stagingDir, made when the first file is
// staged. The copies can hold secrets from the user's Git config, so doppel
// keeps them in its own directory rather than the shared temp directory.
// Close discards them.
func New(stagingDir string) *Plan {
	return &Plan{stagingDir: stagingDir, files: map[string]*plannedFile{}}
}

// Close discards the staged copies, and the directories made for them once
// they're empty, so a plan that was never applied leaves nothing behind.
func (p *Plan) Close() {
	if p.dir != "" {
		_ = os.RemoveAll(p.dir)
	}
	for i := len(p.created) - 1; i >= 0; i-- {
		_ = os.Remove(p.created[i]) // fails, as it should, when something else is in there
	}
}

// workDir returns the plan's own directory, creating it on first use.
func (p *Plan) workDir() (string, error) {
	if p.dir != "" {
		return p.dir, nil
	}
	for attempt := 1; ; attempt++ {
		created, err := mkdirAll(p.stagingDir)
		p.created = append(p.created, created...)
		if err != nil {
			return "", fmt.Errorf("creating staging directory: %w", err)
		}
		removeStale(p.stagingDir)
		dir, err := os.MkdirTemp(p.stagingDir, "plan-")
		if err == nil {
			p.dir = dir
			return dir, nil
		}
		// Another doppel command may have just removed the staging
		// directory, finding it empty; make it again.
		if !errors.Is(err, fs.ErrNotExist) || attempt == 3 {
			return "", fmt.Errorf("creating staging directory: %w", err)
		}
	}
}

// mkdirAll is os.MkdirAll with mode 0700, returning the directories it
// made, shallowest first.
func mkdirAll(dir string) ([]string, error) {
	var missing []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil || filepath.Dir(d) == d {
			break
		}
		missing = append([]string{d}, missing...)
	}
	return missing, os.MkdirAll(dir, 0700)
}

// removeStale deletes plan directories left in stagingDir by commands that
// were killed. A recent one may belong to a command still running, so it
// stays.
func removeStale(stagingDir string) {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "plan-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleAfter {
			_ = os.RemoveAll(filepath.Join(stagingDir, e.Name()))
		}
	}
}

// Stage returns the working copy of path, creating it from the current file.
// The copy doesn't exist when the file doesn't; `git config --file` creates it.
func (p *Plan) Stage(path string) (string, error) {
	if f, ok := p.files[path]; ok {
		f.remove = false
		return f.staged, nil
	}
	dir, err := p.workDir()
	if err != nil {
		return "", err
	}
	f := &plannedFile{
		path:   path,
		staged: filepath.Join(dir, fmt.Sprintf("%d-%s", len(p.order), filepath.Base(path))),
	}
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // doppel's own and Git config files
	switch {
	case err == nil:
		f.old, f.existed = data, true
		if err := os.WriteFile(f.staged, data, 0600); err != nil { //nolint:gosec // working copy inside the plan's own staging directory
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
	return os.WriteFile(staged, data, 0600) //nolint:gosec // working copy inside the plan's own staging directory
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

// Force marks path to be written on Apply even if its content equals its
// existing content.
func (p *Plan) Force(path string) error {
	if _, err := p.Stage(path); err != nil {
		return err
	}
	p.files[path].force = true
	return nil
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
		if exists == f.existed && bytes.Equal(data, f.old) && !f.force {
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

// atomicWrite replaces path atomically. When path is a symlink, the file it
// points to is replaced and the link is kept.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(symlinkTarget(path), data, perm)
}

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
		steps, err := backUp(c.Path, c.Old)
		done = append(done, steps...)
		if err != nil {
			return fail(fmt.Errorf("backing up %s: %w", c.Path, err))
		}
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
			done = append(done, undoStep{what: "restore " + c.Path + " from " + backupPath(c.Path, 0), undo: func() error { return writeFile(c.Path, c.Old, mode) }})
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
		done = append(done, undoStep{what: "restore " + c.Path + " from " + backupPath(c.Path, 0), undo: func() error { return writeFile(c.Path, c.Old, mode) }})
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

// keptBackups is how many earlier versions of a file Apply keeps.
const keptBackups = 3

// backupPath is where an earlier version of a file is kept: a hidden file
// alongside it, which Git never reads. Generation 0 is the version just
// before the last change (.<name>.doppel.bak); older ones follow as
// .<name>.doppel.bak.1 and .<name>.doppel.bak.2.
func backupPath(path string, generation int) string {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, ".") {
		name = "." + name
	}
	name += ".doppel.bak"
	if generation > 0 {
		name += "." + strconv.Itoa(generation)
	}
	return filepath.Join(filepath.Dir(path), name)
}

// backUp keeps old, the content path is about to lose, as its newest
// backup, moving each earlier backup one generation older and dropping the
// oldest. It returns how to undo each backup it wrote, including on error.
func backUp(path string, old []byte) ([]undoStep, error) {
	type backup struct {
		data   []byte
		exists bool
	}
	current := make([]backup, keptBackups)
	for g := range current {
		data, err := os.ReadFile(filepath.Clean(backupPath(path, g))) //nolint:gosec // doppel's own hidden backup
		switch {
		case err == nil:
			current[g] = backup{data, true}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}

	var done []undoStep
	// Oldest first, so no backup is overwritten before it has moved on.
	for g := keptBackups - 1; g >= 0; g-- {
		next := backup{old, true}
		if g > 0 {
			next = current[g-1]
		}
		if !next.exists {
			continue // nothing that old yet; leave this generation as it is
		}
		target, was := backupPath(path, g), current[g]
		if err := writeFile(target, next.data, 0600); err != nil {
			return done, err
		}
		done = append(done, undoStep{what: "restore " + target, undo: func() error {
			if was.exists {
				return writeFile(target, was.data, 0600)
			}
			return removeFile(target)
		}})
	}
	return done, nil
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
