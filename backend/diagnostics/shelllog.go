package diagnostics

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// shellTimestampLayout is JavaScript's Date.toISOString(): UTC, three fractional digits, a literal
// Z. shell.log keeps it rather than adopting errors.log's local-time format, because existing
// installs already have the file and the two have always differed.
const shellTimestampLayout = "2006-01-02T15:04:05.000Z"

// Level is one of the three severities shell.log carries.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// ShellLog records what the desktop layer did (BOOT-031): every quit and the reason somebody asked
// for it, window lifecycle, and the failures that used to die in a console nobody had attached.
//
// In 2.x this was the Electron main process's own log, written in TypeScript. The Go port keeps
// the file, its name and its line format; what changed is that it no longer has to duplicate the
// redaction logic in a second language to do it.
type ShellLog struct{ out *appender }

// NewShellLog writes into <logsDirectory>/shell.log.
func NewShellLog(logsDirectory string) *ShellLog {
	return &ShellLog{out: newAppender(logsDirectory, "shell.log")}
}

// Path is the file this log writes to.
func (l *ShellLog) Path() string { return l.out.Path() }

// FormatShellLine builds one line from an already-formatted timestamp. It is exported so a test
// can assert the shape without having to parse a clock, which is the same reason the 2.x
// TypeScript exported its formatLine.
func FormatShellLine(timestamp string, level Level, message string) string {
	padded := strings.ToUpper(string(level))
	for len(padded) < 5 {
		padded += " "
	}
	return timestamp + "  " + padded + "  " + Redact(message)
}

// Record appends one line and mirrors it to stderr.
//
// stderr rather than stdout on purpose: --smoke-test and the 2.x "ready" handshake both used
// stdout as a channel, and a log line landing in the middle of one is the kind of thing that works
// on the developer's machine and fails in CI.
func (l *ShellLog) Record(level Level, message string) {
	line := FormatShellLine(time.Now().UTC().Format(shellTimestampLayout), level, message)
	fmt.Fprintln(os.Stderr, line)
	l.out.append(line)
}

// Info, Warn and Error are the spellings the desktop layer actually calls, so the level constants
// stay an implementation detail of this package rather than something every caller imports.
func (l *ShellLog) Info(message string)  { l.Record(LevelInfo, message) }
func (l *ShellLog) Warn(message string)  { l.Record(LevelWarn, message) }
func (l *ShellLog) Error(message string) { l.Record(LevelError, message) }
