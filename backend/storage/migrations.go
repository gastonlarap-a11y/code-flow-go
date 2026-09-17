package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
)

// The whole migration system (STORE-002).
//
// There is no version table — no `schema_migrations`, no `PRAGMA user_version`. Every step decides
// for itself, from the live schema, whether it already ran. That is what makes running the entire
// procedure on every launch safe, and it is also what makes it safe after a crash: there is no
// recorded position to be wrong about.
//
// Two ordering constraints are real and the rest is incidental:
//
//   - migrateAPITablesBegin runs **before** the schema batch. After it, the four legacy tables
//     would already be in their current shape and it would silently do nothing.
//   - backfillWorkspacePrompts runs **after** migrateReviewStandardsIntoPrompts (STORE-003), or it
//     would seed a default over a row the fold is about to write.
//
// `addGitHubHostToProjects` after `addGitHubColumnsToProjects` is convention rather than a
// requirement; kept in order anyway so the sequence reads the way the specification lists it.

// Migrate runs the full procedure. Called by Open on every launch.
func Migrate(ctx context.Context, db *DB) error {
	// Step 1, before the batch. It manages its own foreign_keys pragma, which cannot be changed
	// inside a transaction — so it runs outside one, on the single connection.
	if err := db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		return migrateAPITablesBegin(ctx, sqlDB)
	}); err != nil {
		return fmt.Errorf("api tables (begin): %w", err)
	}

	// Step 2, the schema batch. Every statement is individually idempotent.
	if err := db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		_, err := sqlDB.ExecContext(ctx, schemaSQL)
		return err
	}); err != nil {
		return fmt.Errorf("schema batch: %w", err)
	}

	// Step 3, also outside a transaction for the pragma's sake.
	if err := db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		return migrateAPITablesFinish(ctx, sqlDB)
	}); err != nil {
		return fmt.Errorf("api tables (finish): %w", err)
	}

	// Steps 4–20. Each is guarded, so the whole group is idempotent and — apart from the two
	// constraints above — order-independent.
	steps := []struct {
		name string
		run  func(context.Context, *sql.Tx) error
	}{
		{"globals environment", ensureGlobalsEnvironment},
		{"review contexts to workspace", migrateReviewContextsToWorkspace},
		{"md files into contexts", migrateMDFilesIntoContexts},
		{"review standards into prompts", migrateReviewStandardsIntoPrompts},
		{"backfill workspace prompts", backfillWorkspacePrompts},
		{"drop legacy installed_skills", dropLegacyInstalledSkills},

		// Steps 10–15: activity_log grew seven columns over time. Each is has_column-guarded, so
		// on a fresh database — where the batch already declares them — every one is a no-op.
		{"activity_log columns", addActivityLogColumns},
		{"job_history.custom_label", addCustomLabelToJobHistory},
		{"projects github columns", addGitHubColumnsToProjects},
		{"projects.github_host", addGitHubHostToProjects},
		{"workspace_skills.enabled", addEnabledToWorkspaceSkills},
		{"workspace_agents.provider", addProviderToWorkspaceAgents},

		// Not in `03-storage.md`'s numbered call order. Its own schema DDL names both steps in
		// comments ("Added by AddGitIdentityToWorkspaces", "Both added by AddAdoOrgToWorkspaces
		// for pre-existing databases") but the list of steps never mentions them — a gap found by
		// running this package against a real 2.7.1 database, where the four columns exist and
		// nothing here would have created them. The document is corrected in the same change.
		{"workspaces git identity", addGitIdentityToWorkspaces},
		{"workspaces ado columns", addAdoColumnsToWorkspaces},
	}

	for _, step := range steps {
		if err := db.Write(ctx, step.run); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

// ---- guards ---------------------------------------------------------------------------------

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// tableExists is the table-level guard.
func tableExists(ctx context.Context, q querier, name string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up table %s: %w", name, err)
	}
	return true, nil
}

