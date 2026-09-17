package diagnostics

import (
	"reflect"
	"time"
)

// timestampLayout is .NET's "yyyy-MM-dd HH:mm:ss zzz" in Go's reference time. Local time with an
// explicit offset, which is what makes a log readable when the user who sends it is in another
// timezone than the person reading it. errors.log and startup.log both use it.
const timestampLayout = "2006-01-02 15:04:05 -07:00"

// ErrorLog records one line for every command that returned an error.
//
// It is called from exactly one place — bridge.Service.Invoke — which is where 2.x called it too
// (IpcServer.DispatchAsync). Keeping it to one call site is what makes the file a complete record
// rather than a sample: a handler that logs its own failures as well produces duplicates, and one
// that logs instead of returning produces a silent command.
type ErrorLog struct{ out *appender }

// NewErrorLog writes into <logsDirectory>/errors.log.
func NewErrorLog(logsDirectory string) *ErrorLog {
	return &ErrorLog{out: newAppender(logsDirectory, "errors.log")}
}

// Path is the file this log writes to.
func (l *ErrorLog) Path() string { return l.out.Path() }

// Record files one failed command. The signature matches what bridge.Service expects, so main can
// hand it over as a plain func without the bridge knowing this package exists.
//
// Format: "<timestamp>  <method>  <Type>: <redacted message>", two spaces between fields.
func (l *ErrorLog) Record(method string, failure error) {
	if failure == nil {
		return
	}
	l.out.append(
		time.Now().Format(timestampLayout) + "  " + method + "  " +
			typeName(failure) + ": " + Redact(failure.Error()),
	)
}

// typeName is the Go stand-in for C#'s failure.GetType().Name: the dynamic type without its
// package path. It is diagnostics, not a contract — nothing matches on it — but it is what tells
// the reader whether a line came from a sentinel translation (*errors.errorString), a wrapped
// chain (*fmt.wrapError) or one of the typed errors a feature defines (*azure.Error).
func typeName(err error) string {
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if name := t.Name(); name != "" {
		return name
	}
	return t.String()
}
