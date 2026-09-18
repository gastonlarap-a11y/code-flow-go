package ai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/google/uuid"
)

// A chat turn (AI-049, AI-050, AI-052).
//
// It is the only operation that persists, resumes and protects at once, and each of those three
// has a rule worth stating on its own.

// ChatReply is what one turn answers.
//
// provider and model are recorded **as they were when the turn ran**, not as they are configured
// now: the two differ the moment routing changes mid-conversation, and a reopened conversation
// relabelled with today's settings would misattribute every past answer.
type ChatReply struct {
	Text      string  `json:"text"`
	SessionID *string `json:"session_id"`
	Model     *string `json:"model"`
	Provider  string  `json:"provider"`
	// EngineVersion is null for the HTTP engines, which have none to read.
	EngineVersion *string `json:"engine_version"`
	// CreatedAt comes from the persisted row, so the live turn and the same turn reopened tomorrow
	// name the same instant.
	CreatedAt      string `json:"created_at"`
	ResponseTimeMs int64  `json:"response_time_ms"`
}

// AgentOverride runs one turn as a configured agent rather than the project's default routing.
type AgentOverride struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Prompt   string `json:"prompt"`
}

// ChatRequest is one turn's inputs.
type ChatRequest struct {
	ProjectID  string
	WorkingDir string
	Message    string
	// SessionID is the engine's resume token from the previous turn, if any.
	SessionID string
	// ConversationID is the app's own identity for the chat, and what groups turns in the history.
	ConversationID string
	RunID          string
	Agent          *AgentOverride
}

// TurnRecorder persists a finished turn. Declared at the consumer: this package needs to write one
// row and knows nothing else about the activity log.
type TurnRecorder interface {
	RecordTurn(ctx context.Context, turn NewTurn) (string, error)
	// LastTurnProvider answers which engine answered the previous turn of a conversation.
	LastTurnProvider(ctx context.Context, projectID, conversationID string) (*string, error)
}

// NewTurn is the row to write. It mirrors the activity store's own shape without importing it.
type NewTurn struct {
	ProjectID       string
	SessionID       string
	EngineSessionID *string
	Question        string
	Answer          string
	Trace           *string
	ResponseTimeMs  *int64
	IsError         bool
	Provider        *string
	Model           *string
	EngineVersion   *string
}

// Checkpointer protects a working tree before a run that can write to it.
type Checkpointer interface {
	CreateCheckpoint(ctx context.Context, repo, kind string) (string, error)
	RemoveCheckpointIfUnchanged(ctx context.Context, repo, checkpointID string) (bool, error)
}

