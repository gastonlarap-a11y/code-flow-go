package storage_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func open(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// objects lists what the live schema actually contains, which is the only thing worth asserting
// about a migration: not that a step ran, but that the result is right.
func objects(t *testing.T, db *storage.DB, kind string) []string {
	t.Helper()
	var names []string
	require.NoError(t, db.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		rows, err := sqlDB.QueryContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = ? AND sql IS NOT NULL
			   AND name NOT LIKE 'sqlite_%' ORDER BY name`, kind)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			names = append(names, name)
		}
		return rows.Err()
	}))
	return names
}

func columns(t *testing.T, db *storage.DB, table string) []string {
	t.Helper()
	var names []string
	require.NoError(t, db.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		rows, err := sqlDB.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var (
				cid        int
				name, kind string
				notNull    int
				dflt       sql.NullString
				pk         int
			)
			if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
				return err
			}
			names = append(names, name)
		}
		return rows.Err()
	}))
	sort.Strings(names)
	return names
}

// The count measured against a real 2.7.1 database (23 tables, 10 indexes). `03-storage.md` says
// 22/10, which is what this number corrects — the two DBML tables live in Schema.cs but were never
// transcribed into the specification.
func TestSchemaHasTwentyThreeTablesAndTenIndexes(t *testing.T) {
	db := open(t)

	assert.Len(t, objects(t, db, "table"), 23)
	assert.Len(t, objects(t, db, "index"), 10)
}

func TestSchemaCreatesTheExpectedTables(t *testing.T) {
	db := open(t)

	assert.Equal(t, []string{
		"activity_log", "api_collections", "api_cookies", "api_environments", "api_folders",
		"api_history", "api_requests", "app_settings", "conversation_titles", "db_connections",
		"dbml_layouts", "job_history", "projects", "review_contexts", "review_runs",
		"ticket_links", "ticket_review_runs", "tickets", "workspace_agents", "workspace_mcps",
		"workspace_prompts", "workspace_skills", "workspaces",
	}, objects(t, db, "table"))
}

func TestSchemaCreatesTheExpectedIndexes(t *testing.T) {
	db := open(t)

	assert.Equal(t, []string{
		"idx_activity_log_project", "idx_api_cookies_key", "idx_api_folders_parent",
		"idx_api_history_time", "idx_api_requests_parent", "idx_dbml_layouts_key",
		"idx_job_history_project", "idx_review_runs_pr", "idx_ticket_review_runs_branch",
		"idx_tickets_identity",
	}, objects(t, db, "index"))
}

// The pragmas are per connection, and several behaviours depend on foreign_keys being on for every
// statement. This is what proves SetMaxOpenConns(1) actually kept them in force.
func TestPragmasAreInForce(t *testing.T) {
	db := open(t)

	require.NoError(t, db.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		var journal string
		require.NoError(t, sqlDB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal))
		assert.Equal(t, "wal", strings.ToLower(journal))

		var foreignKeys int
		require.NoError(t, sqlDB.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "cascade deletes depend on this")

		var synchronous int
		require.NoError(t, sqlDB.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous))
		assert.Equal(t, 1, synchronous, "NORMAL, SQLite's recommended pairing with WAL")
		return nil
	}))
}

// Running the whole procedure on every launch is the design (STORE-002); this is the assertion
// that it is safe.
func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codeflow.db")

	first, err := storage.Open(t.Context(), path)
	require.NoError(t, err)
	before := objects(t, first, "table")
	require.NoError(t, first.Close())

	second, err := storage.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	assert.Equal(t, before, objects(t, second, "table"))
	assert.Len(t, objects(t, second, "index"), 10)
}

func TestForeignKeysCascade(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	require.NoError(t, db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', '2026-01-01T00:00:00.0000000+00:00')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, workspace_id, name, local_path, created_at)
			 VALUES ('p1', 'w1', 'Repo', '/tmp/repo', '2026-01-01T00:00:00.0000000+00:00')`)
		return err
	}))

	require.NoError(t, db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id = 'w1'`)
		return err
	}))

	require.NoError(t, db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		var count int
		require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&count))
		assert.Equal(t, 0, count, "deleting a workspace must take its projects with it")
		return nil
	}))
}

// Stored timestamps are TEXT and are ordered as strings. A format with a different number of
// fractional digits, or a `Z` instead of `+00:00`, sorts into the wrong place against every row
// 2.x wrote — and nothing fails, the list just comes back strange.
func TestTimestampFormatMatchesWhatTwoPointXWrote(t *testing.T) {
	at := time.Date(2026, 9, 17, 14, 30, 5, 123456700, time.UTC)

	got := storage.FixedClock{At: at}.Now()

	assert.Equal(t, "2026-09-17T14:30:05.1234567+00:00", got)
	assert.Regexp(t, regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{7}\+00:00$`), got)
	assert.NotContains(t, got, "Z")
}

