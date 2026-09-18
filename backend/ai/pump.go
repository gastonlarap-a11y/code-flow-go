package ai

import (
	"bytes"
	"io"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// maxPendingBytes is when a line with no newline yet is flushed anyway.
//
// It is what keeps a CLI redrawing a `\r` progress bar from growing a buffer without bound and
// showing nothing while it does — the bar never emits a newline, so without this the log stays
// empty for the whole download and then arrives at once.
const maxPendingBytes = 8192

// pump reads one pipe, streaming complete lines and accumulating everything (AI-010).
//
// **Raw bytes, not decoded text.** A read boundary can fall in the middle of a multi-byte UTF-8
// character, and decoding each chunk as it arrives turns that character into two replacement
// characters — in the middle of a diff, a filename or a review's prose. Splitting on the newline
// byte first and decoding whole lines afterwards cannot cut one in half.
//
// The accumulated bytes are what the engine interprets afterwards. The emitted lines are the
// activity log, and the two are deliberately different things (AI-011).
func pump(reader io.Reader, run *Run, stream string) []byte {
	var (
		collected bytes.Buffer
		pending   bytes.Buffer
		chunk     = make([]byte, 4096)
	)

	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			// Anchored to the read, which is what makes the deadline measure silence: a CLI
			// printing only whitespace is alive, and a deadline anchored to the emit — which
			// drops blank lines — would judge it dead.
			if run != nil {
				run.Touch()
			}

			collected.Write(chunk[:n])
			pending.Write(chunk[:n])
			flushLines(&pending, run, stream)

			if pending.Len() > maxPendingBytes {
				emit(run, stream, pending.Bytes())
				pending.Reset()
			}
		}
		if err != nil {
			break
		}
	}

	// Whatever never got its newline. A CLI that exits without one would otherwise lose its last
	// line, which is often the one that says why it exited.
	if pending.Len() > 0 {
		emit(run, stream, pending.Bytes())
	}
	return collected.Bytes()
}

// flushLines emits every complete line in the buffer and keeps the remainder.
func flushLines(pending *bytes.Buffer, run *Run, stream string) {
	for {
		index := bytes.IndexByte(pending.Bytes(), '\n')
		if index < 0 {
			return
		}
		line := make([]byte, index)
		copy(line, pending.Bytes()[:index])

		pending.Next(index + 1)
		emit(run, stream, line)
	}
}

// emit hands one line to the run, decoding it as late as possible.
//
// An untracked run — a call made outside the registry, such as a version probe — still accumulates
// its bytes for the interpreter and emits nothing at all.
func emit(run *Run, stream string, line []byte) {
	if run == nil {
		return
	}
	run.EmitLine(stream, stripANSI(proc.DecodeLossyUTF8(line)))
}

// stripANSI removes terminal escape sequences.
//
// These CLIs colour their output and draw progress bars even when their stdout is a pipe, so
// without this the activity log fills with `ESC[2K` and `ESC[1;32m`, and the engine's own parser
// sees them too — a JSON line wrapped in colour codes does not parse.
//
// Hand-written rather than a regular expression because it runs over every byte of every run's
// output, and because the grammar needed is small: CSI sequences, which is what a terminal library
// emits, plus the two-character escapes that sometimes precede them.
func stripANSI(text string) string {
	if !bytes.ContainsRune([]byte(text), 0x1b) {
		return text
	}

	var out []rune
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != 0x1b {
			out = append(out, runes[i])
			continue
		}
		// ESC [ … <final byte in @ to ~>
		if i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && (runes[i] < '@' || runes[i] > '~') {
				i++
			}
			continue
		}
		// A two-character escape: skip both.
		i++
	}
	return string(out)
}
