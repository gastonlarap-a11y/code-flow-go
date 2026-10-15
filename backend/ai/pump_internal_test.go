package ai

import (
	"fmt"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Internal, because what these check is the reading itself rather than anything a caller sees.

func collectLines(t *testing.T, reader interface{ Read([]byte) (int, error) }) ([]string, []byte) {
	t.Helper()
	recorder := &bridge.RecordingEmitter{}
	run := NewRunRegistry(recorder, time.Minute).Begin("run", true)

	collected := pump(reader, run, StreamStdout)
	return run.Trace(), collected
}

// The reason the pump reads bytes rather than decoded text: a read boundary can fall inside a
// multi-byte character, and decoding each chunk as it arrives turns that character into two
// replacement characters — in the middle of a filename, a diff or a review's prose.
func TestACharacterSplitAcrossReadsSurvives(t *testing.T) {
	const text = "acentuación y ñ y 漢字\n"

	// One byte at a time, which puts a boundary inside every multi-byte character there is.
	lines, collected := collectLines(t, iotest.OneByteReader(strings.NewReader(text)))

	assert.Equal(t, []string{"acentuación y ñ y 漢字"}, lines)
	assert.Equal(t, text, string(collected))
}

// A progress bar never emits a newline. It is shown as a terminal shows it — its latest drawing,
// every so often — rather than as kilobytes of every drawing run together, and the buffer stays one
// drawing long.
func TestAProgressBarShowsItsLatestDrawing(t *testing.T) {
	var bar strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&bar, "\rdownloading %3d%%", i%101)
	}

	lines, collected := collectLines(t, strings.NewReader(bar.String()))

	require.NotEmpty(t, lines, "a CLI redrawing a progress bar must not go unreported")
	for _, line := range lines {
		assert.Regexp(t, `^downloading +\d+%$`, line, "one drawing per line, never several run together")
	}
	assert.Equal(t, bar.String(), string(collected), "the interpreter still gets every byte")
}

// Within a complete line, what follows the last carriage return is what a terminal shows.
func TestALineRedrawnBeforeItsNewlineShowsItsLastDrawing(t *testing.T) {
	lines, _ := collectLines(t, strings.NewReader("10%\r50%\r100%\ndone\n"))

	assert.Equal(t, []string{"100%", "done"}, lines)
}

// A Windows line ending split across two reads ends a line; it redraws nothing.
func TestACRLFSplitAcrossReadsEndsALine(t *testing.T) {
	lines, _ := collectLines(t, iotest.OneByteReader(strings.NewReader("first\r\nsecond\r\n")))

	assert.Equal(t, []string{"first", "second"}, lines)
}

// A JSON event holding a whole file is one long line. It reaches the log whole, reduced to what
// the log shows, instead of being cut into pieces nobody can parse.
func TestALongJSONEventReachesTheLogWhole(t *testing.T) {
	event := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"src/app.ts","content":"` +
		strings.Repeat("x", 200_000) + `"}}]}}` + "\n"

	lines, _ := collectLines(t, iotest.HalfReader(strings.NewReader(event)))

	assert.Equal(t, []string{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"src/app.ts"}}]}}`}, lines)
}

// A CLI that exits without a trailing newline would otherwise lose its last line, which is often
// the one that says why it exited.
func TestTheFinalLineWithoutANewlineIsStillEmitted(t *testing.T) {
	lines, _ := collectLines(t, strings.NewReader("first\nlast line, no newline"))

	assert.Equal(t, []string{"first", "last line, no newline"}, lines)
}

// These CLIs colour their output even when stdout is a pipe. Without stripping, the log fills with
// escape codes and the engine's own parser sees them too — a JSON line wrapped in colour does not
// parse.
func TestAnsiEscapesAreStripped(t *testing.T) {
	lines, _ := collectLines(t, strings.NewReader("\x1b[1;32mgreen\x1b[0m and \x1b[2Kcleared\n"))

	assert.Equal(t, []string{"green and cleared"}, lines)
}

func TestTextWithNoEscapesIsUntouched(t *testing.T) {
	const plain = "nothing to strip here"

	assert.Equal(t, plain, stripANSI(plain))
}

// An untracked call — a version probe — still accumulates its bytes for the interpreter and emits
// nothing at all.
func TestAnUntrackedRunCollectsWithoutEmitting(t *testing.T) {
	collected := pump(strings.NewReader("output\n"), nil, StreamStdout)

	assert.Equal(t, "output\n", string(collected))
}

// The deadline measures silence, and it is anchored to the read rather than to the emit: the emit
// drops blank lines, so a CLI printing only whitespace would otherwise be judged dead.
func TestWhitespaceOnlyOutputStillCountsAsAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &bridge.RecordingEmitter{}
		run := NewRunRegistry(recorder, time.Minute).Begin("run", true)

		time.Sleep(20 * time.Millisecond)
		pump(strings.NewReader("   \n\n   \n"), run, StreamStdout)

		assert.Zero(t, run.silentFor(), "the read pushed the deadline out")
		assert.Empty(t, run.Trace(), "and nothing was emitted, which is why the emit is the wrong anchor")
	})
}
