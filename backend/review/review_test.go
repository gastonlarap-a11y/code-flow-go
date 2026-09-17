package review_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/review"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var clock = storage.FixedClock{At: time.Date(2026, 9, 17, 14, 30, 5, 0, time.UTC)}

func newStore(t *testing.T) (*review.Store, *storage.DB) {
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
			 VALUES ('p1', 'w1', 'code-flow', '/tmp/repo', 'now')`)
		return err
	}))
	return review.NewStore(db, clock), db
}

// A finding carries fields this port has not modelled yet. They are written in full here so the
// "must not be dropped" assertion below means something.
const findingsJSON = `[
  {"id":"F-001","tipo":"bug","categoria":"correctness","archivo":"src/a.go","lineas":"10-12",
   "confianza":"alta","estado":"abierto","subtitulo":"off-by-one","introducido_en_iter":1,
   "resuelto_en_iter":null,"motivo_descarte":null,"thread_id":null,"delta":"nuevo"},
  {"id":"F-002","tipo":"estilo","categoria":"naming","archivo":"src/b.go","lineas":"3",
   "confianza":"media","estado":"posteado","subtitulo":"unclear name","introducido_en_iter":1,
   "resuelto_en_iter":null,"motivo_descarte":null,"thread_id":"t-9","delta":null}
]`

func insertRun(t *testing.T, db *storage.DB, id string, prID int64, iter int64, createdAt, meta, findings string) {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO review_runs (id, project_id, workspace_id, pr_id, iter, level, meta, review_md, diff, findings, created_at)
			 VALUES (?, 'p1', 'w1', ?, ?, 'completo', ?, '# Review', 'diff --git a b', ?, ?)`,
			id, prID, iter, meta, findings, createdAt)
		return err
	}))
}

func at(minute int) string {
	return storage.FixedClock{At: time.Date(2026, 9, 17, 14, minute, 5, 0, time.UTC)}.Now()
}

// The summary is a projection: project_name comes from a join, pr_title out of the meta JSON and
// findings_count out of the findings array. None of the three is a stored column.
func TestSummaryIsProjectedFromTheJoinAndTheJSON(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 2, at(1), `{"pr_title":"Add the thing"}`, findingsJSON)

	runs, err := store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)

	require.Len(t, runs, 1)
	assert.Equal(t, "code-flow", runs[0].ProjectName)
	assert.Equal(t, "Add the thing", runs[0].PRTitle)
	assert.EqualValues(t, 2, runs[0].FindingsCount)
	assert.EqualValues(t, 42, runs[0].PRID)
}

// Deleting a project takes its review history with it: review_runs.project_id carries
// ON DELETE CASCADE. The denormalised workspace_id does not keep the row alive — it is there so a
// run can be listed and purged per workspace without a join, not to outlive the project.
//
// Worth pinning because it is destructive and easy to get wrong in the other direction: a port
// that made project_id nullable "to preserve history" would quietly change what removing a
// project means.
func TestDeletingAProjectTakesItsReviewRuns(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{"pr_title":"Add the thing"}`, findingsJSON)

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = 'p1'`)
		return err
	}))

	runs, err := store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)
	assert.Empty(t, runs)
}

func TestMetaWithoutAPRTitleDoesNotBecomeNull(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, `[]`)

	runs, err := store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)
	assert.Equal(t, "", runs[0].PRTitle)
	assert.EqualValues(t, 0, runs[0].FindingsCount)
}

func TestRunsComeBackNewestFirst(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "older", 1, 1, at(1), `{}`, `[]`)
	insertRun(t, db, "newer", 2, 1, at(2), `{}`, `[]`)

	runs, err := store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)

	require.Len(t, runs, 2)
	assert.Equal(t, "newer", runs[0].ID)
}

