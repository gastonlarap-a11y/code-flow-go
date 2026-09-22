package usage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/usage"
)

// at is a readable instant: `at(9, 30)` is 09:30 on the reference day.
func at(hour, minute int) time.Time {
	return time.Date(2026, 9, 22, hour, minute, 0, 0, time.UTC)
}

func event(hour, minute int, model string, tokens int64) usage.Event {
	return usage.Event{At: at(hour, minute), Model: model, Tokens: tokens}
}

func TestCurrentSessionOpensWithTheFirstCall(t *testing.T) {
	events := []usage.Event{
		event(9, 0, "claude-opus-5", 100),
		event(9, 30, "claude-opus-5", 200),
	}

	window, open := usage.CurrentSession(events, at(10, 0))

	require.True(t, open)
	assert.Equal(t, at(9, 0), window.Start)
	assert.Equal(t, at(14, 0), window.End, "five hours from the first call")
	assert.Equal(t, int64(300), window.Tokens)
}

// The rule the whole reset time rests on: once a block elapses, the next call opens a new one, and
// the panel must show the new block rather than the exhausted old one.
func TestACallAfterTheBlockElapsesOpensTheNextOne(t *testing.T) {
	events := []usage.Event{
		event(9, 0, "claude-opus-5", 5_000),
		event(14, 30, "claude-opus-5", 100),
	}

	window, open := usage.CurrentSession(events, at(15, 0))

	require.True(t, open)
	assert.Equal(t, at(14, 30), window.Start)
	assert.Equal(t, at(19, 30), window.End)
	assert.Equal(t, int64(100), window.Tokens, "the elapsed block's spend is not carried over")
}

func TestACallExactlyAtTheBoundaryStartsTheNextBlock(t *testing.T) {
	events := []usage.Event{
		event(9, 0, "claude-opus-5", 100),
		event(14, 0, "claude-opus-5", 50),
	}

	window, open := usage.CurrentSession(events, at(14, 30))

	require.True(t, open)
	assert.Equal(t, at(14, 0), window.Start, "the window is half-open: 14:00 is outside the 09:00 block")
	assert.Equal(t, int64(50), window.Tokens)
}

func TestManyBlocksCarryForwardToTheLastOne(t *testing.T) {
	events := []usage.Event{
		event(0, 0, "claude-opus-5", 1),
		event(6, 0, "claude-opus-5", 2),
		event(12, 0, "claude-opus-5", 4),
		event(12, 30, "claude-opus-5", 8),
	}

	window, open := usage.CurrentSession(events, at(13, 0))

	require.True(t, open)
	assert.Equal(t, at(12, 0), window.Start)
	assert.Equal(t, int64(12), window.Tokens)
}

// Not having worked in a while is the ordinary state, not a failure: the panel shows an empty
// window rather than an error or a stale block.
func TestNoBlockIsOpenOnceTheLastOneElapsed(t *testing.T) {
	events := []usage.Event{event(9, 0, "claude-opus-5", 100)}

	_, open := usage.CurrentSession(events, at(14, 30))

	assert.False(t, open)
}

func TestNoEventsMeansNoBlock(t *testing.T) {
	_, open := usage.CurrentSession(nil, at(12, 0))

	assert.False(t, open)
}

// The readers walk files in whatever order the filesystem gives them, so the block rule has to
// survive events arriving shuffled.
func TestEventsOutOfOrderAreSortedBeforeBlocking(t *testing.T) {
	shuffled := []usage.Event{
		event(12, 30, "claude-opus-5", 8),
		event(0, 0, "claude-opus-5", 1),
		event(12, 0, "claude-opus-5", 4),
	}

	window, open := usage.CurrentSession(shuffled, at(13, 0))

	require.True(t, open)
	assert.Equal(t, at(12, 0), window.Start)
	assert.Equal(t, int64(12), window.Tokens)
}

func TestTheCallerSliceIsNeverReordered(t *testing.T) {
	events := []usage.Event{event(12, 0, "a", 1), event(9, 0, "b", 2)}

	_, _ = usage.CurrentSession(events, at(13, 0))

	assert.Equal(t, at(12, 0), events[0].At, "sorting happens on a copy")
}

func TestModelsAreBrokenDownHeaviestFirst(t *testing.T) {
	events := []usage.Event{
		event(9, 0, "claude-sonnet-5", 100),
		event(9, 10, "claude-opus-5", 900),
		event(9, 20, "claude-sonnet-5", 50),
	}

	window, open := usage.CurrentSession(events, at(10, 0))

	require.True(t, open)
	assert.Equal(t, []usage.ModelUsage{
		{Model: "claude-opus-5", Tokens: 900},
		{Model: "claude-sonnet-5", Tokens: 150},
	}, window.Models)
}

// A nil slice marshals as `null` and the renderer maps over this.
func TestModelsIsNeverNil(t *testing.T) {
	window, open := usage.CurrentSession([]usage.Event{event(9, 0, "", 10)}, at(10, 0))

	require.True(t, open)
	assert.NotNil(t, window.Models)
	assert.Empty(t, window.Models, "an event with no model names no model")
}

func TestTheWeekIsATrailingSevenDays(t *testing.T) {
	now := at(12, 0)
	events := []usage.Event{
		{At: now.Add(-8 * 24 * time.Hour), Model: "claude-opus-5", Tokens: 1_000},
		{At: now.Add(-6 * 24 * time.Hour), Model: "claude-opus-5", Tokens: 20},
		{At: now.Add(-time.Hour), Model: "claude-opus-5", Tokens: 3},
	}

	week := usage.CurrentWeek(events, now)

	assert.Equal(t, int64(23), week.Tokens, "what fell out of the trailing week is not counted")
	assert.Equal(t, now, week.End)
}

func TestBurnPerHourAveragesTheElapsedPart(t *testing.T) {
	window, open := usage.CurrentSession([]usage.Event{event(9, 0, "claude-opus-5", 600)}, at(11, 0))

	require.True(t, open)
	assert.Equal(t, int64(300), window.BurnPerHour(at(11, 0)), "600 over two hours")
}

// One large call in the first seconds of a window extrapolates to a rate nobody could sustain, and
// a panel that announced it would be lying about what is going to happen.
func TestBurnPerHourIsZeroBeforeTheWindowHasRun(t *testing.T) {
	window, open := usage.CurrentSession([]usage.Event{event(9, 0, "claude-opus-5", 50_000)}, at(9, 0))

	require.True(t, open)
	assert.Equal(t, int64(0), window.BurnPerHour(at(9, 0)))
}
