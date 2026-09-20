package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedEngine compiles the stand-in CLI once per package run and answers its path.
//
// A real executable, because everything the runner has to get right lives on the process
// boundary: pipes that deadlock undrained, a child that outlives a kill, a character split across
// two reads. A mock proves none of it.
var scriptedEngine = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "codeflow-scripted-engine-")
	if err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "scripted")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	build := exec.Command("go", "build", "-o", binary, "./testdata/scriptedengine") //nolint:gosec
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the scripted engine (%s): %w", out, err)
	}
	return binary, nil
})

func engineBinary(t *testing.T) string {
	t.Helper()
	binary, err := scriptedEngine()
	require.NoError(t, err)
	return binary
}

// script sets the stand-in's behaviour for one test.
func script(t *testing.T, vars map[string]string) {
	t.Helper()
	for _, name := range []string{
		"SCRIPT_STDOUT", "SCRIPT_STDERR", "SCRIPT_ECHO_STDIN",
		"SCRIPT_ARGV", "SCRIPT_SLEEP_MS", "SCRIPT_EXIT",
	} {
		t.Setenv(name, vars[name])
	}
}

// passthrough is an interpreter that reports exactly what it was handed, so a test can assert on
// the runner rather than on an engine's parsing.
type passthrough struct{ binary string }

func (p passthrough) Interpret(stdout, stderr string, exitCode int) (ai.Result, error) {
	if exitCode != 0 {
		return ai.Result{}, errors.New(ai.FailureDetail(p.binary, exitCode, stdout, stderr))
	}
	return ai.Result{Text: strings.TrimSpace(stdout)}, nil
}

// plainCommands sends whatever it is given, with no argv of its own.
type plainCommands struct{ args []string }

func (p plainCommands) BuildCommand(ai.Invocation) []string   { return p.args }
func (p plainCommands) StdinPayload(inv ai.Invocation) string { return inv.StdinContent }

// noStdinCommands declares that its CLI does not read stdin — the declaration AI-054 reads.
type noStdinCommands struct{}

func (noStdinCommands) BuildCommand(ai.Invocation) []string { return nil }
func (noStdinCommands) StdinPayload(ai.Invocation) string   { return "" }

func newRun(t *testing.T, timeout time.Duration, withTrace bool) (*ai.RunRegistry, *ai.Run, *bridge.RecordingEmitter) {
	t.Helper()
	recorder := &bridge.RecordingEmitter{}
	registry := ai.NewRunRegistry(recorder, timeout)
	run := registry.Begin("run-1", withTrace)
	t.Cleanup(func() { registry.End("run-1") })
	return registry, run, recorder
}

func TestARunStreamsItsOutputAndReturnsItsReply(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": "first line\nsecond line\n"})
	_, run, recorder := newRun(t, time.Minute, false)

	result, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})

	require.NoError(t, err)
	assert.Equal(t, "first line\nsecond line", result.Text)

	lines := emittedLines(recorder)
	assert.Equal(t, []string{"first line", "second line"}, lines,
		"every line is emitted live, as an activity log")
}

func emittedLines(recorder *bridge.RecordingEmitter) []string {
	events := recorder.Named("ai:output")
	lines := make([]string, 0, len(events))
	for _, event := range events {
		// The payload is the struct the renderer is typed against; reach it through JSON so the
		// test also proves the field names.
		encoded, _ := json.Marshal(event)
		var payload struct {
			RunID  string `json:"run_id"`
			Stream string `json:"stream"`
			Line   string `json:"line"`
		}
		_ = json.Unmarshal(encoded, &payload)
		lines = append(lines, payload.Line)
	}
	return lines
}

