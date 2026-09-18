package ai_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingTurns stands in for the activity log.
type recordingTurns struct {
	written  []ai.NewTurn
	previous *string
	err      error
}

func (r *recordingTurns) RecordTurn(_ context.Context, turn ai.NewTurn) (string, error) {
	r.written = append(r.written, turn)
	return "2026-09-17T23:00:00Z", r.err
}

func (r *recordingTurns) LastTurnProvider(context.Context, string, string) (*string, error) {
	return r.previous, nil
}

// recordingCheckpoints remembers what was protected and what was dropped afterwards.
type recordingCheckpoints struct {
	created   []string
	removed   []string
	createErr error
}

func (c *recordingCheckpoints) CreateCheckpoint(_ context.Context, _, kind string) (string, error) {
	if c.createErr != nil {
		return "", c.createErr
	}
	c.created = append(c.created, kind)
	return "1700000000-abcdef12", nil
}

func (c *recordingCheckpoints) RemoveCheckpointIfUnchanged(_ context.Context, _, id string) (bool, error) {
	c.removed = append(c.removed, id)
	return true, nil
}

func chatRequest(message string) ai.ChatRequest {
	return ai.ChatRequest{
		ProjectID:      "proj-1",
		WorkingDir:     "/repo",
		Message:        message,
		ConversationID: "conv-1",
	}
}

func TestAChatTurnIsRecordedWithItsRouting(t *testing.T) {
	operations, _ := ollamaOperations(t, "the answer")
	turns := &recordingTurns{}

	reply, err := operations.Chat(t.Context(), turns, nil, chatRequest("what does this do?"))

	require.NoError(t, err)
	assert.Equal(t, "the answer", reply.Text)
	assert.Equal(t, "ollama", reply.Provider)
	assert.Equal(t, "2026-09-17T23:00:00Z", reply.CreatedAt,
		"from the stored row, so a reopened conversation names the same instant")
	assert.Positive(t, reply.ResponseTimeMs)

	require.Len(t, turns.written, 1)
	assert.Equal(t, "what does this do?", turns.written[0].Question)
	assert.Equal(t, "the answer", turns.written[0].Answer)
	assert.False(t, turns.written[0].IsError)
	require.NotNil(t, turns.written[0].Provider)
	assert.Equal(t, "ollama", *turns.written[0].Provider)
}

// A conversation that silently skipped its failures would leave the user wondering what they had
// asked, so a genuine failure is recorded as a failed turn carrying the engine's own text.
func TestAFailedTurnIsStillRecorded(t *testing.T) {
	rows := settings{"ai_provider": "ollama", "ollama_binary_path": "http://127.0.0.1:1"}
	operations := ai.NewOperations(ai.NewRouter(rows), nil, nil, nil)
	turns := &recordingTurns{}

	_, err := operations.Chat(t.Context(), turns, nil, chatRequest("a question"))

	require.Error(t, err)
	require.Len(t, turns.written, 1)
	assert.True(t, turns.written[0].IsError)
	assert.NotEmpty(t, turns.written[0].Answer, "the engine's own text is the record")
}

// A stopped run has no answer, and a permanent row for something the user did on purpose is an
// artefact they then have to delete.
func TestACancelledTurnIsNeverRecorded(t *testing.T) {
	binary := engineBinary(t)
	script(t, map[string]string{"SCRIPT_STDOUT": "started\n", "SCRIPT_SLEEP_MS": "30000"})

	rows := settings{"ai_provider": "claude", "claude_binary_path": binary}
	registry := ai.NewRunRegistry(nil, 0)
	operations := ai.NewOperations(ai.NewRouter(rows), registry, nil, nil)
	turns := &recordingTurns{}

	request := chatRequest("a question")
	request.RunID = "run-chat"
	request.WorkingDir = t.TempDir()

	done := make(chan error, 1)
	go func() {
		_, err := operations.Chat(t.Context(), turns, nil, request)
		done <- err
	}()

	require.Eventually(t, func() bool { return registry.Cancel("run-chat") },
		10*time.Second, 20*time.Millisecond)

	err := <-done
	require.Error(t, err)
	assert.Equal(t, "RUN_CANCELLED::", err.Error())
	assert.Empty(t, turns.written, "nothing was written for a run the user stopped")
}

// A chat turn can edit the tree, so it is snapshotted first — and the snapshot dropped afterwards
// when the run changed nothing, rather than kept as clutter in the undo list.
func TestAChatTurnIsProtectedByACheckpoint(t *testing.T) {
	operations, _ := ollamaOperations(t, "answered")
	checkpoints := &recordingCheckpoints{}

	_, err := operations.Chat(t.Context(), &recordingTurns{}, checkpoints, chatRequest("edit this"))

	require.NoError(t, err)
	assert.Equal(t, []string{"chat"}, checkpoints.created, "the stable action key the modal shows")
	assert.Len(t, checkpoints.removed, 1, "a snapshot of an unchanged tree is clutter")
}

// A failed snapshot costs the undo button and must never block what the user asked for.
func TestAFailedCheckpointDoesNotStopTheTurn(t *testing.T) {
	operations, _ := ollamaOperations(t, "answered anyway")
	checkpoints := &recordingCheckpoints{createErr: errors.New("not a repository")}

	reply, err := operations.Chat(t.Context(), &recordingTurns{}, checkpoints, chatRequest("a question"))

	require.NoError(t, err)
	assert.Equal(t, "answered anyway", reply.Text)
	assert.Empty(t, checkpoints.removed, "nothing was taken, so nothing is dropped")
}

