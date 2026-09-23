package activity_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectID = "p1"

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, workspace_id, name, local_path, created_at)
			 VALUES (?, 'w1', 'Repo', '/tmp/repo', 'now')`, projectID)
		return err
	}))
	return db
}

// insertTurn writes one activity_log row. session is a *string so a test can write the
// pre-session-tracking rows that every chat feature deliberately ignores.
func insertTurn(t *testing.T, db *storage.DB, id string, session *string, question, answer, createdAt string) {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO activity_log (id, project_id, session_id, question, answer, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, projectID, storage.NullString(session), question, answer, createdAt)
		return err
	}))
}

func at(minute int) string {
	return storage.FixedClock{At: time.Date(2026, 9, 17, 12, minute, 0, 0, time.UTC)}.Now()
}

func newStore(t *testing.T) (*activity.Store, *storage.DB) {
	t.Helper()
	db := newDB(t)
	return activity.NewStore(db, storage.FixedClock{At: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}), db
}

// A conversation's title is its FIRST turn's question, in insertion order.
func TestConversationTitleIsTheFirstQuestion(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "t1", new("s1"), "how do I start?", "like this", at(1))
	insertTurn(t, db, "t2", new("s1"), "and then?", "like that", at(2))

	list, err := store.ListConversations(t.Context(), projectID, nil)
	require.NoError(t, err)

	require.Len(t, list, 1)
	assert.Equal(t, "how do I start?", list[0].Title)
	assert.Equal(t, at(1), list[0].CreatedAt)
	assert.Equal(t, at(2), list[0].UpdatedAt, "updated_at is the LAST turn")
	assert.EqualValues(t, 2, list[0].TurnCount)
}

// Rows written before session tracking existed are permanently invisible to every chat feature.
// 2.x behaviour, kept: they have no conversation to belong to.
func TestTurnsWithoutASessionAreExcluded(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "old", nil, "before sessions", "answer", at(1))
	insertTurn(t, db, "new", new("s1"), "after sessions", "answer", at(2))

	list, err := store.ListConversations(t.Context(), projectID, nil)
	require.NoError(t, err)

	require.Len(t, list, 1)
	assert.Equal(t, "after sessions", list[0].Title)
}

// Most recently active first, ordered on the stored timestamp strings — which only works because
// the Clock's format sorts lexicographically.
func TestConversationsAreOrderedByLastActivity(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "a1", new("older"), "first conversation", "x", at(1))
	insertTurn(t, db, "b1", new("newer"), "second conversation", "x", at(2))
	insertTurn(t, db, "a2", new("older"), "revived", "x", at(3))

	list, err := store.ListConversations(t.Context(), projectID, nil)
	require.NoError(t, err)

	require.Len(t, list, 2)
	assert.Equal(t, "older", list[0].SessionID, "the revived conversation comes first")
	assert.Equal(t, "newer", list[1].SessionID)
}

// The needle matches any turn's question or answer, case-folded — so a conversation surfaces on
// something said in its middle, not only on its title.
func TestSearchMatchesAnyTurnInEitherDirection(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "a1", new("s1"), "about storage", "some answer", at(1))
	insertTurn(t, db, "a2", new("s1"), "follow-up", "mentions MIGRATIONS here", at(2))
	insertTurn(t, db, "b1", new("s2"), "unrelated", "nothing to see", at(3))

	for name, needle := range map[string]string{
		"matches a later answer":     "migrations",
		"matches the first question": "STORAGE",
	} {
		t.Run(name, func(t *testing.T) {
			list, err := store.ListConversations(t.Context(), projectID, new(needle))
			require.NoError(t, err)
			require.Len(t, list, 1)
			assert.Equal(t, "s1", list[0].SessionID)
		})
	}

	none, err := store.ListConversations(t.Context(), projectID, new("absent"))
	require.NoError(t, err)
	assert.Empty(t, none)
}

// A conversation is a GROUP BY over turns and has no row of its own, which is the entire reason
// conversation_titles exists.
func TestAStoredTitleOverridesTheDerivedOne(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "t1", new("s1"), "how do I start?", "like this", at(1))

	require.NoError(t, store.RenameConversation(t.Context(), projectID, "s1", "Getting started"))

	list, err := store.ListConversations(t.Context(), projectID, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Getting started", list[0].Title)
}

// Oldest first, so the renderer can flatten turns straight into [user, assistant, user, …].
func TestConversationTurnsComeBackOldestFirst(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "t2", new("s1"), "second", "b", at(2))
	insertTurn(t, db, "t1", new("s1"), "first", "a", at(1))

	turns, err := store.GetConversation(t.Context(), projectID, "s1")
	require.NoError(t, err)

	require.Len(t, turns, 2)
	assert.Equal(t, "first", turns[0].Question)
	assert.Equal(t, "second", turns[1].Question)
}

func TestDeletingAConversationRemovesItsTurnsAndItsTitle(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "t1", new("s1"), "q", "a", at(1))
	require.NoError(t, store.RenameConversation(t.Context(), projectID, "s1", "Named"))

	require.NoError(t, store.DeleteConversation(t.Context(), projectID, "s1"))

	list, err := store.ListConversations(t.Context(), projectID, nil)
	require.NoError(t, err)
	assert.Empty(t, list)

	// The title must go too, or a new conversation reusing the id would inherit it.
	insertTurn(t, db, "t2", new("s1"), "a fresh question", "a", at(5))
	list, err = store.ListConversations(t.Context(), projectID, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "a fresh question", list[0].Title)
}

// A conversation whose every turn predates the provider column returns nil, indistinguishable
// from one that does not exist. 2.x's ambiguity, kept — the caller's fallback is the same.
func TestLastTurnProviderIgnoresRowsFromBeforeTheColumn(t *testing.T) {
	store, db := newStore(t)
	insertTurn(t, db, "t1", new("s1"), "q", "a", at(1))

	provider, err := store.LastTurnProvider(t.Context(), projectID, "s1")
	require.NoError(t, err)
	assert.Nil(t, provider)

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO activity_log (id, project_id, session_id, question, answer, created_at, provider)
			 VALUES ('t2', ?, 's1', 'q', 'a', ?, 'claude')`, projectID, at(2))
		return err
	}))

	provider, err = store.LastTurnProvider(t.Context(), projectID, "s1")
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, "claude", *provider)
}

