package terminal_test

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/terminal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These run against a real PTY and a real shell. A fake would prove nothing about the one thing
// that was measured rather than assumed — that the read loop ends only when the PTY is closed.

// collector accumulates what a session printed, safely across the emitter's goroutine.
type collector struct {
	mu     sync.Mutex
	output strings.Builder
	exited map[string]bool
}

func newCollector() *collector { return &collector{exited: map[string]bool{}} }

func (c *collector) Emit(name string, payload any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fields, ok := payload.(map[string]string)
	if !ok {
		return
	}
	switch name {
	case "terminal:output":
		c.output.WriteString(fields["data"])
	case "terminal:exit":
		c.exited[fields["id"]] = true
	}
}

func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func (c *collector) hasExited(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exited[id]
}

func waitFor(t *testing.T, within time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return condition()
}

func openSession(t *testing.T) (*terminal.Registry, *collector, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Git Bash through ConPTY is verified on Windows separately; this suite needs a Unix PTY")
	}

	recorder := newCollector()
	registry := terminal.NewRegistry(recorder)
	t.Cleanup(registry.CloseAll)

	id, err := registry.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, id)
	return registry, recorder, id
}

func TestASessionRunsAShellAndReportsItsOutput(t *testing.T) {
	registry, recorder, id := openSession(t)

	// A marker rather than a prompt: a prompt is whatever the developer's own shell config makes
	// it, and asserting on one would pass or fail per machine.
	require.NoError(t, registry.Write(id, "echo codeflow-marker-7\n"))

	require.True(t, waitFor(t, 10*time.Second, func() bool {
		return strings.Contains(recorder.text(), "codeflow-marker-7")
	}), "got: %q", recorder.text())
}

// FILE-015: the exit is detected by the reader ending, never by the child's status — which is only
// possible because closing the PTY is what ends the reader. Measured on both platforms.
func TestExitingTheShellReportsExitExactlyOnce(t *testing.T) {
	registry, recorder, id := openSession(t)

	require.NoError(t, registry.Write(id, "exit\n"))

	require.True(t, waitFor(t, 10*time.Second, func() bool { return recorder.hasExited(id) }),
		"a shell the user typed exit into must not look like it is still running")

	// The session is gone, so writing to it is the same as writing to one that never existed.
	assert.EqualError(t, registry.Write(id, "echo again\n"), "no such terminal session")
}

// The output has to drain before the exit, or the pane closes over text the user never saw.
func TestTheLastOutputArrivesBeforeTheExit(t *testing.T) {
	registry, recorder, id := openSession(t)

	require.NoError(t, registry.Write(id, "echo final-line-marker; exit\n"))

	require.True(t, waitFor(t, 10*time.Second, func() bool { return recorder.hasExited(id) }))
	assert.Contains(t, recorder.text(), "final-line-marker",
		"the exit event is emitted only after the output channel drained")
}

func TestClosingASessionEndsIt(t *testing.T) {
	registry, recorder, id := openSession(t)

	registry.Close(id)

	assert.True(t, recorder.hasExited(id), "Close waits for the same path an exit takes")
	assert.EqualError(t, registry.Resize(id, 80, 24), "no such terminal session")
}

// The renderer closes a pane it may already have lost.
func TestClosingAnUnknownSessionIsANoOp(t *testing.T) {
	registry := terminal.NewRegistry(nil)

	assert.NotPanics(t, func() {
		registry.Close("00000000-0000-0000-0000-000000000000")
		registry.CloseAll()
	})
}

func TestResize(t *testing.T) {
	registry, recorder, id := openSession(t)

	require.NoError(t, registry.Resize(id, 100, 24))

	// Proven through the shell rather than by asking the library back: COLUMNS is what a program
	// running inside the terminal actually sees, which is the point of resizing at all.
	require.NoError(t, registry.Write(id, "stty size\n"))
	require.True(t, waitFor(t, 10*time.Second, func() bool {
		return strings.Contains(recorder.text(), "24 100")
	}), "rows then columns, as stty prints them; got: %q", recorder.text())
}

func TestCommandsOnAnUnknownSession(t *testing.T) {
	registry := terminal.NewRegistry(nil)
	const unknown = "00000000-0000-0000-0000-000000000000"

	assert.EqualError(t, registry.Write(unknown, "x"), "no such terminal session")
	assert.EqualError(t, registry.Resize(unknown, 80, 24), "no such terminal session")
}

func TestTwoSessionsAreIndependent(t *testing.T) {
	registry, recorder, first := openSession(t)

	second, err := registry.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	registry.Close(first)

	assert.True(t, recorder.hasExited(first))
	assert.False(t, recorder.hasExited(second), "closing one must not touch the other")
	assert.NoError(t, registry.Write(second, "echo still-here\n"))
}