// hasColumn is the column-level guard. PRAGMA table_info is the only portable way to ask, and it
// returns nothing at all for a table that does not exist — which is the answer this wants.
func hasColumn(ctx context.Context, q querier, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }() // the error below is the one that matters

	for rows.Next() {
		var (
			cid        int
			name, kind string
			notNull    int
			dflt       sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("scan the columns of %s: %w", table, err)
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

// addColumn applies one guarded `ALTER TABLE ... ADD COLUMN`. definition carries the type and any
// default, exactly as the column is declared in the schema batch.
func addColumn(ctx context.Context, tx *sql.Tx, table, column, definition string) error {
	present, err := hasColumn(ctx, tx, table, column)
	if err != nil || present {
		return err
	}
	if exists, err := tableExists(ctx, tx, table); err != nil || !exists {
		return err
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s %s", table, column, definition))
	if err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

// ---- steps 1 and 3: the two-phase api_* migration --------------------------------------------

// legacyAPITables are the four pre-workspace roots, in the order the finish step copies them.
var legacyAPITables = []string{"api_collections", "api_environments", "api_history", "api_cookies"}

// migrateAPITablesBegin renames the four pre-workspace api_* tables aside (step 1).
//
// Why a rename rather than an ADD COLUMN: SQLite refuses `ALTER TABLE ... ADD COLUMN` for a column
// carrying a REFERENCES clause while foreign keys are on, so `workspace_id REFERENCES
// workspaces(id)` cannot simply be added. Renaming out of the way lets the schema batch recreate
// the tables in their current shape, and the finish step copies the rows across.
//
// `legacy_alter_table = ON` is the subtle part. Modern SQLite rewrites REFERENCES clauses in
// *other* tables when the table they point at is renamed — so `api_folders` and `api_requests`
// would end up referencing `api_collections_legacy`, which the finish step then drops. With the
// pragma on, the rename leaves them declaring `REFERENCES api_collections(id)`, which resolves to
// the recreated table with no further action.
func migrateAPITablesBegin(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, "api_collections")
	if err != nil || !exists {
		return err
	}
	scoped, err := hasColumn(ctx, db, "api_collections", "workspace_id")
	if err != nil || scoped {
		return err // already migrated
	}

	// Not a transaction: PRAGMA foreign_keys is a no-op inside one.
	statements := []string{
		"PRAGMA foreign_keys = OFF",
		"PRAGMA legacy_alter_table = ON",
		// The indexes go rather than travel: a renamed table keeps its indexes under their
		// original names, which would collide with the schema batch recreating them.
		"DROP INDEX IF EXISTS idx_api_history_time",
		"DROP INDEX IF EXISTS idx_api_cookies_key",
	}
	for _, table := range legacyAPITables {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s RENAME TO %s_legacy", table, table))
	}
	statements = append(statements, "PRAGMA legacy_alter_table = OFF")

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("%s: %w", statement, err)
		}
	}
	return nil
}

// migrateAPITablesFinish copies the pre-workspace rows into the recreated tables (step 3).
//
// BUG-STORE-a — the one step that was not safe to re-run after a crash — is closed here: the copy
// and the drops happen in a single transaction with `INSERT OR IGNORE`, so a failure part-way
// leaves the legacy tables intact for the next launch to retry.
func migrateAPITablesFinish(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, "api_collections_legacy")
	if err != nil || !exists {
		return err
	}

	// The oldest workspace by the app's own sort order, tie-broken by creation time.
	var workspaceID string
	err = db.QueryRowContext(ctx,
		`SELECT id FROM workspaces ORDER BY sort_order, created_at LIMIT 1`).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		// STORE-006: no workspace to attach the rows to. Leave the legacy tables completely
		// alone — a later launch, once a workspace exists, finishes the job. Dropping them here
		// would destroy the user's collections.
		return nil
	}
	if err != nil {
		return fmt.Errorf("find the target workspace: %w", err)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	defer func() {
		// Ignored: the copy's own error is what the caller needs, and leaving foreign keys off
		// would be worse than any message this could add.
		_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful commit

	copies := []string{
		`INSERT OR IGNORE INTO api_collections
		    (id, workspace_id, name, description, auth, pre_script, post_script, variables, sort_order, created_at, updated_at)
		 SELECT id, ?, name, description, auth, pre_script, post_script, variables, sort_order, created_at, updated_at
		   FROM api_collections_legacy`,

		// STORE-008: is_global = 0 only. The legacy global row is deliberately left behind —
		// ensureGlobalsEnvironment has already created the workspace's own Globals row, and
		// copying the old one would produce two.
		`INSERT OR IGNORE INTO api_environments
		    (id, workspace_id, name, variables, is_global, sort_order, created_at)
		 SELECT id, ?, name, variables, is_global, sort_order, created_at
		   FROM api_environments_legacy WHERE is_global = 0`,

		`INSERT OR IGNORE INTO api_history
		    (id, workspace_id, request_id, name, protocol, method, url, status, duration_ms, size_bytes, snapshot, created_at)
		 SELECT id, ?, request_id, name, protocol, method, url, status, duration_ms, size_bytes, snapshot, created_at
		   FROM api_history_legacy`,

		`INSERT OR IGNORE INTO api_cookies
		    (id, workspace_id, domain, path, name, value, secure, http_only, expires, updated_at)
		 SELECT id, ?, domain, path, name, value, secure, http_only, expires, updated_at
		   FROM api_cookies_legacy`,
	}
	for _, statement := range copies {
		if _, err := tx.ExecContext(ctx, statement, workspaceID); err != nil {
			return fmt.Errorf("copy legacy api rows: %w", err)
		}
	}

	for _, table := range legacyAPITables {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s_legacy", table)); err != nil {
			return fmt.Errorf("drop %s_legacy: %w", table, err)
		}
	}
	return tx.Commit()
}

