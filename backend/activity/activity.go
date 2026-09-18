// Package activity owns what the app remembers about work it has already done: every AI chat turn
// and every finished review or analysis run.
//
// Both exist because the renderer's stores are in-memory only. Without these tables a restart
// silently loses every question ever asked and every review ever produced — which is what made
// them worth persisting rather than a nicety.
package activity

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/google/uuid"
)

// newID mints a row id in the lowercase 8-4-4-4-12 form .NET's Guid.ToString() produced, so a row
// written by 3.0 is indistinguishable in shape from one 2.x wrote.
func newID() string { return uuid.NewString() }

// boolToInt is how every boolean column is written: the schema declares INTEGER and the renderer
// types the field as a real boolean.
func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ChatTurn is one question/answer pair. The field names are the renderer's `ActivityLogEntry`.
type ChatTurn struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`

	// The conversation this turn belongs to — app-minted and stable. Distinct from
	// EngineSessionID, which is the CLI's own resume token and changes between runs.
	SessionID       *string `json:"session_id"`
	EngineSessionID *string `json:"engine_session_id"`

	Question string `json:"question"`
	Answer   string `json:"answer"`

	// JSON array of {stream, line} with what the engine printed while working, so a finished
	// answer can still show how it got there. Null for turns recorded before traces existed.
	Trace     *string `json:"trace"`
	CreatedAt string  `json:"created_at"`

	ResponseTimeMs *int64 `json:"response_time_ms"`
	// True when the turn failed, in which case Answer holds the engine's error text.
	IsError bool `json:"is_error"`

	// Recorded at the time the turn ran, so reopening a conversation does not relabel it with
	// today's routing. Null for turns older than each column.
	Provider      *string `json:"provider"`
	Model         *string `json:"model"`
	EngineVersion *string `json:"engine_version"`
}

// Conversation is a group of turns sharing a session id, as the history list shows them.
type Conversation struct {
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	TurnCount int64  `json:"turn_count"`
}

// JobEntry is one finished review or pre-commit analysis.
type JobEntry struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Kind        string  `json:"kind"`
	Label       string  `json:"label"`
	CustomLabel *string `json:"custom_label"`
	Status      string  `json:"status"`
	Result      *string `json:"result"`
	Error       *string `json:"error"`
	Meta        string  `json:"meta"`
	CreatedAt   string  `json:"created_at"`
}

// Store is this feature's queries.
type Store struct {
	db    *storage.DB
	clock storage.Clock
}

// NewStore wires the store.
func NewStore(db *storage.DB, clock storage.Clock) *Store {
	if clock == nil {
		clock = storage.SystemClock{}
	}
	return &Store{db: db, clock: clock}
}

const turnColumns = `id, project_id, session_id, engine_session_id, question, answer, trace,
	created_at, response_time_ms, is_error, provider, model, engine_version`

func scanTurn(scan func(...any) error) (ChatTurn, error) {
	var t ChatTurn
	err := scan(&t.ID, &t.ProjectID, &t.SessionID, &t.EngineSessionID, &t.Question, &t.Answer,
		&t.Trace, &t.CreatedAt, &t.ResponseTimeMs, &t.IsError, &t.Provider, &t.Model, &t.EngineVersion)
	return t, err
}

// ListConversations groups a project's turns into conversations.
//
// Grouping happens in Go rather than in SQL, and that is not laziness: the title is the *first*
// turn's question in insertion order, the search needle has to match any turn's question or
// answer, and a `conversation_titles` override replaces the derived title afterwards. Expressing
// that as one query would be a window function stack nobody could check against the specification.
//
// Rows with a NULL session_id are excluded entirely — they predate session tracking and are
// permanently invisible to every chat-history feature, which is 2.x's behaviour and not a bug to
// fix here.
func (s *Store) ListConversations(ctx context.Context, projectID string, search *string) ([]Conversation, error) {
	out := make([]Conversation, 0, 8)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT `+turnColumns+` FROM activity_log
			  WHERE project_id = ? AND session_id IS NOT NULL
			  ORDER BY created_at, id`, projectID)
		if err != nil {
			return fmt.Errorf("list conversations: %w", err)
		}
		defer func() { _ = rows.Close() }()

		type group struct {
			conversation Conversation
			matched      bool
		}
		order := make([]string, 0, 8)
		groups := make(map[string]*group, 8)

		needle := ""
		if search != nil {
			needle = strings.ToLower(strings.TrimSpace(*search))
		}

		for rows.Next() {
			turn, err := scanTurn(rows.Scan)
			if err != nil {
				return fmt.Errorf("scan turn: %w", err)
			}
			session := *turn.SessionID

			existing, seen := groups[session]
			if !seen {
				existing = &group{conversation: Conversation{
					SessionID: session,
					ProjectID: turn.ProjectID,
					// The first turn in insertion order, which is why the query orders by
					// created_at before this loop ever runs.
					Title:     turn.Question,
					CreatedAt: turn.CreatedAt,
				}}
				groups[session] = existing
				order = append(order, session)
			}

			existing.conversation.TurnCount++
			existing.conversation.UpdatedAt = turn.CreatedAt

			if needle != "" && !existing.matched {
				existing.matched = strings.Contains(strings.ToLower(turn.Question), needle) ||
					strings.Contains(strings.ToLower(turn.Answer), needle)
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list conversations: %w", err)
		}

		titles, err := s.conversationTitles(ctx, db, projectID)
		if err != nil {
			return err
		}

		for _, session := range order {
			g := groups[session]
			if needle != "" && !g.matched {
				continue
			}
			// A stored title overrides the derived one, after grouping.
			if title, renamed := titles[session]; renamed {
				g.conversation.Title = title
			}
			out = append(out, g.conversation)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Most recently active first.
	sortByUpdatedDesc(out)
	return out, nil
}

func (s *Store) conversationTitles(ctx context.Context, db *sql.DB, projectID string) (map[string]string, error) {
	titles := make(map[string]string, 4)
	rows, err := db.QueryContext(ctx,
		`SELECT session_id, title FROM conversation_titles WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("read conversation titles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var session, title string
		if err := rows.Scan(&session, &title); err != nil {
			return nil, fmt.Errorf("scan conversation title: %w", err)
		}
		titles[session] = title
	}
	return titles, rows.Err()
}

// sortByUpdatedDesc orders conversations by their last turn, newest first. Timestamps are the
// stored strings and sort correctly as such — which is exactly why the Clock's format is fixed.
func sortByUpdatedDesc(items []Conversation) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].UpdatedAt > items[j-1].UpdatedAt; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// GetConversation returns a conversation's turns, oldest first, so the renderer can flatten them
// straight into [user, assistant, user, assistant, …].
func (s *Store) GetConversation(ctx context.Context, projectID, sessionID string) ([]ChatTurn, error) {
	out := make([]ChatTurn, 0, 16)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT `+turnColumns+` FROM activity_log
			  WHERE project_id = ? AND session_id = ? ORDER BY created_at, id`, projectID, sessionID)
		if err != nil {
			return fmt.Errorf("get conversation: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			turn, err := scanTurn(rows.Scan)
			if err != nil {
				return fmt.Errorf("scan turn: %w", err)
			}
			out = append(out, turn)
		}
		return rows.Err()
	})
	return out, err
}

