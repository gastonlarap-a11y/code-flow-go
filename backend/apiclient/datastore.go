package apiclient

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Environments, history and the cookie jar.

// ---- environments ----------------------------------------------------------------------------------

// ListEnvironments reads a workspace's environments.
//
// `ORDER BY sort_order, created_at` always puts Globals first, because the migration that seeds it
// gives it `sort_order = -1` — that is what makes "always in scope" visible rather than merely true.
func (s *Store) ListEnvironments(ctx context.Context, workspaceID string) ([]Environment, error) {
	out := make([]Environment, 0, 8)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT id, workspace_id, name, variables, is_global, sort_order, created_at
			  FROM api_environments
			 WHERE workspace_id = ?
			 ORDER BY sort_order, created_at`, workspaceID)
		if err != nil {
			return fmt.Errorf("list environments: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var e Environment
			if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.Name, &e.Variables, &e.IsGlobal,
				&e.SortOrder, &e.CreatedAt); err != nil {
				return fmt.Errorf("scan an environment: %w", err)
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// CreateEnvironment inserts an ordinary environment, last in its workspace.
func (s *Store) CreateEnvironment(ctx context.Context, workspaceID, name string) (Environment, error) {
	environment := Environment{
		ID:          uuid.NewString(),
		WorkspaceID: workspaceID,
		Name:        name,
		Variables:   "[]",
		CreatedAt:   s.clock.Now(),
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		order, err := nextOrder(ctx, tx,
			`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM api_environments WHERE workspace_id = ?`,
			workspaceID)
		if err != nil {
			return err
		}
		environment.SortOrder = order

		_, err = tx.ExecContext(ctx, `
			INSERT INTO api_environments (id, workspace_id, name, variables, is_global, sort_order,
			                              created_at)
			VALUES (?, ?, ?, '[]', 0, ?, ?)`,
			environment.ID, workspaceID, name, environment.SortOrder, environment.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert an environment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Environment{}, err
	}
	return environment, nil
}

// UpdateEnvironment replaces an environment's editable columns.
//
// `is_global` is **not** among them, in either direction: a second Globals row would make
// `ensure_globals_environment` skip the seeding forever, and clearing the flag on the only one would
// leave the workspace with no always-in-scope variables and no way back to it from the UI.
func (s *Store) UpdateEnvironment(ctx context.Context, e Environment) error {
	return s.write(ctx,
		`UPDATE api_environments SET name = ?, variables = ?, sort_order = ? WHERE id = ?`,
		e.Name, e.Variables, e.SortOrder, e.ID)
}

// DeleteEnvironment removes an environment — **except** a Globals row, which is a no-op.
//
// A no-op rather than an error: the renderer hides the button on that row, so reaching here is a
// state that should not be, and refusing loudly would put an error in front of a user who cannot
// have meant it. The `is_global = 0` clause is what makes it structural rather than a check
// somebody has to remember to write.
func (s *Store) DeleteEnvironment(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM api_environments WHERE id = ? AND is_global = 0`, id)
}

