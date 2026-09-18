package ai

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// An internal test because the two ways a run stops are set from inside: reaching the timeout path
// from outside would mean waiting out a real window, and the formatting of the marker is what is
// being checked, not the clock that reaches it.

func TestTheDeadlineIsReportedInWholeMinutes(t *testing.T) {
	for _, window := range []struct {
		timeout time.Duration
		want    string
	}{
		{10 * time.Minute, "RUN_TIMED_OUT::10"},
		{time.Minute, "RUN_TIMED_OUT::1"},
		{90 * time.Second, "RUN_TIMED_OUT::1"},
		// Under a minute carries no number at all, which only a test's deadline is — the renderer
		// has wording for it that names no duration rather than saying "0 minutes".
		{30 * time.Second, "RUN_TIMED_OUT::"},
	} {
		t.Run(window.timeout.String(), func(t *testing.T) {
			run := NewRunRegistry(nil, window.timeout).Begin("run", false)
			run.markTimedOut()

			assert.Equal(t, window.want, run.StopReason())
		})
	}
}

// A shutdown racing the deadline reads as a cancellation: the two are stored separately, and the
// user pressing stop is the reading that matters to them.
func TestCancellationWinsOverAConcurrentTimeout(t *testing.T) {
	run := NewRunRegistry(nil, 10*time.Minute).Begin("run", false)

	run.markCancelled()
	run.markTimedOut()

	assert.Equal(t, "RUN_CANCELLED::", run.StopReason())
	assert.True(t, run.Cancelled())
}

func TestAnUnstoppedRunHasNoReason(t *testing.T) {
	run := NewRunRegistry(nil, time.Minute).Begin("run", false)

	assert.Empty(t, run.StopReason())
	assert.False(t, run.Cancelled())
}

// The deadline measures silence, so a read pushes it out.
func TestTouchPushesTheDeadlineOut(t *testing.T) {
	run := NewRunRegistry(nil, time.Minute).Begin("run", false)

	time.Sleep(20 * time.Millisecond)
	before := run.silentFor()
	run.Touch()

	assert.Less(t, run.silentFor(), before)
}

// The ring buffer keeps the newest, so a long run's trace stays storable.
func TestTheTraceRingBufferDropsTheOldest(t *testing.T) {
	run := NewRunRegistry(nil, time.Minute).Begin("run", true)

	const extra = 50
	for i := range maxTraceLines + extra {
		run.EmitLine(StreamStdout, "line "+strconv.Itoa(i))
	}

	trace := run.Trace()
	assert.Len(t, trace, maxTraceLines)
	assert.Equal(t, "line "+strconv.Itoa(maxTraceLines+extra-1), trace[len(trace)-1],
		"the newest line survives")
	assert.Equal(t, "line "+strconv.Itoa(extra), trace[0], "and the oldest are the ones dropped")
}