// ---- steps 4–9 --------------------------------------------------------------------------------

// ensureGlobalsEnvironment seeds the one is_global row every workspace needs (step 4).
//
// The Globals pseudo-environment is always in scope and cannot be deleted or switched away from,
// so a workspace without one has a hole the UI cannot fill.
func ensureGlobalsEnvironment(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO api_environments (id, workspace_id, name, variables, is_global, sort_order, created_at)
		SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
		       substr(lower(hex(randomblob(2))), 2) || '-a' ||
		       substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6))),
		       w.id, 'Globals', '[]', 1, 0, w.created_at
		  FROM workspaces w
		 WHERE NOT EXISTS (
		       SELECT 1 FROM api_environments e WHERE e.workspace_id = w.id AND e.is_global = 1)`)
	if err != nil {
		return fmt.Errorf("seed the Globals environment: %w", err)
	}
	return nil
}

// migrateReviewContextsToWorkspace re-points pre-workspace rows and drops the old column (step 5).
func migrateReviewContextsToWorkspace(ctx context.Context, tx *sql.Tx) error {
	legacy, err := hasColumn(ctx, tx, "review_contexts", "project_id")
	if err != nil || !legacy {
		return err
	}

	// A context belonged to a project; its workspace is that project's.
	if _, err := tx.ExecContext(ctx, `
		UPDATE review_contexts
		   SET workspace_id = COALESCE(
		       (SELECT p.workspace_id FROM projects p WHERE p.id = review_contexts.project_id),
		       workspace_id)
		 WHERE workspace_id = '' OR workspace_id IS NULL`); err != nil {
		return fmt.Errorf("re-point review contexts: %w", err)
	}

	// Orphans — a context whose project is already gone — have no workspace to belong to and
	// would violate the NOT NULL foreign key.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM review_contexts WHERE workspace_id = '' OR workspace_id IS NULL`); err != nil {
		return fmt.Errorf("drop orphaned review contexts: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `ALTER TABLE review_contexts DROP COLUMN project_id`); err != nil {
		return fmt.Errorf("drop review_contexts.project_id: %w", err)
	}
	return nil
}

// migrateMDFilesIntoContexts folds the old workspace_md_files table into review_contexts (step 6).
func migrateMDFilesIntoContexts(ctx context.Context, tx *sql.Tx) error {
	exists, err := tableExists(ctx, tx, "workspace_md_files")
	if err != nil || !exists {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO review_contexts (id, workspace_id, name, content, enabled, created_at)
		SELECT id, workspace_id, name, content, 1, created_at FROM workspace_md_files`); err != nil {
		return fmt.Errorf("fold md files into contexts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE workspace_md_files`); err != nil {
		return fmt.Errorf("drop workspace_md_files: %w", err)
	}
	return nil
}

// migrateReviewStandardsIntoPrompts folds workspace_review_standards into workspace_prompts under
// kind = 'review_standard' (step 7).
func migrateReviewStandardsIntoPrompts(ctx context.Context, tx *sql.Tx) error {
	exists, err := tableExists(ctx, tx, "workspace_review_standards")
	if err != nil || !exists {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO workspace_prompts (workspace_id, kind, content, updated_at)
		SELECT workspace_id, 'review_standard', content, updated_at
		  FROM workspace_review_standards`); err != nil {
		return fmt.Errorf("fold review standards into prompts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE workspace_review_standards`); err != nil {
		return fmt.Errorf("drop workspace_review_standards: %w", err)
	}
	return nil
}

// promptKinds maps a workspace_prompts.kind to the embedded default that seeds it.
var promptKinds = map[string]string{
	"review_standard": ai.PromptPRReviewStandard,
	"pr_description":  ai.PromptPRDescription,
}

