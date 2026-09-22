// Package usage measures what the app and its AI agents are consuming, for the indicator in the
// bottom-right corner (USAGE-001).
//
// **Everything here is measured, never estimated.** The providers this app drives publish no quota
// API — Anthropic closed the request for one as not planned — so the numbers come from what their
// CLIs write to disk about their own work, and from the operating system about this process. Where
// there is no source, the answer is "unavailable", not a guess. That is `AGENTS.md`'s "Do not
// guess" applied to a panel whose whole worth is that its numbers are true.
package usage

import (
	"sort"
	"time"
)

// SessionLength is the rolling window a Claude subscription is metered against.
//
// **Inferred, not documented** (USAGE-003). Anthropic publishes neither the length nor the exact
// semantics of the window; five hours is what its own interface reports and what every third-party
// reader assumes. If that is ever wrong, the reset time is wrong with it — which is why the panel
// labels the reset as derived rather than as something the provider told us.
const SessionLength = 5 * time.Hour

// WeekLength is the second window a subscription is metered against, on top of the session.
const WeekLength = 7 * 24 * time.Hour

// Event is one measured call: when it happened, which model answered, and what it cost.
//
// The three fields are the whole of what the readers extract from a provider's logs. Nothing else
// is read, and in particular no message content ever reaches this struct (USAGE-002).
type Event struct {
	At     time.Time
	Model  string
	Tokens int64
}

// Window is a bounded stretch of time and everything spent inside it.
type Window struct {
	Start  time.Time
	End    time.Time
	Tokens int64
	// Models is what each model spent inside this window, heaviest first. Never nil.
	Models []ModelUsage
}

// BurnPerHour is the average spend across the part of the window that has elapsed.
//
// Answers zero before a meaningful stretch has passed rather than extrapolating from the first
// seconds, where one large call reads as an impossible rate.
func (w Window) BurnPerHour(now time.Time) int64 {
	elapsed := now.Sub(w.Start)
	if elapsed < time.Minute || w.Tokens == 0 {
		return 0
	}
	return int64(float64(w.Tokens) / elapsed.Hours())
}

/*
CurrentSession finds the rolling block that is open now, and what was spent in it.

The rule: a block opens with the first call made outside any open block and stays open for
`SessionLength`. A call made after it closes opens the next one. So the events are walked in order,
carrying the block forward, and whatever block the last event belongs to is the one that matters —
if it has not expired by `now`.

Answers ok=false when nothing is open: either there are no events at all, or the last block has
already elapsed, which is the ordinary state of somebody who has not worked in a while. That is not
a failure and the panel shows it as an empty window, not as an error.
*/
func CurrentSession(events []Event, now time.Time) (Window, bool) {
	ordered := sortedByTime(events)
	if len(ordered) == 0 {
		return Window{}, false
	}

	start := ordered[0].At
	for _, event := range ordered {
		// Outside the block that is open: this call opens the next one.
		if !event.At.Before(start.Add(SessionLength)) {
			start = event.At
		}
	}

	end := start.Add(SessionLength)
	if !now.Before(end) {
		return Window{}, false
	}

	return spend(ordered, start, end), true
}

// CurrentWeek is everything spent in the seven days up to now.
//
// A plain trailing window rather than a calendar week: the limit it stands for is a rolling one,
// and a Monday reset would report a budget nobody has.
func CurrentWeek(events []Event, now time.Time) Window {
	return spend(sortedByTime(events), now.Add(-WeekLength), now)
}

// spend totals the events falling inside [start, end) and breaks them down by model.
func spend(ordered []Event, start, end time.Time) Window {
	window := Window{Start: start, End: end, Models: []ModelUsage{}}
	byModel := map[string]int64{}

	for _, event := range ordered {
		if event.At.Before(start) || !event.At.Before(end) {
			continue
		}
		window.Tokens += event.Tokens
		if event.Model != "" {
			byModel[event.Model] += event.Tokens
		}
	}

	for model, tokens := range byModel {
		window.Models = append(window.Models, ModelUsage{Model: model, Tokens: tokens})
	}
	// Heaviest first, and by name when two are level, so the panel does not reshuffle between polls.
	sort.Slice(window.Models, func(i, j int) bool {
		if window.Models[i].Tokens != window.Models[j].Tokens {
			return window.Models[i].Tokens > window.Models[j].Tokens
		}
		return window.Models[i].Model < window.Models[j].Model
	})

	return window
}

// sortedByTime copies the events into chronological order, because the readers walk files in
// whatever order the filesystem hands them over and the block rule only works in sequence.
func sortedByTime(events []Event) []Event {
	ordered := make([]Event, len(events))
	copy(ordered, events)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
	return ordered
}