// meta and findings stay JSON strings: the renderer parses them itself, and re-encoding here would
// risk reordering keys in a blob other code matches against.
func TestDetailKeepsMetaAndFindingsAsStrings(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{"pr_title":"x"}`, findingsJSON)

	detail, err := store.GetRun(t.Context(), "r1")
	require.NoError(t, err)

	assert.JSONEq(t, `{"pr_title":"x"}`, detail.Meta)
	assert.JSONEq(t, findingsJSON, detail.Findings)
	assert.Equal(t, "# Review", detail.ReviewMD)
}

// ---- marking ----------------------------------------------------------------------------------

func findingByID(t *testing.T, encoded, id string) map[string]any {
	t.Helper()
	var findings []map[string]any
	require.NoError(t, json.Unmarshal([]byte(encoded), &findings))
	for _, f := range findings {
		if f["id"] == id {
			return f
		}
	}
	t.Fatalf("finding %s not found", id)
	return nil
}

func TestMarkingAFindingSetsItsStateAndReason(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, findingsJSON)
	motivo := "the linter already covers this"

	require.NoError(t, store.MarkFinding(t.Context(), "r1", "F-001", review.StateIgnored, &motivo))

	detail, err := store.GetRun(t.Context(), "r1")
	require.NoError(t, err)
	marked := findingByID(t, detail.Findings, "F-001")
	assert.Equal(t, "ignorado", marked["estado"])
	assert.Equal(t, motivo, marked["motivo_descarte"])
}

// A finding carries fields this port has not modelled. Decoding into a typed struct would drop
// every one of them on the way back out — silently, and only for runs somebody marked.
func TestMarkingPreservesFieldsThePortDoesNotModel(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, findingsJSON)

	require.NoError(t, store.MarkFinding(t.Context(), "r1", "F-001", review.StateFalsePositive, nil))

	detail, err := store.GetRun(t.Context(), "r1")
	require.NoError(t, err)

	marked := findingByID(t, detail.Findings, "F-001")
	for _, field := range []string{"tipo", "categoria", "archivo", "lineas", "confianza",
		"subtitulo", "introducido_en_iter", "resuelto_en_iter", "thread_id", "delta"} {
		assert.Contains(t, marked, field, "field %q was dropped", field)
	}
	assert.Equal(t, "off-by-one", marked["subtitulo"])
	assert.EqualValues(t, 1, marked["introducido_en_iter"])

	// And the finding that was not named must be untouched, thread id included.
	untouched := findingByID(t, detail.Findings, "F-002")
	assert.Equal(t, "posteado", untouched["estado"])
	assert.Equal(t, "t-9", untouched["thread_id"])
}

// Re-opening clears the reason with it, or a finding would keep explaining why it was discarded.
func TestClearingTheMarkAlsoClearsTheReason(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, findingsJSON)
	motivo := "not worth it"
	require.NoError(t, store.MarkFinding(t.Context(), "r1", "F-001", review.StateIgnored, &motivo))

	require.NoError(t, store.MarkFinding(t.Context(), "r1", "F-001", review.StateOpen, nil))

	detail, err := store.GetRun(t.Context(), "r1")
	require.NoError(t, err)
	reopened := findingByID(t, detail.Findings, "F-001")
	assert.Equal(t, "abierto", reopened["estado"])
	assert.Nil(t, reopened["motivo_descarte"])
}

// Only the human judgements are reachable from a command; the pipeline owns the other two.
func TestOnlyTheHumanStatesAreAccepted(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, findingsJSON)

	for _, estado := range []string{review.StateResolved, review.StatePosted, "nonsense", ""} {
		err := store.MarkFinding(t.Context(), "r1", "F-001", estado, nil)
		require.Error(t, err, estado)
		assert.Contains(t, err.Error(), "estado must be")
	}
}

func TestMarkingSomethingThatIsNotThereSaysWhich(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, findingsJSON)

	err := store.MarkFinding(t.Context(), "r1", "F-404", review.StateIgnored, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding F-404 is not part of run r1")

	assert.ErrorIs(t, store.MarkFinding(t.Context(), "nope", "F-001", review.StateIgnored, nil), review.ErrNotFound)
}

// ---- deleting ----------------------------------------------------------------------------------

func TestDeletingScopes(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "pr1-a", 1, 1, at(1), `{}`, `[]`)
	insertRun(t, db, "pr1-b", 1, 2, at(2), `{}`, `[]`)
	insertRun(t, db, "pr2", 2, 1, at(3), `{}`, `[]`)

	require.NoError(t, store.DeleteRun(t.Context(), "pr1-a"))
	runs, err := store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)
	assert.Len(t, runs, 2)

	// "Start this PR's memory again": the next review then treats every finding as new.
	require.NoError(t, store.DeleteRunsForPR(t.Context(), "p1", 1))
	runs, err = store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "pr2", runs[0].ID)

	require.NoError(t, store.PurgeWorkspace(t.Context(), "w1"))
	runs, err = store.ListRuns(t.Context(), "w1")
	require.NoError(t, err)
	assert.Empty(t, runs)
}

// ---- exporting ----------------------------------------------------------------------------------

func TestExportWritesAFolderPerRun(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, `[]`)
	insertRun(t, db, "r2", 43, 1, at(2), `{}`, `[]`)
	dest := filepath.Join(t.TempDir(), "export")

	count, err := store.ExportRuns(t.Context(), "w1", nil, dest)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// The review alone names line numbers in code the reader no longer has, so the diff travels
	// with it.
	for _, entry := range entries {
		assert.FileExists(t, filepath.Join(dest, entry.Name(), "REVIEW.md"))
		assert.FileExists(t, filepath.Join(dest, entry.Name(), "changes.diff"))
		assert.Regexp(t, `^PR-4[23]_2026-09-17_14-\d\d-05$`, entry.Name())
		assert.NotContains(t, entry.Name(), ":", "a colon is illegal in a Windows path")
	}
}

func TestExportCanSelectASingleRun(t *testing.T) {
	store, db := newStore(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{}`, `[]`)
	insertRun(t, db, "r2", 43, 1, at(2), `{}`, `[]`)
	dest := filepath.Join(t.TempDir(), "export")
	only := "r2"

	count, err := store.ExportRuns(t.Context(), "w1", &only, dest)
	require.NoError(t, err)

	assert.Equal(t, 1, count)
	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Name(), "PR-43")
}