// NewTurn is one chat exchange about to be recorded.
type NewTurn struct {
	ProjectID string
	// SessionID is the app's own conversation id, stable across turns. EngineSessionID is the
	// CLI's resume token, which changes between runs and may be absent entirely.
	SessionID       string
	EngineSessionID *string

	Question string
	Answer   string
	// Trace is the JSON array of activity lines, or nil for a turn that kept none.
	Trace *string

	ResponseTimeMs *int64
	IsError        bool

	// Recorded as they were **at the time of the run**, so reopening a conversation does not
	// relabel its turns with today's routing.
	Provider      *string
	Model         *string
	EngineVersion *string
}

// RecordTurn writes one chat exchange and answers the row as stored (AI-050).
//
// It returns the row rather than nothing because the caller shows the user a timestamp, and the
// stored one is what a reopened conversation will show — taking a second reading from the clock
// would make the live turn and the same turn tomorrow disagree by milliseconds.
//
// **A cancelled turn is never recorded**, and that decision belongs to the caller: a stopped run
// has no answer, and a permanent artefact for something the user did on purpose is clutter they
// then have to delete.
func (s *Store) RecordTurn(ctx context.Context, turn NewTurn) (ChatTurn, error) {
	stored := ChatTurn{
		ID:              newID(),
		ProjectID:       turn.ProjectID,
		SessionID:       &turn.SessionID,
		EngineSessionID: turn.EngineSessionID,
		Question:        turn.Question,
		Answer:          turn.Answer,
		Trace:           turn.Trace,
		CreatedAt:       s.clock.Now(),
		ResponseTimeMs:  turn.ResponseTimeMs,
		IsError:         turn.IsError,
		Provider:        turn.Provider,
		Model:           turn.Model,
		EngineVersion:   turn.EngineVersion,
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO activity_log (`+turnColumns+`)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stored.ID, stored.ProjectID, storage.NullString(stored.SessionID),
			storage.NullString(stored.EngineSessionID), stored.Question, stored.Answer,
			storage.NullString(stored.Trace), stored.CreatedAt,
			storage.NullInt64(stored.ResponseTimeMs), boolToInt(stored.IsError),
			storage.NullString(stored.Provider), storage.NullString(stored.Model),
			storage.NullString(stored.EngineVersion))
		if err != nil {
			return fmt.Errorf("record chat turn: %w", err)
		}
		return nil
	})
	return stored, err
}