func TestBothStreamsAreEmittedWithTheirNames(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": "out\n", "SCRIPT_STDERR": "err\n"})
	_, run, recorder := newRun(t, time.Minute, false)

	_, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})

	require.NoError(t, err)

	streams := map[string]string{}
	for _, event := range recorder.Named("ai:output") {
		encoded, _ := json.Marshal(event)
		var payload struct{ Stream, Line string }
		_ = json.Unmarshal(encoded, &payload)
		streams[payload.Line] = payload.Stream
	}
	assert.Equal(t, "stdout", streams["out"])
	assert.Equal(t, "stderr", streams["err"])
}

// Everything the process wrote arrives, including what it wrote just before exiting.
//
// `Wait` closes the pipes `StdoutPipe`/`StderrPipe` hand out the moment the process exits, so
// calling it before the pumps have drained loses whatever was still in the kernel buffer. The run
// then returns missing its last lines — or, for a process that writes once and exits, all of them.
// It is a race, so it is written as one a slow reader cannot win by luck: 30 000 bytes across 1 000
// lines is more than a pipe holds, from a process that exits the instant it has written them.
//
// Three CI failures in this package were this defect wearing three different costumes, and each one
// looked like flakiness on its own. This is what tells them apart from the real thing.
func TestEveryLineSurvivesAProcessThatExitsAsSoonAsItHasWritten(t *testing.T) {
	const lines = 1000
	var written strings.Builder
	for i := range lines {
		fmt.Fprintf(&written, "line %04d of output\n", i)
	}

	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": written.String()})
	_, run, recorder := newRun(t, time.Minute, false)

	result, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})

	require.NoError(t, err)
	assert.Len(t, emittedLines(recorder), lines, "every line is streamed, not just the ones that beat Wait")
	assert.Equal(t, strings.TrimSpace(written.String()), result.Text, "and the text comes back whole")
}

// The payload has to arrive whole, and the pipe has to be closed: a CLI reading to end-of-file
// waits forever otherwise, and the run's only bound would be the silence deadline.
func TestStdinIsDeliveredWholeAndClosed(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_ECHO_STDIN": "1"})
	_, run, _ := newRun(t, time.Minute, false)

	payload := strings.Repeat("x", 300_000)
	result, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{StdinContent: payload})

	require.NoError(t, err)
	assert.Equal(t, "received 300000 bytes", result.Text,
		"a payload larger than a pipe buffer needs the concurrent writer, or this deadlocks")
}

// An engine that declares it reads no stdin produces a broken pipe on every run. Treating that as
// a failure is what hid a real broken delivery for months (AI-054).
func TestAnEngineThatReadsNoStdinIsNotAFailure(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": "fine\n"})
	_, run, _ := newRun(t, time.Minute, false)

	result, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		noStdinCommands{}, passthrough{binary},
		ai.Invocation{StdinContent: "ignored, because the engine declared it wants none"})

	require.NoError(t, err)
	assert.Equal(t, "fine", result.Text)
}

func TestANonZeroExitCarriesTheDetail(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDERR": "something broke\n", "SCRIPT_EXIT": "3"})
	_, run, _ := newRun(t, time.Minute, false)

	_, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status: 3")
	assert.Contains(t, err.Error(), "something broke")
}

// VERBATIM, and Spanish: a process that printed nothing at all still has to say something.
func TestAFailureWithNoOutputAtAll(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_EXIT": "1"})
	_, run, _ := newRun(t, time.Minute, false)

	_, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sin salida en stdout ni stderr")
}

// ---- stopping ------------------------------------------------------------------------------------

func TestCancellingARunKillsItAndReportsWhy(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": "started\n", "SCRIPT_SLEEP_MS": "30000"})
	registry, run, recorder := newRun(t, time.Minute, false)

	var (
		err  error
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		_, err = ai.NewRunner(nil).Execute(t.Context(), run, binary,
			plainCommands{}, passthrough{binary}, ai.Invocation{})
	}()

	// Wait for the child to say something before stopping it. Cancelling sooner would pass by
	// killing a process that had not started yet, which proves nothing about killing a live one —
	// and the scripted engine sleeps for thirty seconds precisely so this cannot be a coincidence.
	require.Eventually(t, func() bool {
		return len(recorder.Named("ai:output")) > 0
	}, 10*time.Second, 20*time.Millisecond, "the child never started")

	require.True(t, registry.Cancel("run-1"))

	start := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run outlived its cancellation")
	}
	assert.Less(t, time.Since(start), 5*time.Second, "the tree was killed, not waited out")

	require.Error(t, err)
	// Nothing after the marker: the user knows why, and a sentence under a banner they caused is
	// noise.
	assert.Equal(t, "RUN_CANCELLED::", err.Error())
}