// DuplicateEnvironment copies an environment, last in its workspace (STORE-019).
//
// Allowed on the Globals row, and the copy is always an ordinary environment — never a second
// Globals, which is the one thing that would break the seeding.
func (s *Store) DuplicateEnvironment(ctx context.Context, id string) (Environment, error) {
	var copied Environment

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var source Environment
		err := tx.QueryRowContext(ctx, `
			SELECT id, workspace_id, name, variables, is_global, sort_order, created_at
			  FROM api_environments WHERE id = ?`, id).
			Scan(&source.ID, &source.WorkspaceID, &source.Name, &source.Variables,
				&source.IsGlobal, &source.SortOrder, &source.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("Unknown environment %s: %w", id, ErrNotFound) //nolint:staticcheck // ST1005: VERBATIM
		}
		if err != nil {
			return fmt.Errorf("read the environment: %w", err)
		}

		order, err := nextOrder(ctx, tx,
			`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM api_environments WHERE workspace_id = ?`,
			source.WorkspaceID)
		if err != nil {
			return err
		}

		copied = source
		copied.ID = uuid.NewString()
		copied.Name = source.Name + " copy"
		copied.IsGlobal = false
		copied.SortOrder = order
		copied.CreatedAt = s.clock.Now()

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_environments (id, workspace_id, name, variables, is_global, sort_order,
			                              created_at)
			VALUES (?, ?, ?, ?, 0, ?, ?)`,
			copied.ID, copied.WorkspaceID, copied.Name, copied.Variables, copied.SortOrder,
			copied.CreatedAt); err != nil {
			return fmt.Errorf("insert the copied environment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Environment{}, err
	}
	return copied, nil
}

// ---- history ---------------------------------------------------------------------------------------

// ListHistory reads a workspace's most recent sends.
//
// The limit is the settings UI's display limit, not the storage bound: what the table carries is
// capped separately at `historyHardCap` by every insert.
func (s *Store) ListHistory(ctx context.Context, workspaceID string, limit int64) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	out := make([]HistoryEntry, 0, 64)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT id, workspace_id, request_id, name, protocol, method, url, status,
			       duration_ms, size_bytes, snapshot, created_at
			  FROM api_history
			 WHERE workspace_id = ?
			 ORDER BY created_at DESC
			 LIMIT ?`, workspaceID, limit)
		if err != nil {
			return fmt.Errorf("list history: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var e HistoryEntry
			if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.RequestID, &e.Name, &e.Protocol,
				&e.Method, &e.URL, &e.Status, &e.DurationMs, &e.SizeBytes,
				&e.Snapshot, &e.CreatedAt); err != nil {
				return fmt.Errorf("scan a history entry: %w", err)
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// AddHistory records one send and trims the workspace's history in the same transaction
// (STORE-016).
//
// Idempotent by id, and it honours a caller-supplied `created_at` when there is one — a replayed or
// re-imported entry keeps the instant the request actually ran, rather than being restamped to the
// moment it was replayed.
func (s *Store) AddHistory(ctx context.Context, entry HistoryEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt == "" {
		entry.CreatedAt = s.clock.Now()
	}
	if entry.Snapshot == "" {
		entry.Snapshot = "{}"
	}

	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_history (id, workspace_id, request_id, name, protocol, method, url,
			                         status, duration_ms, size_bytes, snapshot, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`,
			entry.ID, entry.WorkspaceID, entry.RequestID, entry.Name, entry.Protocol,
			entry.Method, entry.URL, entry.Status, entry.DurationMs, entry.SizeBytes,
			entry.Snapshot, entry.CreatedAt); err != nil {
			return fmt.Errorf("insert a history entry: %w", err)
		}

		// Scoped to the workspace that just received an entry, so a repository with heavy API
		// traffic never evicts a rarely-used workspace's history.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM api_history
			 WHERE workspace_id = ?
			   AND id NOT IN (
			       SELECT id FROM api_history WHERE workspace_id = ?
			        ORDER BY created_at DESC LIMIT ?)`,
			entry.WorkspaceID, entry.WorkspaceID, historyHardCap); err != nil {
			return fmt.Errorf("trim the history: %w", err)
		}
		return nil
	})
}

// DeleteHistory removes one entry.
func (s *Store) DeleteHistory(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM api_history WHERE id = ?`, id)
}

// ClearHistory empties one workspace's history, and only that workspace's.
func (s *Store) ClearHistory(ctx context.Context, workspaceID string) error {
	return s.write(ctx, `DELETE FROM api_history WHERE workspace_id = ?`, workspaceID)
}

// ---- cookies ---------------------------------------------------------------------------------------

// ListCookies reads a workspace's jar.
func (s *Store) ListCookies(ctx context.Context, workspaceID string) ([]Cookie, error) {
	out := make([]Cookie, 0, 16)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT id, workspace_id, domain, path, name, value, secure, http_only, expires,
			       updated_at
			  FROM api_cookies
			 WHERE workspace_id = ?
			 ORDER BY domain, path, name`, workspaceID)
		if err != nil {
			return fmt.Errorf("list cookies: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var c Cookie
			if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Domain, &c.Path, &c.Name, &c.Value,
				&c.Secure, &c.HTTPOnly, &c.Expires, &c.UpdatedAt); err != nil {
				return fmt.Errorf("scan a cookie: %w", err)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// UpsertCookie writes one cookie, keyed on its **wire identity** rather than on the row id
// (STORE-020).
//
// `(workspace_id, domain, path, name)` is how `Set-Cookie` identifies a cookie on the wire, so a
// second send that updates one never accumulates a duplicate — and the same cookie in two
// workspaces stays two rows, because a jar belongs to a workspace.
func (s *Store) UpsertCookie(ctx context.Context, cookie Cookie) error {
	if cookie.ID == "" {
		cookie.ID = uuid.NewString()
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}

	return s.write(ctx, `
		INSERT INTO api_cookies (id, workspace_id, domain, path, name, value, secure, http_only,
		                         expires, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, domain, path, name) DO UPDATE SET
		    value      = excluded.value,
		    secure     = excluded.secure,
		    http_only  = excluded.http_only,
		    expires    = excluded.expires,
		    updated_at = excluded.updated_at`,
		cookie.ID, cookie.WorkspaceID, cookie.Domain, cookie.Path, cookie.Name, cookie.Value,
		cookie.Secure, cookie.HTTPOnly, cookie.Expires, s.clock.Now())
}

// DeleteCookie removes one cookie by row id.
func (s *Store) DeleteCookie(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM api_cookies WHERE id = ?`, id)
}

// ClearCookies empties one workspace's jar.
func (s *Store) ClearCookies(ctx context.Context, workspaceID string) error {
	return s.write(ctx, `DELETE FROM api_cookies WHERE workspace_id = ?`, workspaceID)
}