// ---- the wire ------------------------------------------------------------------------------------

func newService(t *testing.T) (*bridge.Service, *storage.DB) {
	t.Helper()
	store, db := newStore(t)
	r := bridge.NewRegistry()
	review.RegisterStore(r, review.Deps{Store: store})
	r.Seal()
	return bridge.NewService(r, nil), db
}

func TestEmptyListIsAnArrayAndAMissingRunIsNull(t *testing.T) {
	svc, _ := newService(t)

	out, err := svc.Invoke(t.Context(), "list_review_runs", json.RawMessage(`{"workspaceId":"w1"}`))
	require.NoError(t, err)
	assert.Equal(t, "[]", string(out))

	out, err = svc.Invoke(t.Context(), "get_review_run", json.RawMessage(`{"id":"nope"}`))
	require.NoError(t, err)
	assert.Equal(t, "null", string(out))
}

func TestSummaryFieldNamesMatchTheRenderer(t *testing.T) {
	svc, db := newService(t)
	insertRun(t, db, "r1", 42, 1, at(1), `{"pr_title":"x"}`, findingsJSON)

	out, err := svc.Invoke(t.Context(), "list_review_runs", json.RawMessage(`{"workspaceId":"w1"}`))
	require.NoError(t, err)

	var runs []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &runs))
	require.Len(t, runs, 1)
	for _, name := range []string{"id", "project_id", "project_name", "pr_id", "pr_title",
		"iter", "level", "findings_count", "created_at"} {
		assert.Contains(t, runs[0], name)
	}
	assert.Len(t, runs[0], 9)
}

func TestCommandsReportAMissingParameterByName(t *testing.T) {
	svc, _ := newService(t)

	for _, tc := range []struct{ method, params, missing string }{
		{"list_review_runs", `{}`, "workspaceId"},
		{"get_review_run", `{}`, "id"},
		{"mark_review_finding", `{"runId":"r1","findingId":"F-001"}`, "estado"},
		{"delete_review_runs_for_pr", `{"projectId":"p1"}`, "prId"},
		{"export_review_runs", `{"workspaceId":"w1"}`, "destDir"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), tc.method, json.RawMessage(tc.params))
			require.Error(t, err)
			assert.Equal(t, "missing required parameter '"+tc.missing+"'", err.Error())
		})
	}
}
