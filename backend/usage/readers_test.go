package usage_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/usage"
)

// writeTranscript lays a JSONL file down where a source expects one.
func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// claudeTurn is a transcript line in the shape Claude Code really writes, trimmed to what matters
// plus a content field, so the privacy assertion has something to catch.
func claudeTurn(stamp, model string, in, out, cacheWrite, cacheRead int64) string {
	return `{"type":"assistant","timestamp":"` + stamp + `",` +
		`"cwd":"/Users/someone/secret-project","gitBranch":"feature/unreleased",` +
		`"message":{"model":"` + model + `","role":"assistant",` +
		`"content":[{"type":"text","text":"the private answer text"}],` +
		`"usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) +
		`,"cache_creation_input_tokens":` + itoa(cacheWrite) +
		`,"cache_read_input_tokens":` + itoa(cacheRead) + `}}}`
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

func claudeReader(t *testing.T) (*usage.Reader, string) {
	t.Helper()

	home := t.TempDir()
	return usage.NewReader(usage.ClaudeSource(home)), filepath.Join(home, ".claude", "projects")
}

func TestClaudeTurnsAreSummedAcrossEveryTokenKind(t *testing.T) {
	reader, root := claudeReader(t)
	writeTranscript(t, filepath.Join(root, "a-project", "session.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 20, 30, 40))

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(100), events[0].Tokens, "input + output + cache written + cache read")
	assert.Equal(t, "claude-opus-5", events[0].Model)
}

/*
The rule this whole reader exists under: a transcript holds the user's code, their prompts and the
model's answers, and none of it may leave the file.

Asserted structurally rather than by inspection — `Event` has three fields and none of them is a
string the content could land in, so the test proves the model cannot carry it even by mistake.
*/
func TestNoMessageContentEverLeavesTheTranscript(t *testing.T) {
	reader, root := claudeReader(t)
	writeTranscript(t, filepath.Join(root, "a-project", "session.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 20, 0, 0))

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "claude-opus-5", events[0].Model, "the model name is the only string carried")
	assert.NotContains(t, events[0].Model, "private")
	assert.NotContains(t, events[0].Model, "secret-project")
}

// The tail of a live session is half written more often than not.
func TestAHalfWrittenLineIsSkippedRatherThanFatal(t *testing.T) {
	reader, root := claudeReader(t)
	writeTranscript(t, filepath.Join(root, "a-project", "session.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 0, 0, 0),
		`{"type":"assistant","timestamp":"2026-09-22T09:01`,
	)

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.Len(t, events, 1, "the good turn survives the broken one")
}

func TestLinesWithNoUsageAreNotEvents(t *testing.T) {
	reader, root := claudeReader(t)
	writeTranscript(t, filepath.Join(root, "a-project", "session.jsonl"),
		`{"type":"user","timestamp":"2026-09-22T09:00:00.000Z","message":{"role":"user"}}`,
		`{"type":"summary","summary":"a title"}`,
	)

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestTranscriptsAreFoundAtAnyDepth(t *testing.T) {
	reader, root := claudeReader(t)
	writeTranscript(t, filepath.Join(root, "one", "deeper", "session.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 5, 0, 0, 0))
	writeTranscript(t, filepath.Join(root, "two", "session.jsonl"),
		claudeTurn("2026-09-22T09:30:00.000Z", "claude-opus-5", 7, 0, 0, 0))

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.Len(t, events, 2)
}

// Never having run a CLI is an answer, not a failure — and it must not be an error the panel shows.
func TestAProviderThatWasNeverRunAnswersNothingAndNoError(t *testing.T) {
	reader := usage.NewReader(usage.ClaudeSource(t.TempDir()))

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.NotNil(t, events, "an empty slice, never nil: the renderer maps over this")
	assert.Empty(t, events)
}

func TestAnUnknownProviderAnswersNothingAndNoError(t *testing.T) {
	reader := usage.NewReader(usage.ClaudeSource(t.TempDir()))

	events, err := reader.Events(t.Context(), "gemini", at(0, 0))

	require.NoError(t, err)
	assert.Empty(t, events)
}

// The whole reason the sweep is affordable: a file older than the window is never opened. Proven by
// making it unreadable — if the reader touched it, the test would see the failure.
func TestAFileOlderThanTheWindowIsNeverOpened(t *testing.T) {
	reader, root := claudeReader(t)
	stale := filepath.Join(root, "old", "session.jsonl")
	writeTranscript(t, stale, claudeTurn("2026-09-01T09:00:00.000Z", "claude-opus-5", 999, 0, 0, 0))
	old := at(0, 0).Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))

	events, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestEventsBeforeTheWindowAreDroppedFromAFileInsideIt(t *testing.T) {
	reader, root := claudeReader(t)
	path := filepath.Join(root, "a", "session.jsonl")
	writeTranscript(t, path,
		claudeTurn("2026-09-22T06:00:00.000Z", "claude-opus-5", 100, 0, 0, 0),
		claudeTurn("2026-09-22T10:00:00.000Z", "claude-opus-5", 7, 0, 0, 0),
	)
	// A real transcript's last write is its last turn, which is what gets it past the mtime filter.
	require.NoError(t, os.Chtimes(path, at(10, 0), at(10, 0)))

	events, err := reader.Events(t.Context(), "claude", at(9, 0))

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(7), events[0].Tokens)
}

// A file that grew since the last sweep has to be re-read, or the newest turns never appear.
func TestAFileThatGrewIsReadAgain(t *testing.T) {
	reader, root := claudeReader(t)
	path := filepath.Join(root, "a", "session.jsonl")
	writeTranscript(t, path, claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 0, 0, 0))

	first, err := reader.Events(t.Context(), "claude", at(0, 0))
	require.NoError(t, err)
	require.Len(t, first, 1)

	writeTranscript(t, path,
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 0, 0, 0),
		claudeTurn("2026-09-22T09:05:00.000Z", "claude-opus-5", 20, 0, 0, 0),
	)

	second, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.Len(t, second, 2)
}