// DeleteConversation removes every turn of a conversation and its stored title.
func (s *Store) DeleteConversation(ctx context.Context, projectID, sessionID string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM activity_log WHERE project_id = ? AND session_id = ?`, projectID, sessionID); err != nil {
			return fmt.Errorf("delete conversation turns: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM conversation_titles WHERE project_id = ? AND session_id = ?`, projectID, sessionID); err != nil {
			return fmt.Errorf("delete conversation title: %w", err)
		}
		return nil
	})
}

// RenameConversation stores a title override.
//
// A conversation has no row of its own — it is a GROUP BY over turns — which is exactly why this
// table exists: there is nowhere else to attach a name to.
func (s *Store) RenameConversation(ctx context.Context, projectID, sessionID, title string) error {
	now := s.clock.Now()
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO conversation_titles (session_id, project_id, title, updated_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(session_id) DO UPDATE SET title = excluded.title, updated_at = excluded.updated_at`,
			sessionID, projectID, title, now)
		if err != nil {
			return fmt.Errorf("rename conversation: %w", err)
		}
		return nil
	})
}

// LastTurnProvider is which engine answered a conversation most recently.
//
// Rows predating the provider column are filtered out, so a conversation whose every turn is older
// returns nil — indistinguishable from one that does not exist. That ambiguity is 2.x's and is
// kept: the caller's fallback is the same either way.
func (s *Store) LastTurnProvider(ctx context.Context, projectID, sessionID string) (*string, error) {
	var provider *string
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		var found string
		err := db.QueryRowContext(ctx,
			`SELECT provider FROM activity_log
			  WHERE project_id = ? AND session_id = ? AND provider IS NOT NULL
			  ORDER BY created_at DESC LIMIT 1`, projectID, sessionID).Scan(&found)
		if err == sql.ErrNoRows { //nolint:errorlint // database/sql returns this sentinel unwrapped
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the last turn's provider: %w", err)
		}
		provider = &found
		return nil
	})
	return provider, err
}

// ---- job history ------------------------------------------------------------------------------

const jobColumns = `id, project_id, kind, label, custom_label, status, result, error, meta, created_at`

// ListJobs returns a project's finished runs, newest first.
func (s *Store) ListJobs(ctx context.Context, projectID string) ([]JobEntry, error) {
	out := make([]JobEntry, 0, 16)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT `+jobColumns+` FROM job_history WHERE project_id = ? ORDER BY created_at DESC`, projectID)
		if err != nil {
			return fmt.Errorf("list job history: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var j JobEntry
			if err := rows.Scan(&j.ID, &j.ProjectID, &j.Kind, &j.Label, &j.CustomLabel,
				&j.Status, &j.Result, &j.Error, &j.Meta, &j.CreatedAt); err != nil {
				return fmt.Errorf("scan job entry: %w", err)
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, err
}

// RenameJob stores a user-given label. An empty one clears the override back to NULL, so the
// derived label shows again.
func (s *Store) RenameJob(ctx context.Context, id, label string) error {
	var stored any
	if strings.TrimSpace(label) != "" {
		stored = label
	}
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE job_history SET custom_label = ? WHERE id = ?`, stored, id)
		if err != nil {
			return fmt.Errorf("rename job entry: %w", err)
		}
		return nil
	})
}

// DeleteJob removes one entry.
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM job_history WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete job entry: %w", err)
		}
		return nil
	})
}
