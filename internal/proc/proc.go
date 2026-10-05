// Package proc runs external tools with a time limit, so a tool that hangs,
// such as an ssh-agent that stopped answering or gh waiting on the network,
// can't freeze doppel.
package proc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Time limits for tools that don't wait for the user. Commands that may
// ask something, such as ssh-keygen asking for a passphrase, run without one.
var (
	// Local is for tools that only read local files or talk to a local
	// agent: git config, ssh-keygen -y, ssh-add -l, ssh -G.
	Local = 15 * time.Second
	// Network is for tools that may talk to a server: gh api, ssh -T.
	Network = 60 * time.Second
)

// TimeoutError means a tool didn't finish within its time limit.
type TimeoutError struct {
	Tool  string
	Limit time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s didn't finish within %s, so doppel stopped it", e.Tool, e.Limit)
}

// Command is exec.Command with a time limit. Pass the error from running cmd
// through finish: it releases the limit's timer, and turns the error into a
// *TimeoutError when the limit was hit.
func Command(limit time.Duration, name string, args ...string) (cmd *exec.Cmd, finish func(error) error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	cmd = exec.CommandContext(ctx, name, args...) //nolint:gosec // callers pass fixed tool names; arguments are built by doppel
	// A child that keeps the output pipes open, such as an ssh
	// ControlMaster, mustn't keep doppel waiting once the tool is gone.
	cmd.WaitDelay = time.Second
	return cmd, func(err error) error {
		defer cancel()
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &TimeoutError{Tool: name, Limit: limit}
		}
		return err
	}
}
