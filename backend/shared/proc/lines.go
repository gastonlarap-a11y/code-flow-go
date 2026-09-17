package proc

import (
	"bufio"
	"io"
	"iter"
	"strings"
)

// maxLineBytes caps one line of child output.
//
// bufio.Scanner's own default is 64 KiB and it *fails* past it, which turns a single long line —
// a minified bundle echoed by a build tool, a base64 blob — into "the installer stopped reporting
// progress" with no error anywhere. 1 MiB with an explicit truncation is the honest version: the
// line is cut, everything after it still arrives.
const maxLineBytes = 1 << 20

// Lines reads a child process's output line by line.
//
// Three things it does that a bare Scanner does not:
//
//   - Decodes each line as lossy UTF-8, so a CLI writing in the system code page degrades to
//     replacement characters instead of producing JSON that fails to parse on the way to the
//     renderer.
//   - Strips a trailing \r, so a Windows child's output does not arrive with a stray carriage
//     return on every line.
//   - Never fails. A read error ends the sequence; the caller is streaming progress, and there is
//     nothing useful to do with the error that ending quietly does not already do.
func Lines(reader io.Reader) iter.Seq[string] {
	return scan(reader, bufio.ScanLines)
}

// ProgressLines reads output where a bare carriage return ends a line as well.
//
// It exists because `git clone` and `git fetch` redraw one line in place: "Receiving objects: 1%\r
// Receiving objects: 2%\r…", with no newline until the whole phase is over. Read with Lines, a
// minute of download arrives as a single enormous line at the end, which is the opposite of a
// progress report.
//
// The rule is .NET's StreamReader.ReadLine — \n, \r\n and a lone \r all terminate — because that is
// what 2.x's pump used and therefore what the progress log was shaped around.
//
// Deliberately **not** how Lines behaves: that one reassembles output for a parser, where inventing
// a line break inside a diff's content would corrupt it. Same input, two jobs.
func ProgressLines(reader io.Reader) iter.Seq[string] {
	return scan(reader, scanAnyLineBreak)
}

func scan(reader io.Reader, split bufio.SplitFunc) iter.Seq[string] {
	return func(yield func(string) bool) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
		scanner.Split(split)

		for scanner.Scan() {
			line := strings.TrimSuffix(DecodeLossyUTF8(scanner.Bytes()), "\r")
			if !yield(line) {
				return
			}
		}
	}
}

// scanAnyLineBreak is a bufio.SplitFunc treating \n, \r\n and a lone \r as terminators.
func scanAnyLineBreak(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i], nil
		}
		if b != '\r' {
			continue
		}
		// A \r at the very end of what has been read so far is undecidable: the next byte may be
		// the \n that makes it one terminator rather than two. Ask for more unless there is no more.
		if i == len(data)-1 && !atEOF {
			break
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			return i + 2, data[:i], nil
		}
		return i + 1, data[:i], nil
	}

	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
