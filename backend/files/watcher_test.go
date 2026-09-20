package files_test

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The watcher's tests run against the real filesystem and a real OS watcher. A fake would prove
// nothing about the thing being tested: the throttle exists because of how bursts actually arrive.

// waitFor polls until the condition holds or the deadline passes, so a slow machine costs seconds
// rather than a flake. Never a bare sleep — the timings here are 200 ms and 400 ms, and a fixed
// wait long enough to be safe on CI would make the suite crawl.
func waitFor(t *testing.T, within time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return condition()
}

func changeEvents(recorder *bridge.RecordingEmitter) int {
	return len(recorder.Named("repo:fs-changed"))
}

// requireWatcherSupported skips a test that would start a real OS watch, in the one configuration
// where starting one aborts the process. **Every test in this file that calls `Start` calls this
// first**, which is why it is a helper rather than a line inside `startWatching`: three of them
// build their own registry.
//
// `syncthing/notify`'s Windows backend walks the `FILE_NOTIFY_INFORMATION` records the kernel
// writes by converting an offset into its read buffer with `unsafe.Pointer`, and `checkptr` —
// which exists only in a `-race` build — rejects the conversion:
//
//	fatal error: checkptr: converted pointer straddles multiple allocations
//	.../notify@v0.0.0-20250528144937-c7027d4f7465/watcher_readdcw.go:406
//
// It is a fatal error, so it takes the whole package's run with it, and it is not ours: no frame of
// this repository appears in the trace. There is no newer version to move to either — the pinned
// pseudo-version is the latest the proxy offers.
//
// Skipped in that configuration rather than on Windows outright: without `-race` there is no
// `checkptr`, so these tests still run for a developer on Windows and in the manual acceptance
// pass, which is where "does the watcher actually work there" gets answered. Recorded as
// `BUG-FILE-b`, because a release build has no `checkptr` either — what ships carries the same
// arithmetic unchecked rather than crashing on it.
func requireWatcherSupported(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" && raceEnabled {
		t.Skip("syncthing/notify trips checkptr on Windows under -race (BUG-FILE-b); run without -race there")
	}
}

func startWatching(t *testing.T, repo string) *bridge.RecordingEmitter {
	t.Helper()
	requireWatcherSupported(t)

	recorder := &bridge.RecordingEmitter{}
	registry := files.NewWatcherRegistry(recorder)
	require.NoError(t, registry.Start(repo))
	t.Cleanup(registry.StopAll)

	// The OS watcher is not necessarily in place the instant Watch returns; a write in that window
	// would be missed and the test would blame the throttle.
	time.Sleep(100 * time.Millisecond)
	return recorder
}

func TestAChangeIsReported(t *testing.T) {
	repo := t.TempDir()
	recorder := startWatching(t, repo)

	write(t, repo, "a.txt", "content\n")

	require.True(t, waitFor(t, 2*time.Second, func() bool { return changeEvents(recorder) > 0 }))
	payload := recorder.Named("repo:fs-changed")[0].(map[string]string)
	assert.Equal(t, repo, payload["repo_path"], "the renderer keys its refresh on this")
}

// The leading edge: the first event of a burst emits immediately rather than waiting out a window.
func TestTheFirstChangeEmitsImmediately(t *testing.T) {
	repo := t.TempDir()
	recorder := startWatching(t, repo)

	write(t, repo, "a.txt", "content\n")

	assert.True(t, waitFor(t, 300*time.Millisecond, func() bool { return changeEvents(recorder) > 0 }),
		"a user's first save must not wait out a throttle window")
}

// The bug the whole design exists to prevent: several files written in a row — an AI agent's edit
// tool — where everything after the first would vanish under a plain leading-edge throttle.
func TestAWriteInsideTheWindowIsFlushedNotDropped(t *testing.T) {
	repo := t.TempDir()
	recorder := startWatching(t, repo)

	write(t, repo, "first.txt", "one\n")
	require.True(t, waitFor(t, time.Second, func() bool { return changeEvents(recorder) >= 1 }))

	// Well inside the 400 ms window, and then nothing else happens: there is no later event to
	// wake a plain throttle back up.
	time.Sleep(50 * time.Millisecond)
	write(t, repo, "second.txt", "two\n")

	assert.True(t, waitFor(t, 2*time.Second, func() bool { return changeEvents(recorder) >= 2 }),
		"the catch-up tick has to flush it, or the second write is invisible until a reload")
}

