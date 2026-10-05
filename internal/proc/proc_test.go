package proc

import (
	"errors"
	"testing"
	"time"
)

func TestCommandStopsAToolThatHangs(t *testing.T) {
	cmd, finish := Command(100*time.Millisecond, "sleep", "10")
	start := time.Now()
	err := finish(cmd.Run())
	var timeout *TimeoutError
	if !errors.As(err, &timeout) || timeout.Tool != "sleep" {
		t.Fatalf("error = %v, want a TimeoutError naming sleep", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s to stop", elapsed)
	}
}

func TestCommandKeepsOtherErrors(t *testing.T) {
	cmd, finish := Command(time.Minute, "false")
	err := finish(cmd.Run())
	var timeout *TimeoutError
	if err == nil || errors.As(err, &timeout) {
		t.Errorf("error = %v, want false's own exit error", err)
	}
	cmd, finish = Command(time.Minute, "true")
	if err := finish(cmd.Run()); err != nil {
		t.Errorf("true: %v", err)
	}
}
