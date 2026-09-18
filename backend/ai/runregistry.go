package ai

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

// Making a run observable and cancellable (AI-011 … AI-013).
//
// Two things the registry exists to get right, and both are about what the user sees:
//
//   - # ai:output is an activity log, never the answer
//
// Every line a process prints while it runs is emitted live. The reply is extracted afterwards,
// from the terminal event, inside the engine's own interpretation — never assembled from the lines
// that streamed past. For Claude those lines include tool-use events that are valid JSON and are
// not the answer; a consumer rendering them as a partial reply shows the user the agent's
// scratchpad and calls it the result.
//
//   - # The deadline bounds silence, not length
//
// Ten minutes without output, pushed back on every read. As a total budget it killed reviews that
// were doing exactly what they were asked — an Opus review died mid-answer, observed 2026-08-18 —
// while catching a genuinely hung child no faster. A working agent writes continuously; a hung one
// writes nothing. The accepted cost is that a runaway agent which keeps talking is unbounded, and
// a stop is one click away.

const (
	// maxLineChars caps one emitted line. A CLI echoing a whole file would otherwise put it in
	// the activity log a character at a time.
	maxLineChars = 2000
	// maxTraceLines is the ring buffer a traced run keeps, oldest dropped first. It is persisted
	// with a chat turn, so it has to stay small enough to store.
	maxTraceLines = 300
	// DefaultRunTimeout is how long a run may stay silent before it is killed.
	DefaultRunTimeout = 10 * time.Minute
)

// Stream names, as the renderer types them.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// outputEvent is the payload of `ai:output`.
type outputEvent struct {
	RunID  string `json:"run_id"`
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

// RunRegistry owns the runs currently in flight.
type RunRegistry struct {
	emitter bridge.Emitter
	timeout time.Duration

	mu   sync.Mutex
	runs map[string]*Run
}

// NewRunRegistry builds the registry. A zero timeout means the default; an explicit one is a test
// seam and nothing else.
func NewRunRegistry(emitter bridge.Emitter, timeout time.Duration) *RunRegistry {
	if emitter == nil {
		emitter = bridge.NopEmitter{}
	}
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	return &RunRegistry{emitter: emitter, timeout: timeout, runs: make(map[string]*Run, 2)}
}

// Run is one in-flight operation: what to emit under, how to stop it, and why it stopped.
type Run struct {
	id        string
	emitter   bridge.Emitter
	timeout   time.Duration
	withTrace bool

	// stop is closed once, by whichever of cancel or the deadline fires first.
	stop     chan struct{}
	stopOnce sync.Once

	mu        sync.Mutex
	trace     []string
	timedOut  bool
	cancelled bool
	// lastRead is pushed forward by every read, which is what makes the deadline measure silence.
	lastRead time.Time
}

// Begin registers a run and returns it.
//
// **Registered before the work starts, not at spawn.** A user who clicks stop while the binary is
// still being resolved is cancelling a run that exists as far as they are concerned, and a
// registry populated at spawn would answer "no such run" and leave the panel spinning.
//
// withTrace decides whether the emitted lines are also kept: a chat turn persists its trace, and
// every other run does not. Keeping one for all of them would store a few hundred lines per review
// that nothing ever reads.
func (r *RunRegistry) Begin(id string, withTrace bool) *Run {
	run := &Run{
		id:        id,
		emitter:   r.emitter,
		timeout:   r.timeout,
		withTrace: withTrace,
		stop:      make(chan struct{}),
		lastRead:  time.Now(),
	}
	if withTrace {
		run.trace = make([]string, 0, 32)
	}

	r.mu.Lock()
	r.runs[id] = run
	r.mu.Unlock()
	return run
}

// End removes a run. Safe to call for one that was already cancelled.
func (r *RunRegistry) End(id string) {
	r.mu.Lock()
	delete(r.runs, id)
	r.mu.Unlock()
}

// Cancel stops a run, reporting whether there was one to stop.
//
// **False rather than an error** for an unknown id, and that covers the ordinary race: the run
// finished between the panel rendering its stop button and the user pressing it. Failing the
// command there would put an error banner over a run that completed successfully.
func (r *RunRegistry) Cancel(id string) bool {
	r.mu.Lock()
	run, found := r.runs[id]
	r.mu.Unlock()

	if !found {
		return false
	}
	run.markCancelled()
	return true
}

// CancelAll stops every run, for shutdown.
func (r *RunRegistry) CancelAll() {
	r.mu.Lock()
	runs := make([]*Run, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()

	for _, run := range runs {
		run.markCancelled()
	}
}

// ---- one run ------------------------------------------------------------------------------------

// ID is the run id the renderer knows it by.
func (run *Run) ID() string { return run.id }

// Stopped is closed when the run should stop, for whichever reason.
func (run *Run) Stopped() <-chan struct{} { return run.stop }

// Timeout is the silence window this run is held to.
func (run *Run) Timeout() time.Duration { return run.timeout }

func (run *Run) markCancelled() {
	run.mu.Lock()
	run.cancelled = true
	run.mu.Unlock()
	run.stopOnce.Do(func() { close(run.stop) })
}

func (run *Run) markTimedOut() {
	run.mu.Lock()
	run.timedOut = true
	run.mu.Unlock()
	run.stopOnce.Do(func() { close(run.stop) })
}

// Touch pushes the deadline back.
//
// Called from the read itself, **not** from the emit: emitting drops blank lines and does nothing
// at all for an untracked run, so a CLI printing only whitespace would be judged dead by a
// deadline anchored there.
func (run *Run) Touch() {
	run.mu.Lock()
	run.lastRead = time.Now()
	run.mu.Unlock()
}

// silentFor is how long it has been since the last read.
func (run *Run) silentFor() time.Duration {
	run.mu.Lock()
	defer run.mu.Unlock()
	return time.Since(run.lastRead)
}

// WatchSilence enforces the deadline until the run stops.
//
// It polls rather than resetting a timer on every read, because a read can arrive thousands of
// times a second from a chatty agent and rescheduling a timer that often costs more than the check
// it replaces.
func (run *Run) WatchSilence() {
	// A tenth of the window: fine enough that the reported deadline is honest, coarse enough to
	// cost nothing.
	interval := run.timeout / 10
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-run.stop:
			return
		case <-ticker.C:
			if run.silentFor() >= run.timeout {
				run.markTimedOut()
				return
			}
		}
	}
}

