// A stand-in for an AI CLI, driven by environment variables.
//
// A real executable rather than a fake interface, because what the runner has to get right is all
// on the process boundary: pipes that deadlock when nobody drains them, a child that outlives a
// kill, a multi-byte character split across two reads, stdin that is never closed. None of that is
// observable through a mock.
//
// It lives in testdata so `go build ./...` and the linters ignore it; the test compiles it.
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

func main() {
	// SCRIPT_STDOUT and SCRIPT_STDERR are written verbatim, in that order.
	if text := os.Getenv("SCRIPT_STDOUT"); text != "" {
		fmt.Fprint(os.Stdout, text)
	}
	if text := os.Getenv("SCRIPT_STDERR"); text != "" {
		fmt.Fprint(os.Stderr, text)
	}

	// SCRIPT_ECHO_STDIN reads stdin to end-of-file and prints what it got, so a test can prove the
	// payload arrived whole.
	if os.Getenv("SCRIPT_ECHO_STDIN") != "" {
		received, _ := io.ReadAll(os.Stdin)
		fmt.Fprintf(os.Stdout, "received %d bytes\n", len(received))
	}

	// SCRIPT_ARGV prints the arguments one per line, for asserting on argv.
	if os.Getenv("SCRIPT_ARGV") != "" {
		for _, arg := range os.Args[1:] {
			fmt.Fprintln(os.Stdout, arg)
		}
	}

	// SCRIPT_SLEEP_MS keeps the process alive, for cancellation and the silence deadline.
	if ms := os.Getenv("SCRIPT_SLEEP_MS"); ms != "" {
		if milliseconds, err := strconv.Atoi(ms); err == nil {
			time.Sleep(time.Duration(milliseconds) * time.Millisecond)
		}
	}

	if code := os.Getenv("SCRIPT_EXIT"); code != "" {
		if value, err := strconv.Atoi(code); err == nil {
			os.Exit(value)
		}
	}
}
