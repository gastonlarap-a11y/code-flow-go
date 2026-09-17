package diagnostics

import (
	"fmt"
	"os"
	"time"
)

// StartupLog records a start-up stage that failed (BOOT-030).
//
// It is the log with the hardest job: the stages it reports on are the ones that create the
// directory it wants to write into. So it writes to three places, in order of how likely each is
// to exist — stderr always, then {base}/logs/startup.log, and the temp directory when that fails.
// Losing the reason the app came up broken is how a bug report becomes "it just doesn't work".
//
// Unlike 2.x, a failed stage no longer ends the process: the window still opens and reports the
// failure through app.StartupState, so the user sees something they can act on (§3.8, BOOT-032).
// This log is what tells them, and us, what actually happened.
type StartupLog struct {
	out      *appender
	fallback *appender
}

// NewStartupLog writes into <logsDirectory>/startup.log, falling back to the OS temp directory.
func NewStartupLog(logsDirectory string) *StartupLog {
	return &StartupLog{
		out:      newAppender(logsDirectory, "startup.log"),
		fallback: newAppender(os.TempDir(), "startup.log"),
	}
}

// Path is the file this log prefers to write to.
func (l *StartupLog) Path() string { return l.out.Path() }

// Record files one failed stage. stage is the name from the start-up sequence — "reset-marker",
// "directories", "scratch-sweep", "storage" — and the names are part of the log's contract with
// whoever reads it, so they are spelled the same as in 2.x.
//
// Format: "<timestamp>  startup/<stage>  <redacted error chain>".
func (l *StartupLog) Record(stage string, failure error) {
	if failure == nil {
		return
	}

	// C# wrote failure.ToString(), which is the message plus every inner exception. Go's
	// equivalent is Error() on a chain built with %w: the wrapped text is already inside it.
	line := time.Now().Format(timestampLayout) + "  startup/" + stage + "  " + Redact(failure.Error())

	// stderr first and unconditionally. In a packaged GUI build nothing is attached to it, but
	// when a developer or a support session runs the binary from a terminal it is the fastest
	// path to the answer, and it costs nothing when nobody is looking.
	fmt.Fprintln(os.Stderr, line)

	if !l.out.append(line) {
		l.fallback.append(line)
	}
}
