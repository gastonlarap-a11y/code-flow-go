//go:build windows

package proc

import (
	"context"
	"os/exec"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// taskkillTimeout bounds the fallback kill.
//
// It is reached when the Job Object could not be created or could not be closed — already the
// degraded path — and it is called from "stop this run", which a person is waiting on. A taskkill
// that never returns would hang that click forever, and the process it was asked to kill is not
// going to become more killable by waiting.
const taskkillTimeout = 10 * time.Second

// applyGroupAttributes gives the child its own process group and no console window.
//
// CREATE_NEW_PROCESS_GROUP is the Windows counterpart of Setpgid: a Ctrl+C or Ctrl+Break raised
// inside an AI CLI's tree stays there instead of reaching CodeFlow.
//
// CREATE_NO_WINDOW and HideWindow are the ones whose absence is immediately visible: CodeFlow is a
// GUI application, and every git call — of which a repository status makes several — would
// otherwise flash a black console window over the UI.
func applyGroupAttributes(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
	cmd.SysProcAttr.HideWindow = true
}

// attachToJob puts the started process into a Job Object that kills its whole tree when closed.
//
// Windows has no kill(-pgid): a process group only scopes console signals, not termination. A Job
// Object is the supported way to say "this process and everything it creates", and
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE makes closing the handle terminate all of them — including
// when CodeFlow itself dies unexpectedly, since the handle closes with the process.
//
// It must happen after Start and is best-effort: a failure leaves the taskkill fallback in
// killTree, which is what 2.x used exclusively.
func (c *Cmd) attachToJob() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	// gosec G103: `SetInformationJobObject` takes the structure as an address and a length, because
	// that is the Win32 signature. `unsafe.Pointer` is the only way to express it, `limits` is a
	// local that outlives the call, and the length is taken from the same value — the three
	// conditions the rule exists to check.
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), //nolint:gosec
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		// Ignored: the job is unusable, so close it and leave the taskkill fallback in charge.
		_ = windows.CloseHandle(job)
		return
	}

	// gosec G115: a Windows process id **is** a DWORD — `os.Process.Pid` widens it to int on the way
	// in, and this narrows it back for the API that issued it. The value came from a process this
	// package started moments ago, so there is no range to check that could be false.
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid)) //nolint:gosec
	if err != nil {
		// Ignored: same reason as above.
		_ = windows.CloseHandle(job)
		return
	}
	// Ignored: the handle has done its job once assignment is attempted; the outcome is covered
	// by the fallback either way.
	defer func() { _ = windows.CloseHandle(handle) }()

	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		// Ignored: as above.
		_ = windows.CloseHandle(job)
		return
	}
	c.jobHandle = uintptr(job)
}

// killTree closes the Job Object, which terminates the process and every descendant.
//
// taskkill /T /F is the fallback for the case where the job could not be created — an older
// Windows, a policy, or a process that was already inside someone else's job that does not allow
// nesting. It is what 2.x did on its own, so the fallback is a known-working path rather than a
// guess.
func killTree(c *Cmd) error {
	if c.jobHandle != 0 {
		handle := windows.Handle(c.jobHandle)
		c.jobHandle = 0
		if err := windows.CloseHandle(handle); err == nil {
			return nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), taskkillTimeout)
	defer cancel()

	// gosec G204: the only interpolated argument is a process id rendered as decimal digits by
	// `strconv.Itoa`, and the arguments go as a slice rather than through a shell. There is nothing
	// here a caller could make mean something else.
	kill := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)) //nolint:gosec
	applyGroupAttributes(kill)
	if err := kill.Run(); err != nil {
		// taskkill exits non-zero when the process is already gone, which is not a failure of
		// "make sure this is not running".
		if c.ProcessState != nil && c.ProcessState.Exited() {
			return nil
		}
		return err
	}
	return nil
}
