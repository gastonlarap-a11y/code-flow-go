// Package diagnostics writes the three log files CodeFlow keeps under {base}/logs.
//
//   - errors.log  — one line per command that returned an error (BOOT-030, written from bridge.Invoke)
//   - startup.log — one line per start-up stage that failed (BOOT-030)
//   - shell.log   — what the desktop layer did: quits and their reasons, window lifecycle (BOOT-031)
//
// In 2.x these were two implementations in two languages that had to be kept in step by hand: the
// sidecar's Diagnostics/ErrorLog.cs and the Electron main process's shell/src/shell-log.ts. They
// agreed on the rollover size, the redaction and the "never throw" rule, and drifted on
// everything else. One process means one implementation; the three formats stay as they shipped,
// because existing installs have these files and users send them.
//
// Three properties are load-bearing and shared by all of them:
//
//   - The directory is a constructor parameter, never a global. Without that the test suite writes
//     its fixtures into the user's real ~/CodeFlow/logs.
//   - Writing never fails the caller. A full disk is not a reason to fail the thing that was only
//     being recorded — least of all a start-up that is already failing.
//   - Every line is redacted before it reaches the disk.
package diagnostics

import (
	"os"
	"path/filepath"
	"sync"
)

// maxBytes is the size past which a log is rolled over. One `.1` sibling, not an archive:
// 2 * 1024 * 1024, the value both 2.x implementations used.
const maxBytes int64 = 2 * 1024 * 1024

// appender owns one file. The three logs each hold one, so a start-up failure and a command
// failure never interleave halfway through a line.
//
// Synchronous on purpose: the most important thing any of these ever records is the last thing the
// process does before it dies, and a buffered write would not survive that.
type appender struct {
	mu        sync.Mutex
	directory string
	name      string
}

func newAppender(directory, name string) *appender {
	return &appender{directory: directory, name: name}
}

// Path is where this log writes. Exported through each log type for the renderer's "open logs".
func (a *appender) Path() string { return filepath.Join(a.directory, a.name) }

// append writes one line plus a newline, rolling the file over first if it has outgrown maxBytes.
// It never returns an error: the caller is in no position to handle one. The bool says whether the
// line landed, which only StartupLog uses — it is the one log with somewhere else to go when the
// real directory is unwritable, and that case is precisely when its line matters most.
func (a *appender) append(line string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 0750/0640 rather than 0755/0644: redaction is a best-effort filter over a remote host's
	// error bodies, not a guarantee, so these files are kept to the user who owns them. The
	// constants live in platform, but diagnostics must not import it — platform's own start-up
	// failures are what this log exists to record, so the dependency would be a cycle.
	if err := os.MkdirAll(a.directory, 0o750); err != nil {
		return false
	}
	path := a.Path()

	// Rolled before the write, on the size the file already had — the same test both 2.x
	// implementations made ("> maxBytes", not ">="), so a file sitting exactly at the limit is
	// left alone. os.Rename replaces an existing .1, which is the intended overwrite.
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		// Ignored: a rename that fails leaves the oversized file in place and the line is still
		// appended to it. Losing the rollover costs disk; failing here would cost the line.
		_ = os.Rename(path, path+".1")
	}

	// gosec G304: the path is this appender's own, built in the constructor from a directory the
	// application chose and a filename that is a compile-time constant. There is no user input in it.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640) //nolint:gosec
	if err != nil {
		return false
	}
	// Ignored: the write below is what this function is for, and its error is the one reported.
	// A Close error after a successful write has nothing left to tell the caller, who by contract
	// cannot act on it anyway.
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(line + "\n"); err != nil {
		return false
	}
	return true
}