// An engine's session id means nothing to another engine: passing one along produces either a hard
// failure or, worse, a resumed conversation that is not the user's.
func TestASessionIsDroppedWhenTheProviderChanged(t *testing.T) {
	operations, received := ollamaOperations(t, "answered")
	claude := "claude"
	turns := &recordingTurns{previous: &claude}

	request := chatRequest("continue")
	request.SessionID = "sess-from-claude"

	_, err := operations.Chat(t.Context(), turns, nil, request)

	require.NoError(t, err)
	assert.NotContains(t, *received, "sess-from-claude")
}

// Two turns on the same provider keep the token: each engine resumes a specific id.
func TestASessionSurvivesOnTheSameProvider(t *testing.T) {
	operations, _ := ollamaOperations(t, "answered")
	ollama := "ollama"
	turns := &recordingTurns{previous: &ollama}

	request := chatRequest("continue")
	request.SessionID = "ollama-1234"

	reply, err := operations.Chat(t.Context(), turns, nil, request)

	require.NoError(t, err)
	require.NotNil(t, reply.SessionID)
	assert.Equal(t, "ollama-1234", *reply.SessionID, "Ollama reuses the caller's own id")
}

// An absent lookup keeps the token: guessing it stale would cost continuity on every conversation
// older than the provider column.
func TestAnUnknownPreviousProviderKeepsTheSession(t *testing.T) {
	operations, _ := ollamaOperations(t, "answered")

	request := chatRequest("continue")
	request.SessionID = "ollama-9999"

	reply, err := operations.Chat(t.Context(), &recordingTurns{}, nil, request)

	require.NoError(t, err)
	require.NotNil(t, reply.SessionID)
	assert.Equal(t, "ollama-9999", *reply.SessionID)
}

// The user picked an engine for this one turn, not a new default.
func TestAnAgentOverrideRunsTheTurn(t *testing.T) {
	operations, received := ollamaOperations(t, "answered as the agent")
	turns := &recordingTurns{}

	request := chatRequest("do the thing")
	request.Agent = &ai.AgentOverride{Provider: "ollama", Model: "llama3.2", Prompt: "you are an architect"}

	reply, err := operations.Chat(t.Context(), turns, nil, request)

	require.NoError(t, err)
	assert.Equal(t, "ollama", reply.Provider)
	assert.Contains(t, *received, "you are an architect", "the agent's own prompt runs the turn")
}

// A turn with no conversation still belongs somewhere: losing it because the caller forgot an id
// is worse than a stray group.
func TestAMissingConversationIdGetsAThrowaway(t *testing.T) {
	operations, _ := ollamaOperations(t, "answered")
	turns := &recordingTurns{}

	request := chatRequest("a question")
	request.ConversationID = ""

	_, err := operations.Chat(t.Context(), turns, nil, request)

	require.NoError(t, err)
	require.Len(t, turns.written, 1)
	assert.Regexp(t, `^conv-[0-9a-f-]{36}$`, turns.written[0].SessionID)
}

// Losing the reply because the log could not be updated would be the worse of the two failures.
func TestAFailedHistoryWriteStillReturnsTheAnswer(t *testing.T) {
	operations, _ := ollamaOperations(t, "the answer")
	turns := &recordingTurns{err: errors.New("database is locked")}

	reply, err := operations.Chat(t.Context(), turns, nil, chatRequest("a question"))

	require.NoError(t, err)
	assert.Equal(t, "the answer", reply.Text)
	assert.NotEmpty(t, reply.CreatedAt, "stamped with the current time instead")
}

// ---- pull request descriptions ------------------------------------------------------------------

func TestGeneratePRDescriptionSplitsTitleFromBody(t *testing.T) {
	operations, received := ollamaOperations(t, "TITLE: Add the widget\n\nThis adds the widget.\n\n- one\n- two")

	draft, err := operations.GeneratePRDescription(t.Context(), "", "feat/x", "main", "a real diff")

	require.NoError(t, err)
	assert.Equal(t, "Add the widget", draft.Title)
	assert.Equal(t, "This adds the widget.\n\n- one\n- two", draft.Body)
	assert.Contains(t, *received, "RAMA ORIGEN: feat/x")
	assert.Contains(t, *received, "RAMA DESTINO: main")
}

// A wrong title is harder to notice than a missing one, because it looks deliberate.
func TestAnAnswerWithNoTitleLineBecomesAllBody(t *testing.T) {
	operations, _ := ollamaOperations(t, "Just a description with no title prefix.")

	draft, err := operations.GeneratePRDescription(t.Context(), "", "feat/x", "main", "a diff")

	require.NoError(t, err)
	assert.Empty(t, draft.Title)
	assert.Equal(t, "Just a description with no title prefix.", draft.Body)
}

func TestGeneratePRDescriptionRefusesAnEmptyDiff(t *testing.T) {
	operations, _ := ollamaOperations(t, "unused")

	_, err := operations.GeneratePRDescription(t.Context(), "", "feat/x", "main", "  \n ")

	// VERBATIM, Spanish.
	assert.EqualError(t, err, "No hay diferencias entre las ramas para describir")
}
