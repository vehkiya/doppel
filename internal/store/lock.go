package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/vehkiya/doppel/internal/paths"
)

// lockWait is how long Lock waits for another doppel command to finish.
var lockWait = 15 * time.Second

// Lock takes doppel's write lock, an advisory lock on a file in doppel's
// directory, and returns the function that releases it. Commands that write
// hold it from reloading the accounts until their plan is applied, so
// concurrent writes run one after the other. Don't hold it while asking the
// user anything: Save's staleness check covers what was read before.
func Lock(env *paths.Env) (release func(), err error) {
	if err := os.MkdirAll(env.DoppelDir(), 0700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", env.Shorten(env.DoppelDir()), err)
	}
	path := filepath.Join(env.DoppelDir(), ".lock")
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE, 0600) //nolint:gosec // doppel's own lock file
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", env.Shorten(path), err)
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // a file descriptor fits in an int
		if err == nil {
			return func() { _ = f.Close() }, nil // closing the file releases the lock
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s: %w", env.Shorten(path), err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another doppel command is still changing your accounts (lock: %s); try again in a moment", env.Shorten(path))
		}
		time.Sleep(25 * time.Millisecond)
	}
}