// The deadline bounds **silence**, not length, and reports a different marker because it says the
// opposite thing to the person reading the panel.
func TestASilentRunTimesOut(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_SLEEP_MS": "30000"})
	_, run, _ := newRun(t, 400*time.Millisecond, false)

	start := time.Now()
	_, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})

	require.Error(t, err)
	assert.Less(t, time.Since(start), 10*time.Second, "it did not wait for the child")
	// Under a minute carries no number, which only a test's deadline is — the renderer has
	// wording for it that names no duration.
	assert.Equal(t, "RUN_TIMED_OUT::", err.Error())
}

// A stop is a stop whatever the window: cancelling reports the user's marker, never the deadline's.
func TestCancellingReportsTheUsersMarkerNotTheDeadlines(t *testing.T) {
	registry := ai.NewRunRegistry(nil, 10*time.Minute)
	run := registry.Begin("run-minutes", false)

	require.True(t, registry.Cancel("run-minutes"))

	assert.Equal(t, "RUN_CANCELLED::", run.StopReason())
}

// Cancelling something that already finished is the ordinary race, not an error.
func TestCancellingAnUnknownRunAnswersFalse(t *testing.T) {
	registry := ai.NewRunRegistry(nil, time.Minute)

	assert.False(t, registry.Cancel("never-started"))
}

// ---- the activity log ------------------------------------------------------------------------------

func TestBlankLinesAreDroppedAndLongOnesCapped(t *testing.T) {
	binary := engineBinary(t)
	long := strings.Repeat("a", 2500)
	script(t, map[string]string{"SCRIPT_STDOUT": "kept\n\n   \n" + long + "\n"})
	_, run, recorder := newRun(t, time.Minute, false)

	_, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})
	require.NoError(t, err)

	lines := emittedLines(recorder)
	require.Len(t, lines, 2, "the blank and the whitespace-only line are dropped entirely")
	assert.Equal(t, "kept", lines[0])
	assert.Equal(t, 2001, len([]rune(lines[1])), "2000 characters plus the ellipsis")
	assert.True(t, strings.HasSuffix(lines[1], "…"))
}

// A run that asked for no trace still emits live, and keeps nothing.
func TestATraceIsKeptOnlyWhenAsked(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": "one\ntwo\n"})

	_, untraced, _ := newRun(t, time.Minute, false)
	_, err := ai.NewRunner(nil).Execute(t.Context(), untraced, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})
	require.NoError(t, err)
	assert.Empty(t, untraced.Trace())
	assert.NotNil(t, untraced.Trace(), "an array, never null — it is persisted as JSON")

	registry := ai.NewRunRegistry(&bridge.RecordingEmitter{}, time.Minute)
	traced := registry.Begin("traced", true)
	_, err = ai.NewRunner(nil).Execute(t.Context(), traced, binary,
		plainCommands{}, passthrough{binary}, ai.Invocation{})
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, traced.Trace())
}

// ---- argv ------------------------------------------------------------------------------------------

func TestTheEngineDecidesArgv(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_ARGV": "1"})
	_, run, _ := newRun(t, time.Minute, false)

	result, err := ai.NewRunner(nil).Execute(t.Context(), run, binary,
		plainCommands{args: []string{"-p", "the ask", "--model", "a-model"}},
		passthrough{binary}, ai.Invocation{})

	require.NoError(t, err)
	assert.Equal(t, "-p\nthe ask\n--model\na-model", result.Text)
}