// backfillWorkspacePrompts seeds the built-in defaults into every workspace missing them, and
// refreshes rows that still hold a superseded default (step 8).
//
// Must run after step 7 (STORE-003): the fold writes real `review_standard` rows, and seeding
// first would leave `INSERT OR IGNORE` skipping them.
func backfillWorkspacePrompts(ctx context.Context, tx *sql.Tx) error {
	if err := ai.VerifyPrompts(); err != nil {
		return err
	}

	for kind, prompt := range promptKinds {
		// Seed the workspaces that have no row of this kind at all.
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO workspace_prompts (workspace_id, kind, content, updated_at)
			SELECT w.id, ?, ?, w.created_at FROM workspaces w`,
			kind, ai.Prompt(prompt)); err != nil {
			return fmt.Errorf("seed %s prompts: %w", kind, err)
		}

		// Then refresh the ones still holding an unedited former default. Done row by row because
		// the decision is a digest comparison, which SQL cannot make.
		if err := refreshSeededPrompts(ctx, tx, kind, prompt); err != nil {
			return err
		}
	}
	return nil
}

func refreshSeededPrompts(ctx context.Context, tx *sql.Tx, kind, prompt string) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT workspace_id, content FROM workspace_prompts WHERE kind = ?`, kind)
	if err != nil {
		return fmt.Errorf("read %s prompts: %w", kind, err)
	}

	type stale struct{ workspaceID, content string }
	var refresh []stale
	for rows.Next() {
		var workspaceID, content string
		if err := rows.Scan(&workspaceID, &content); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan %s prompts: %w", kind, err)
		}
		if updated := ai.RefreshSeededPrompt(prompt, content); updated != content {
			refresh = append(refresh, stale{workspaceID, updated})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read %s prompts: %w", kind, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read %s prompts: %w", kind, err)
	}

	for _, row := range refresh {
		if _, err := tx.ExecContext(ctx,
			`UPDATE workspace_prompts SET content = ? WHERE workspace_id = ? AND kind = ?`,
			row.content, row.workspaceID, kind); err != nil {
			return fmt.Errorf("refresh %s prompt: %w", kind, err)
		}
	}
	return nil
}

// dropLegacyInstalledSkills removes the old, never-used table unconditionally (step 9).
func dropLegacyInstalledSkills(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS installed_skills`); err != nil {
		return fmt.Errorf("drop installed_skills: %w", err)
	}
	return nil
}

// ---- steps 10–20: guarded column additions ---------------------------------------------------

// addActivityLogColumns covers steps 10–15. The definitions are the ones a real 2.7.1 database
// carries, so a migrated install and a fresh one end up with identical types and defaults.
func addActivityLogColumns(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct{ name, definition string }{
		{"session_id", "TEXT"},
		{"response_time_ms", "INTEGER"},
		{"is_error", "INTEGER NOT NULL DEFAULT 0"},
		{"engine_session_id", "TEXT"},
		{"trace", "TEXT"},
		{"provider", "TEXT"},
		{"model", "TEXT"},
		{"engine_version", "TEXT"},
	} {
		if err := addColumn(ctx, tx, "activity_log", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func addCustomLabelToJobHistory(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, "job_history", "custom_label", "TEXT")
}

func addGitHubColumnsToProjects(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []string{"github_owner", "github_repo"} {
		if err := addColumn(ctx, tx, "projects", column, "TEXT"); err != nil {
			return err
		}
	}
	return nil
}

// addGitHubHostToProjects runs after the two columns above: a row with an owner and a repo but a
// NULL host is read elsewhere as defaulting to github.com, so the three arrive together.
func addGitHubHostToProjects(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, "projects", "github_host", "TEXT")
}

func addEnabledToWorkspaceSkills(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, "workspace_skills", "enabled", "INTEGER NOT NULL DEFAULT 1")
}

func addProviderToWorkspaceAgents(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, "workspace_agents", "provider", "TEXT NOT NULL DEFAULT ''")
}

// addGitIdentityToWorkspaces adds the per-workspace commit identity (WS-008). Both null means
// "use the global git identity", which is why neither column has a default.
func addGitIdentityToWorkspaces(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []string{"git_name", "git_email"} {
		if err := addColumn(ctx, tx, "workspaces", column, "TEXT"); err != nil {
			return err
		}
	}
	return nil
}

// addAdoColumnsToWorkspaces adds the Azure DevOps organisation and board project a workspace's
// work items come from (WI-005). Null means "not chosen", and the resolution falls through to the
// project's own link — a repository hosted on GitHub has no projects.ado_project at all, which is
// why the workspace needs its own pair.
func addAdoColumnsToWorkspaces(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []string{"ado_org", "ado_project"} {
		if err := addColumn(ctx, tx, "workspaces", column, "TEXT"); err != nil {
			return err
		}
	}
	return nil
}
