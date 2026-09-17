//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// applyGroupAttributes puts the child in a process group of its own.
//
// Setpgid makes the child the leader of a new group whose id equals its pid. Two things follow:
// a signal the child raises against its own group (an AI CLI shutting down its workers with
// kill(0, ...)) cannot reach CodeFlow, and the whole tree can be addressed at once with a negative
// pid — which is what killTree relies on.
func applyGroupAttributes(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// attachToJob is a Windows concept; on Unix the process group set above is the whole mechanism.
func (c *Cmd) attachToJob() {}

// killTree signals the child's entire process group.
//
// The negative pid is the point: kill(-pgid) delivers to every process in the group, which after
// Setpgid is the child and everything it spawned. SIGKILL rather than SIGTERM because this is the
// path a user's "stop" takes, and an AI CLI that traps SIGTERM to finish its turn would keep the
// run alive past the click.
func killTree(c *Cmd) error {
	pgid, err := syscall.Getpgid(c.Process.Pid)
	if err != nil {
		// The process is already gone, or it never got its own group. Fall back to the direct
		// child so a caller's "stop" still does something.
		if killErr := c.Process.Kill(); killErr != nil && !alreadyGone(killErr) {
			return killErr
		}
		return nil
	}

	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !alreadyGone(err) {
		return err
	}
	return nil
}

// alreadyGone reports the two ways "there is nothing left to kill" arrives: ESRCH from the syscall
// and os.ErrProcessDone from os.Process, which has already reaped the child and refuses to signal
// it. Neither is a failure of "make sure this is not running" — the caller asked for a state, and
// the state holds.
func alreadyGone(err error) bool {
	return errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone)
}