// A burst is coalesced rather than replayed: the renderer refreshes the whole tree per event, and
// five hundred of them would be five hundred full reloads.
func TestABurstIsCoalesced(t *testing.T) {
	repo := t.TempDir()
	recorder := startWatching(t, repo)

	for i := range 200 {
		write(t, repo, filepath.Join("burst", "file"+string(rune('a'+i%26))+".txt"), "x\n")
	}

	require.True(t, waitFor(t, 2*time.Second, func() bool { return changeEvents(recorder) > 0 }))
	time.Sleep(600 * time.Millisecond)

	assert.Less(t, changeEvents(recorder), 10,
		"200 writes must not become 200 refreshes")
}

// FILE-013: git rewrites these constantly during ordinary operations, and reacting to them means
// refreshing on git's own churn — a status read would trigger a refresh, which triggers a read.
func TestGitBookkeepingIsNotAChange(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, files.CreateDir(repo, ".git"))
	recorder := startWatching(t, repo)

	for _, name := range []string{".git/index.lock", ".git/FETCH_HEAD", ".git/COMMIT_EDITMSG"} {
		write(t, repo, name, "noise\n")
	}

	time.Sleep(800 * time.Millisecond)
	assert.Zero(t, changeEvents(recorder), "none of the three is a user-visible change")
}

// A real change alongside the noise still reports: the filter is per path, and one of them is real.
func TestARealChangeAlongsideNoiseStillReports(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, files.CreateDir(repo, ".git"))
	recorder := startWatching(t, repo)

	write(t, repo, ".git/index.lock", "noise\n")
	write(t, repo, "real.txt", "the user's work\n")

	assert.True(t, waitFor(t, 2*time.Second, func() bool { return changeEvents(recorder) > 0 }))
}

// Restarting rather than duplicating: the renderer starts a watch whenever the active project
// changes, and a second watcher on the same path would keep reporting after the first is stopped.
//
// Asserted through stopping rather than by counting emissions. One save produces several OS events
// — create, write, attributes — so the count is a property of the platform, not of this code; what
// *is* this code's property is that one Stop silences everything.
func TestStartingTwiceReplacesRatherThanDuplicates(t *testing.T) {
	requireWatcherSupported(t)

	repo := t.TempDir()
	recorder := &bridge.RecordingEmitter{}
	registry := files.NewWatcherRegistry(recorder)
	t.Cleanup(registry.StopAll)

	require.NoError(t, registry.Start(repo))
	require.NoError(t, registry.Start(repo))
	time.Sleep(100 * time.Millisecond)

	registry.Stop(repo)
	assertSilentAfter(t, recorder, repo)
}

func TestStoppingEndsTheReports(t *testing.T) {
	requireWatcherSupported(t)

	repo := t.TempDir()
	recorder := &bridge.RecordingEmitter{}
	registry := files.NewWatcherRegistry(recorder)
	require.NoError(t, registry.Start(repo))
	time.Sleep(100 * time.Millisecond)

	registry.Stop(repo)
	assertSilentAfter(t, recorder, repo)
}

// assertSilentAfter writes to a repository and asserts nothing new is reported.
//
// It measures growth rather than the total, because starting a watch can itself produce an event
// for the directory — FSEvents reports the watched root — and a test asserting zero would be
// asserting the platform's behaviour rather than this code's.
func assertSilentAfter(t *testing.T, recorder *bridge.RecordingEmitter, repo string) {
	t.Helper()
	before := changeEvents(recorder)

	write(t, repo, "after-the-stop.txt", "content\n")
	time.Sleep(700 * time.Millisecond)

	assert.Equal(t, before, changeEvents(recorder), "the watch is over")
}

// The renderer stops watching on unmount, which can follow a start that failed.
func TestStoppingSomethingThatIsNotRunning(t *testing.T) {
	registry := files.NewWatcherRegistry(nil)

	assert.NotPanics(t, func() {
		registry.Stop(t.TempDir())
		registry.StopAll()
	})
}