func TestSystemClockIsUTCAndSortsAsAString(t *testing.T) {
	first := storage.SystemClock{}.Now()
	time.Sleep(2 * time.Millisecond)
	second := storage.SystemClock{}.Now()

	assert.Len(t, first, len("2026-09-17T14:30:05.1234567+00:00"))
	assert.Less(t, first, second, "later must sort after earlier as plain strings")
}

// Write must roll back on error, or a failed command leaves half its work behind.
func TestWriteRollsBackOnError(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	err := db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`); err != nil {
			return err
		}
		return assert.AnError
	})

	require.ErrorIs(t, err, assert.AnError)
	require.NoError(t, db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		var count int
		require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&count))
		assert.Equal(t, 0, count)
		return nil
	}))
}

// bridge.Invoke recovers panics, so the process survives one — which means the *next* command
// would find the single connection inside an open transaction if this did not roll back.
func TestWriteRollsBackOnPanicAndLeavesTheConnectionUsable(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	assert.Panics(t, func() {
		_ = db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`); err != nil {
				return err
			}
			panic("a handler blew up mid-transaction")
		})
	})

	require.NoError(t, db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w2', 'Other', 'now')`)
		return err
	}))

	require.NoError(t, db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		var ids []string
		rows, err := sqlDB.QueryContext(ctx, `SELECT id FROM workspaces ORDER BY id`)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			require.NoError(t, rows.Scan(&id))
			ids = append(ids, id)
		}
		assert.Equal(t, []string{"w2"}, ids, "the panicking transaction must have left nothing")
		return rows.Err()
	}))
}

// One connection behind one mutex. Run with -race, this is what proves the serialisation holds.
func TestConcurrentReadsAndWritesAreSerialised(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('a' + i%26))
			_ = db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO workspaces (id, name, created_at) VALUES (?, ?, 'now')`, id, id)
				return err
			})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
				var count int
				return sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&count)
			})
		}()
	}
	wg.Wait()
}

func TestClosedDatabaseIsReportedRatherThanPanicking(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	assert.NoError(t, db.Close(), "closing twice is what a shutdown race looks like")

	assert.ErrorIs(t, db.Read(t.Context(), func(context.Context, *sql.DB) error { return nil }), storage.ErrClosed)
	assert.ErrorIs(t, db.Write(t.Context(), func(context.Context, *sql.Tx) error { return nil }), storage.ErrClosed)
}

// ---- the backfill ----------------------------------------------------------------------------

func promptOf(t *testing.T, db *storage.DB, workspaceID, kind string) string {
	t.Helper()
	var content string
	require.NoError(t, db.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		return sqlDB.QueryRowContext(ctx,
			`SELECT content FROM workspace_prompts WHERE workspace_id = ? AND kind = ?`,
			workspaceID, kind).Scan(&content)
	}))
	return content
}

func TestBackfillSeedsBothPromptsForEveryWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codeflow.db")
	db, err := storage.Open(t.Context(), path)
	require.NoError(t, err)

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`)
		return err
	}))
	require.NoError(t, db.Close())

	// The workspace existed before this launch, so the backfill is what has to notice it.
	reopened, err := storage.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	assert.Equal(t, ai.Prompt(ai.PromptPRReviewStandard), promptOf(t, reopened, "w1", "review_standard"))
	assert.Equal(t, ai.Prompt(ai.PromptPRDescription), promptOf(t, reopened, "w1", "pr_description"))
}