// ---- job history ------------------------------------------------------------------------------

func insertJob(t *testing.T, db *storage.DB, id, label, createdAt string) {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO job_history (id, project_id, kind, label, status, created_at)
			 VALUES (?, ?, 'review', ?, 'ok', ?)`, id, projectID, label, createdAt)
		return err
	}))
}

func TestJobsComeBackNewestFirst(t *testing.T) {
	store, db := newStore(t)
	insertJob(t, db, "j1", "older", at(1))
	insertJob(t, db, "j2", "newer", at(2))

	jobs, err := store.ListJobs(t.Context(), projectID)
	require.NoError(t, err)

	require.Len(t, jobs, 2)
	assert.Equal(t, "newer", jobs[0].Label)
}

// An empty label clears the override back to NULL, so the derived label shows again rather than
// an empty row.
func TestRenamingAJobWithAnEmptyLabelClearsTheOverride(t *testing.T) {
	store, db := newStore(t)
	insertJob(t, db, "j1", "PR #12 review", at(1))

	require.NoError(t, store.RenameJob(t.Context(), "j1", "The important one"))
	jobs, err := store.ListJobs(t.Context(), projectID)
	require.NoError(t, err)
	require.NotNil(t, jobs[0].CustomLabel)
	assert.Equal(t, "The important one", *jobs[0].CustomLabel)

	require.NoError(t, store.RenameJob(t.Context(), "j1", "   "))
	jobs, err = store.ListJobs(t.Context(), projectID)
	require.NoError(t, err)
	assert.Nil(t, jobs[0].CustomLabel)
}

// ---- the wire ---------------------------------------------------------------------------------

func newService(t *testing.T) (*bridge.Service, *storage.DB) {
	t.Helper()
	store, db := newStore(t)
	r := bridge.NewRegistry()
	activity.Register(r, activity.Deps{Store: store})
	r.Seal()
	return bridge.NewService(r, nil), db
}

func TestEmptyListsAreArraysNotNull(t *testing.T) {
	svc, _ := newService(t)

	for method, params := range map[string]string{
		"list_chat_conversations": fmt.Sprintf(`{"projectId":%q,"search":null}`, projectID),
		"get_chat_conversation":   fmt.Sprintf(`{"projectId":%q,"sessionId":"none"}`, projectID),
		"list_job_history":        fmt.Sprintf(`{"projectId":%q}`, projectID),
	} {
		t.Run(method, func(t *testing.T) {
			out, err := svc.Invoke(t.Context(), method, json.RawMessage(params))
			require.NoError(t, err)
			assert.Equal(t, "[]", string(out))
		})
	}
}

func TestChatTurnFieldNamesMatchTheRenderer(t *testing.T) {
	svc, db := newService(t)
	insertTurn(t, db, "t1", new("s1"), "q", "a", at(1))

	out, err := svc.Invoke(t.Context(), "get_chat_conversation",
		json.RawMessage(fmt.Sprintf(`{"projectId":%q,"sessionId":"s1"}`, projectID)))
	require.NoError(t, err)

	var turns []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &turns))
	require.Len(t, turns, 1)
	for _, name := range []string{
		"id", "project_id", "session_id", "engine_session_id", "question", "answer", "trace",
		"created_at", "response_time_ms", "is_error", "provider", "model", "engine_version",
	} {
		assert.Contains(t, turns[0], name)
	}
	assert.Len(t, turns[0], 13)
	assert.Equal(t, "false", string(turns[0]["is_error"]), "an INTEGER column, a JSON boolean")
}

func TestCommandsReportAMissingParameterByName(t *testing.T) {
	svc, _ := newService(t)

	for _, tc := range []struct{ method, params, missing string }{
		{"list_chat_conversations", `{}`, "projectId"},
		{"get_chat_conversation", `{"projectId":"p1"}`, "sessionId"},
		{"rename_chat_conversation", `{"projectId":"p1","sessionId":"s1"}`, "title"},
		{"list_job_history", `{}`, "projectId"},
		{"rename_job_history_entry", `{"id":"j1"}`, "label"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), tc.method, json.RawMessage(tc.params))
			require.Error(t, err)
			assert.Equal(t, "missing required parameter '"+tc.missing+"'", err.Error())
		})
	}
}