// EmitLine streams one line of output (AI-012).
//
// Trailing whitespace trimmed, an empty result dropped entirely, and the rest capped — a CLI that
// echoes a minified bundle should cost one long line in the log, not a megabyte in every listener.
func (run *Run) EmitLine(stream, line string) {
	line = strings.TrimRight(line, " \t\r\n")
	if line == "" {
		return
	}

	if runes := []rune(line); len(runes) > maxLineChars {
		line = string(runes[:maxLineChars]) + "…"
	}

	run.emitter.Emit("ai:output", outputEvent{RunID: run.id, Stream: stream, Line: line})

	if !run.withTrace {
		return
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	run.trace = append(run.trace, line)
	if len(run.trace) > maxTraceLines {
		run.trace = run.trace[len(run.trace)-maxTraceLines:]
	}
}

// Trace is what a traced run kept, newest last. Always a slice, never nil — it is persisted as JSON
// and the renderer maps over it.
func (run *Run) Trace() []string {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.trace == nil {
		return []string{}
	}
	return append(make([]string, 0, len(run.trace)), run.trace...)
}

// StopReason is the error a stopped run fails with, or the empty string if it was not stopped.
//
// The two markers say opposite things to the person reading the panel — "you stopped this" against
// "this never finished on its own" — which is why they are separate sentinels rather than one
// "did not complete".
func (run *Run) StopReason() string {
	run.mu.Lock()
	defer run.mu.Unlock()

	switch {
	case run.cancelled:
		// Nothing after the marker: the user knows why, and a sentence would only be noise under
		// a banner they caused.
		return sentinel.RunCancelled

	case run.timedOut:
		// The deadline in whole minutes, and **nothing** under a minute — which only a test's
		// deadline is, and where the renderer has wording that names no duration.
		if minutes := int(run.timeout / time.Minute); minutes >= 1 {
			return fmt.Sprintf("%s%d", sentinel.RunTimedOut, minutes)
		}
		return sentinel.RunTimedOut

	default:
		return ""
	}
}

// Cancelled reports whether the user stopped this run, as opposed to it timing out. The two are
// stored separately because a shutdown racing the deadline should read as a cancellation.
func (run *Run) Cancelled() bool {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.cancelled
}
