package usage_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/usage"
)

// openTestDB gives the calibration somewhere real to persist, on a throwaway file.
func openTestDB(t *testing.T) *storage.DB {
	t.Helper()

	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// serviceOn builds a service reading a transcript tree under a temporary home, with no database —
// which is also the degraded shape the app registers when storage failed.
func serviceOn(t *testing.T, now time.Time) (*usage.Service, string) {
	t.Helper()

	home := t.TempDir()
	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.ClaudeSource(home), usage.CodexSource(home)),
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		// Claude's CLI is not called in these tests: a binary that does not exist answers an empty
		// plan, which is the "not signed in" path.
		ClaudeBinary: filepath.Join(home, "no-such-binary"),
	})
	return service, filepath.Join(home, ".claude", "projects")
}

func TestSnapshotReportsAProvidersOpenWindow(t *testing.T) {
	now := at(10, 0)
	service, root := serviceOn(t, now)
	writeTranscript(t, filepath.Join(root, "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 100, 50, 0, 0),
		claudeTurn("2026-09-22T09:30:00.000Z", "claude-sonnet-5", 10, 5, 0, 0),
	)

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	claude := providerNamed(t, snapshot, "claude")
	require.NotNil(t, claude.Session)
	assert.Equal(t, int64(165), claude.Session.Tokens)
	assert.Equal(t, "2026-09-22T14:00:00Z", claude.Session.ResetsAt, "five hours from the first turn")
	assert.Equal(t, []usage.ModelUsage{
		{Model: "claude-opus-5", Tokens: 150},
		{Model: "claude-sonnet-5", Tokens: 15},
	}, claude.Session.Models)
}

/*
The heart of the feature's honesty: no ceiling has been observed, so there is no percentage.

A number here would have to be invented, and the whole panel's worth is that its numbers are true.
*/
func TestAWindowHasNoPercentUntilACeilingIsObserved(t *testing.T) {
	now := at(10, 0)
	service, root := serviceOn(t, now)
	writeTranscript(t, filepath.Join(root, "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 100, 0, 0, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	claude := providerNamed(t, snapshot, "claude")
	require.NotNil(t, claude.Session)
	assert.Nil(t, claude.Session.Percent, "nothing to divide by")
	assert.Nil(t, claude.Session.Ceiling)
	assert.Equal(t, int64(100), claude.Session.Tokens, "the consumption is still reported")
}

func TestAProviderThatWasNeverRunReportsNoWindows(t *testing.T) {
	service, _ := serviceOn(t, at(10, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	codex := providerNamed(t, snapshot, "codex")
	assert.Nil(t, codex.Session)
	assert.Nil(t, codex.Week)
}

func TestAnElapsedBlockLeavesNoOpenSessionButStillCountsTheWeek(t *testing.T) {
	now := at(20, 0)
	service, root := serviceOn(t, now)
	writeTranscript(t, filepath.Join(root, "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 100, 0, 0, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	claude := providerNamed(t, snapshot, "claude")
	assert.Nil(t, claude.Session, "the block elapsed hours ago")
	require.NotNil(t, claude.Week)
	assert.Equal(t, int64(100), claude.Week.Tokens)
}

func TestTheDiskFigureCountsWhatTheAppKeeps(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	dataDir := t.TempDir()
	writeTranscript(t, filepath.Join(dataDir, "logs", "shell.log"), "0123456789")

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.ClaudeSource(home)),
		DataDir: dataDir,
		Now:     func() time.Time { return now },
	})

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Equal(t, int64(11), snapshot.Data.Bytes, "ten characters and the newline")
	assert.True(t, snapshot.Data.Complete)
}

// The first reading has no previous one to rate against, and announcing a spike would be a lie
// about a process that has only just started.
func TestTheFirstResourceReadingIsNotARate(t *testing.T) {
	service, _ := serviceOn(t, at(10, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.False(t, snapshot.Resources.Sampled)
	assert.Zero(t, snapshot.Resources.CPUPercent)
	assert.Positive(t, snapshot.Resources.MemoryBytes, "memory is a level, not a rate")
}

// A rate needs two readings and time between them. The clock is injected, so this is the real
// contract rather than whatever the runtime's counters happened to do between two instructions.
func TestTheSecondResourceReadingIsARate(t *testing.T) {
	clock := at(10, 0)
	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(),
		DataDir: t.TempDir(),
		Now:     func() time.Time { return clock },
	})

	_, err := service.Snapshot(t.Context())
	require.NoError(t, err)

	clock = clock.Add(5 * time.Second)
	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.True(t, snapshot.Resources.Sampled)
	assert.GreaterOrEqual(t, snapshot.Resources.CPUPercent, 0.0)
	assert.LessOrEqual(t, snapshot.Resources.CPUPercent, 100.0, "a share of the machine, never more")
}

// Two readings at the same instant cannot be a rate, and reporting one would be inventing it.
func TestTwoReadingsAtTheSameInstantAreNotARate(t *testing.T) {
	service, _ := serviceOn(t, at(10, 0))

	_, err := service.Snapshot(t.Context())
	require.NoError(t, err)
	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.False(t, snapshot.Resources.Sampled)
	assert.Zero(t, snapshot.Resources.CPUPercent)
}

/*
The wire contract (the repo's rule, and the failure it prevents).

A nil slice marshals as `null`, the renderer calls `.map` on it, and the panel crashes on exactly
the case that is most common: a fresh install with nothing measured yet.
*/
func TestNoSliceCrossesAsNull(t *testing.T) {
	service, root := serviceOn(t, at(10, 0))
	writeTranscript(t, filepath.Join(root, "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 0, 0, 0))

	snapshot, err := service.Snapshot(t.Context())
	require.NoError(t, err)

	encoded, err := json.Marshal(snapshot)

	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"providers":null`)
	assert.NotContains(t, string(encoded), `"models":null`)
}

func TestAnEmptySnapshotStillMarshalsWithArrays(t *testing.T) {
	service := usage.NewService(usage.Deps{DataDir: t.TempDir(), Now: func() time.Time { return at(10, 0) }})

	snapshot, err := service.Snapshot(t.Context())
	require.NoError(t, err)

	encoded, err := json.Marshal(snapshot)

	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"providers":[]`, "no reader at all still answers an array")
}

/*
Calibration: the only path to a percentage.

Written after finding the wiring missing — `ObserveLimit` existed and nothing called it, so the
percentage could never have appeared no matter how often the user ran out. These cover both halves:
the hook reacts to the right failures, and a learned ceiling turns into a percentage.
*/
func TestAQuotaFailureTeachesTheCeilingAndUnlocksThePercentage(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	db := openTestDB(t)
	root := filepath.Join(home, ".claude", "projects")

	service := usage.NewService(usage.Deps{
		Reader:         usage.NewReader(usage.ClaudeSource(home)),
		Store:          usage.NewStore(db),
		DataDir:        t.TempDir(),
		Now:            func() time.Time { return now },
		ActiveProvider: func(context.Context) string { return "claude" },
	})
	writeTranscript(t, filepath.Join(root, "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 800, 200, 0, 0))

	before, err := service.Snapshot(t.Context())
	require.NoError(t, err)
	require.Nil(t, providerNamed(t, before, "claude").Session.Percent, "nothing learned yet")

	// What the bridge hands every failed command. Only the quota sentinel means anything here.
	service.NoteFailure("send_chat_message", errors.New(sentinel.QuotaExceeded+"usage limit reached"))

	after, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	session := providerNamed(t, after, "claude").Session
	require.NotNil(t, session.Ceiling)
	assert.Equal(t, int64(1_000), *session.Ceiling, "what the window held when it ran out")
	require.NotNil(t, session.Percent)
	assert.InDelta(t, 100.0, *session.Percent, 0.01, "at the limit it just learned")
}

func TestOnlyAQuotaFailureTeachesAnything(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	db := openTestDB(t)

	service := usage.NewService(usage.Deps{
		Reader:         usage.NewReader(usage.ClaudeSource(home)),
		Store:          usage.NewStore(db),
		DataDir:        t.TempDir(),
		Now:            func() time.Time { return now },
		ActiveProvider: func(context.Context) string { return "claude" },
	})
	writeTranscript(t, filepath.Join(home, ".claude", "projects", "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 800, 200, 0, 0))

	service.NoteFailure("get_status", errors.New("fatal: not a git repository"))
	service.NoteFailure("send_chat_message", errors.New(sentinel.AuthExpired+"please sign in"))
	service.NoteFailure("send_chat_message", nil)

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Nil(t, providerNamed(t, snapshot, "claude").Session.Percent,
		"an ordinary failure is not a limit")
}

// A ceiling only moves up: running out early proves the window held at least that much.
func TestALaterLargerCeilingReplacesTheEarlierOne(t *testing.T) {
	now := at(10, 0)
	db := openTestDB(t)
	store := usage.NewStore(db)

	require.NoError(t, store.Observe(t.Context(), usage.Ceiling{
		Provider: "claude", Plan: "pro", Window: usage.SessionWindow, Tokens: 900, SeenAt: now,
	}))
	require.NoError(t, store.Observe(t.Context(), usage.Ceiling{
		Provider: "claude", Plan: "pro", Window: usage.SessionWindow, Tokens: 1_500, SeenAt: now,
	}))
	require.NoError(t, store.Observe(t.Context(), usage.Ceiling{
		Provider: "claude", Plan: "pro", Window: usage.SessionWindow, Tokens: 400, SeenAt: now,
	}))

	learned, known := store.Lookup(t.Context(), "claude", "pro", usage.SessionWindow)

	require.True(t, known)
	assert.Equal(t, int64(1_500), learned, "the largest observation wins")
}

func TestCeilingsAreSeparatePerWindow(t *testing.T) {
	now := at(10, 0)
	store := usage.NewStore(openTestDB(t))

	require.NoError(t, store.Observe(t.Context(), usage.Ceiling{
		Provider: "claude", Plan: "pro", Window: usage.SessionWindow, Tokens: 1_000, SeenAt: now,
	}))

	_, sessionKnown := store.Lookup(t.Context(), "claude", "pro", usage.SessionWindow)
	_, weekKnown := store.Lookup(t.Context(), "claude", "pro", usage.WeekWindow)

	assert.True(t, sessionKnown)
	assert.False(t, weekKnown, "the week's limit is a different number nobody has reached yet")
}

// providerNamed finds one provider in a snapshot, failing the test when it is missing.
func providerNamed(t *testing.T, snapshot usage.Snapshot, name string) usage.ProviderUsage {
	t.Helper()

	for _, provider := range snapshot.Providers {
		if provider.Provider == name {
			return provider
		}
	}
	t.Fatalf("%s is not in the snapshot", name)
	return usage.ProviderUsage{}
}