// Chat runs one turn: protect, resolve, run, record.
func (o Operations) Chat(
	ctx context.Context,
	recorder TurnRecorder,
	checkpointer Checkpointer,
	request ChatRequest,
) (ChatReply, error) {
	conversationID := request.ConversationID
	if conversationID == "" {
		// A throwaway rather than a dropped row: a turn with no conversation still belongs
		// somewhere, and losing it because the caller forgot an id is worse than a stray group.
		conversationID = "conv-" + uuid.NewString()
	}

	config := o.chatConfig(ctx, request.Agent)

	// A chat turn can write — the engine has tools and a working directory — so the tree is
	// snapshotted first. Best-effort: a failed snapshot costs the undo button and must never block
	// what the user asked for (AI-052).
	checkpointID := ""
	if checkpointer != nil && request.WorkingDir != "" {
		if id, err := checkpointer.CreateCheckpoint(ctx, request.WorkingDir, "chat"); err == nil {
			checkpointID = id
		}
	}

	sessionID := o.sessionFor(ctx, recorder, request, config.Provider)

	started := time.Now()
	result, runErr := o.invokeWith(ctx, request.RunID, config, Invocation{
		SystemPrompt:     o.chatSystemPrompt(request.Agent),
		Prompt:           request.Message,
		WorkingDir:       request.WorkingDir,
		SessionID:        sessionID,
		AutoApproveEdits: true,
		// Not read-only: a chat turn can edit the tree, and repeating it would apply those edits
		// twice.
		ReadOnly: false,
	}, true)
	elapsed := time.Since(started).Milliseconds()

	// A checkpoint whose run left the tree untouched is clutter in the undo list, so it is dropped
	// — including after a failure, so a half-applied change from a killed run stays undoable.
	if checkpointID != "" && checkpointer != nil {
		_, _ = checkpointer.RemoveCheckpointIfUnchanged(ctx, request.WorkingDir, checkpointID)
	}

	// **A cancelled turn is never recorded.** A stopped run has no answer, and a permanent row for
	// something the user did on purpose is an artefact they then have to delete.
	if runErr != nil && strings.HasPrefix(runErr.Error(), sentinel.RunCancelled) {
		return ChatReply{}, runErr
	}

	version := EngineVersion(ctx, config.Provider, config.Binary)
	reply := ChatReply{
		Provider:       config.Provider,
		EngineVersion:  optional(version),
		ResponseTimeMs: elapsed,
	}

	turn := NewTurn{
		ProjectID:      request.ProjectID,
		SessionID:      conversationID,
		Question:       request.Message,
		Trace:          traceJSON(result.Trace),
		ResponseTimeMs: &elapsed,
		Provider:       &config.Provider,
		EngineVersion:  optional(version),
	}

	if runErr != nil {
		// Recorded as a failed turn, with the engine's own text as the answer: a conversation that
		// silently skipped its failures would leave the user wondering what they had asked.
		turn.Answer, turn.IsError = runErr.Error(), true
	} else {
		turn.Answer = result.Text
		turn.EngineSessionID = optional(result.SessionID)
		// The model the engine reported, not the one configured: they differ when a run fans out,
		// and null means "it did not say" rather than a guess.
		turn.Model = optional(config.Model)
		reply.Text = result.Text
		reply.SessionID = optional(result.SessionID)
		reply.Model = turn.Model
	}

	if recorder != nil {
		createdAt, err := recorder.RecordTurn(ctx, turn)
		if err == nil {
			reply.CreatedAt = createdAt
		}
	}
	if reply.CreatedAt == "" {
		// A failed history write still returns the answer: losing the reply because the log could
		// not be updated would be the worse of the two failures.
		reply.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return reply, runErr
}

// chatConfig resolves the routing, honouring an agent override.
func (o Operations) chatConfig(ctx context.Context, agent *AgentOverride) Config {
	if agent != nil && strings.TrimSpace(agent.Provider) != "" && strings.TrimSpace(agent.Model) != "" {
		return o.router.ResolveWith(ctx, agent.Provider, agent.Model, TaskChat)
	}
	return o.router.Resolve(ctx, TaskChat)
}

// chatSystemPrompt is the agent's own prompt when there is one, else the built-in.
func (o Operations) chatSystemPrompt(agent *AgentOverride) string {
	if agent != nil && strings.TrimSpace(agent.Prompt) != "" {
		return agent.Prompt
	}
	return Prompt(PromptChatSystem)
}

// sessionFor decides whether the previous turn's resume token still applies (AI-049).
//
// It is dropped when the previous turn ran on a **different provider**: an engine's session id
// means nothing to another engine, and passing one along produces either a hard failure or, worse,
// a resumed conversation that is not the user's. The model picker already refuses to switch
// provider mid-chat; this is the backstop for routing changed in Settings or a reopened old
// conversation.
func (o Operations) sessionFor(ctx context.Context, recorder TurnRecorder, request ChatRequest, provider string) string {
	if request.SessionID == "" || request.ConversationID == "" || recorder == nil {
		return request.SessionID
	}

	previous, err := recorder.LastTurnProvider(ctx, request.ProjectID, request.ConversationID)
	if err != nil || previous == nil {
		// An absent or failed lookup keeps the token: guessing it stale would cost continuity on
		// every conversation older than the provider column.
		return request.SessionID
	}
	if *previous != provider {
		return ""
	}
	return request.SessionID
}

// traceJSON encodes the activity log for storage, or nil when there is nothing to keep.
func traceJSON(trace []string) *string {
	if len(trace) == 0 {
		return nil
	}
	encoded, err := json.Marshal(trace)
	if err != nil {
		return nil
	}
	text := string(encoded)
	return &text
}

// optional turns an empty string into nil, which is how every nullable column and every renderer
// field of type `T | null` expects to see "nothing".
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