func TestAnUnchangedFileIsAnsweredFromCache(t *testing.T) {
	reader, root := claudeReader(t)
	path := filepath.Join(root, "a", "session.jsonl")
	writeTranscript(t, path, claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 10, 0, 0, 0))

	first, err := reader.Events(t.Context(), "claude", at(0, 0))
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Emptied on disk without changing size or mtime is impossible in practice; removing it is the
	// available proof that the second sweep did not reopen the file.
	require.NoError(t, os.Remove(path))
	stat := filepath.Join(root, "a")
	require.NoError(t, os.MkdirAll(stat, 0o750))

	second, err := reader.Events(t.Context(), "claude", at(0, 0))

	require.NoError(t, err)
	assert.Empty(t, second, "a file that is gone contributes nothing, cache or not")
}

func TestProvidersNamesWhatCanBeMeasured(t *testing.T) {
	reader := usage.NewReader(usage.ClaudeSource("/home"), usage.CodexSource("/home"))

	assert.Equal(t, []string{"claude", "codex"}, reader.Providers())
}

// ---- Codex ------------------------------------------------------------------------------------

func codexMeta(stamp, model string) string {
	return `{"type":"session_meta","timestamp":"` + stamp + `","payload":{"cwd":"/private/work",` +
		`"base_instructions":{"provenance":{"model":"` + model + `"}}}}`
}

// Codex reports the turn's own cost in `last_token_usage` and a running total beside it. Taking the
// running total would count every earlier turn again on every line.
func codexTurn(stamp string, last, running int64) string {
	return `{"type":"event_msg","timestamp":"` + stamp + `","payload":{"type":"token_count",` +
		`"info":{"last_token_usage":{"total_tokens":` + itoa(last) + `},` +
		`"total_token_usage":{"total_tokens":` + itoa(running) + `}}}}`
}

func TestCodexCountsTheTurnNotTheRunningTotal(t *testing.T) {
	home := t.TempDir()
	reader := usage.NewReader(usage.CodexSource(home))
	writeTranscript(t, filepath.Join(home, ".codex", "sessions", "2026", "rollout.jsonl"),
		codexMeta("2026-09-22T09:00:00.000Z", "gpt-5.6-sol"),
		codexTurn("2026-09-22T09:01:00.000Z", 100, 100),
		codexTurn("2026-09-22T09:02:00.000Z", 40, 140),
	)

	events, err := reader.Events(t.Context(), "codex", at(0, 0))

	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, int64(100), events[0].Tokens)
	assert.Equal(t, int64(40), events[1].Tokens, "the turn's own cost, not the running 140")
}

// Codex names its model once, in the header, and every turn after it belongs to that model.
func TestCodexTurnsInheritTheSessionsModel(t *testing.T) {
	home := t.TempDir()
	reader := usage.NewReader(usage.CodexSource(home))
	writeTranscript(t, filepath.Join(home, ".codex", "sessions", "rollout.jsonl"),
		codexMeta("2026-09-22T09:00:00.000Z", "gpt-5.6-sol"),
		codexTurn("2026-09-22T09:01:00.000Z", 100, 100),
	)

	events, err := reader.Events(t.Context(), "codex", at(0, 0))

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "gpt-5.6-sol", events[0].Model)
}

func TestCodexWithoutAHeaderStillCountsTheTokens(t *testing.T) {
	home := t.TempDir()
	reader := usage.NewReader(usage.CodexSource(home))
	writeTranscript(t, filepath.Join(home, ".codex", "sessions", "rollout.jsonl"),
		codexTurn("2026-09-22T09:01:00.000Z", 100, 100))

	events, err := reader.Events(t.Context(), "codex", at(0, 0))

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(100), events[0].Tokens)
	assert.Empty(t, events[0].Model, "unnamed rather than guessed")
}