// The point of SeededPromptHistory: an improvement to a built-in default must reach the people who
// never edited it, and must never touch the people who did.
func TestBackfillNeverOverwritesTheUsersOwnText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codeflow.db")
	db, err := storage.Open(t.Context(), path)
	require.NoError(t, err)

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_prompts (workspace_id, kind, content, updated_at)
			 VALUES ('w1', 'review_standard', 'my own review methodology', 'now')`)
		return err
	}))
	require.NoError(t, db.Close())

	reopened, err := storage.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	assert.Equal(t, "my own review methodology", promptOf(t, reopened, "w1", "review_standard"))
}

// A workspace created before the api_* tables were workspace-scoped needs a Globals row; without
// one the environment picker has a hole it cannot fill.
func TestEveryWorkspaceGetsExactlyOneGlobalsEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codeflow.db")
	db, err := storage.Open(t.Context(), path)
	require.NoError(t, err)

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'A', 'now'), ('w2', 'B', 'now')`)
		return err
	}))
	require.NoError(t, db.Close())

	reopened, err := storage.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	require.NoError(t, reopened.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		for _, workspace := range []string{"w1", "w2"} {
			var count int
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM api_environments WHERE workspace_id = ? AND is_global = 1`,
				workspace).Scan(&count))
			assert.Equal(t, 1, count, workspace)
		}
		return nil
	}))

	// And running again must not add a second one.
	require.NoError(t, reopened.Close())
	third, err := storage.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = third.Close() })

	require.NoError(t, third.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		var count int
		require.NoError(t, sqlDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM api_environments WHERE is_global = 1`).Scan(&count))
		assert.Equal(t, 2, count, "one per workspace, not one per launch")
		return nil
	}))
}

// ---- the real 2.7.1 database (§5.2) -----------------------------------------------------------

// The drop-in proof. Opening a real 2.7.1 database with the Go layer must leave its schema
// untouched — every migration guard has to recognise that it already ran.
//
// Env-gated because it needs a real database: set CODEFLOW_TEST_DB to a *copy* of one. It is
// never pointed at a live install, which is why the variable takes a path instead of defaulting
// to ~/CodeFlow.
func TestOpeningARealTwoSevenOneDatabaseChangesNothing(t *testing.T) {
	source := os.Getenv("CODEFLOW_TEST_DB")
	if source == "" {
		t.Skip("set CODEFLOW_TEST_DB to a copy of a real 2.7.1 codeflow.db")
	}

	// The -wal and -shm siblings travel with it, and that is not a detail. A live 2.7.x install
	// keeps a great deal in the write-ahead log — on the database this was first run against, the
	// bare .db file did not even contain the `workspaces` table. Copying without them tests an
	// almost-empty database and proves nothing.
	path := filepath.Join(t.TempDir(), "codeflow.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(source + suffix)
		if err != nil {
			if suffix == "" {
				require.NoError(t, err, "the database itself must exist")
			}
			continue // a checkpointed database has no sidecar, which is fine
		}
		require.NoError(t, os.WriteFile(path+suffix, data, 0o600))
	}

	db, err := storage.Open(t.Context(), path)
	require.NoError(t, err, "a real 2.7.1 database must open")
	t.Cleanup(func() { _ = db.Close() })

	assert.Len(t, objects(t, db, "table"), 23, "no table added or dropped")
	assert.Len(t, objects(t, db, "index"), 10, "no index added or dropped")

	// The columns that arrived through ALTER in 2.x must all still be there, whatever order they
	// sit in.
	assert.Subset(t, columns(t, db, "activity_log"),
		[]string{"engine_session_id", "engine_version", "is_error", "model", "provider", "response_time_ms", "session_id", "trace"})
	assert.Subset(t, columns(t, db, "workspaces"),
		[]string{"ado_org", "ado_project", "git_email", "git_name"})
	assert.Subset(t, columns(t, db, "projects"),
		[]string{"github_host", "github_owner", "github_repo"})

	require.NoError(t, db.Read(t.Context(), func(ctx context.Context, sqlDB *sql.DB) error {
		var result string
		require.NoError(t, sqlDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result))
		assert.Equal(t, "ok", result)
		return nil
	}))
}
