package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/google/uuid"
)

// Store is every query this feature makes. One type rather than a function per table, because they
// share a database handle and a clock, and because keeping the SQL in one file is what makes the
// cascade behaviour reviewable in one sitting.
type Store struct {
	db    *storage.DB
	clock storage.Clock
}

// NewStore wires the store. clock is a parameter so a test can pin the timestamps rather than
// match them with a pattern.
func NewStore(db *storage.DB, clock storage.Clock) *Store {
	if clock == nil {
		clock = storage.SystemClock{}
	}
	return &Store{db: db, clock: clock}
}

// ErrNotFound is returned when a row a command names does not exist. Commands that answer
// `T | null` map it to nil; commands that act on a row return it.
var ErrNotFound = errors.New("not found")

// newID mints a row id. uuid.NewString produces the lowercase 8-4-4-4-12 form, which is what
// .NET's Guid.ToString() produced — ids written by 2.x and by 3.0 are the same shape, so nothing
// that sorts or compares them can tell which version created a row.
func newID() string { return uuid.NewString() }

// boolToInt is how every `enabled` column is written: the schema declares INTEGER, and the
// renderer types the field as a real boolean.
func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ---- workspaces -------------------------------------------------------------------------------

const workspaceColumns = `id, name, icon, color, sort_order, created_at,
	git_name, git_email, ado_org, ado_project`

func scanWorkspace(scan func(...any) error) (Workspace, error) {
	var w Workspace
	err := scan(&w.ID, &w.Name, &w.Icon, &w.Color, &w.SortOrder, &w.CreatedAt,
		&w.GitName, &w.GitEmail, &w.ADOOrg, &w.ADOProject)
	return w, err
}

// ListWorkspaces returns every workspace in the order the UI shows them.
//
// The slice is built with make(..., 0, …) and never left nil: the renderer maps over the result
// without guarding, so a nil slice would marshal as `null` and crash the sidebar on an empty
// install — the one case nobody tests by hand.
func (s *Store) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	out := make([]Workspace, 0, 8)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT `+workspaceColumns+` FROM workspaces ORDER BY sort_order, created_at`)
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}
		defer func() { _ = rows.Close() }() // rows.Err below carries the real failure

		for rows.Next() {
			w, err := scanWorkspace(rows.Scan)
			if err != nil {
				return fmt.Errorf("scan workspace: %w", err)
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// GetWorkspace returns one workspace, or ErrNotFound.
func (s *Store) GetWorkspace(ctx context.Context, id string) (Workspace, error) {
	var w Workspace
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id = ?`, id)
		found, err := scanWorkspace(row.Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get workspace: %w", err)
		}
		w = found
		return nil
	})
	return w, err
}

// CreateWorkspace adds a workspace and seeds the rows a new one is expected to have.
//
// Three things are seeded in the same transaction, because a workspace missing any of them is a
// broken one rather than an empty one: its sort order (appended to the end), its Globals
// environment, and its two default prompts. Doing them separately would leave a window where a
// crash produces a workspace the UI cannot render.
func (s *Store) CreateWorkspace(ctx context.Context, name, icon, color string, prompts map[string]string) (Workspace, error) {
	created := Workspace{
		ID:        newID(),
		Name:      name,
		Icon:      icon,
		Color:     color,
		CreatedAt: s.clock.Now(),
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var next sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(sort_order) FROM workspaces`).Scan(&next); err != nil {
			return fmt.Errorf("find the next sort order: %w", err)
		}
		if next.Valid {
			created.SortOrder = next.Int64 + 1
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, icon, color, sort_order, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			created.ID, created.Name, created.Icon, created.Color, created.SortOrder, created.CreatedAt,
		); err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO api_environments (id, workspace_id, name, variables, is_global, sort_order, created_at)
			 VALUES (?, ?, 'Globals', '[]', 1, 0, ?)`,
			newID(), created.ID, created.CreatedAt,
		); err != nil {
			return fmt.Errorf("seed the Globals environment: %w", err)
		}

		for kind, content := range prompts {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workspace_prompts (workspace_id, kind, content, updated_at)
				 VALUES (?, ?, ?, ?)`,
				created.ID, kind, content, created.CreatedAt,
			); err != nil {
				return fmt.Errorf("seed the %s prompt: %w", kind, err)
			}
		}
		return nil
	})
	return created, err
}

// RenameWorkspace and the two setters below are separate commands in the renderer rather than one
// update, so they stay separate here: each writes exactly the column it names.
func (s *Store) RenameWorkspace(ctx context.Context, id, name string) error {
	return s.update(ctx, `UPDATE workspaces SET name = ? WHERE id = ?`, name, id)
}

func (s *Store) SetWorkspaceColor(ctx context.Context, id, color string) error {
	return s.update(ctx, `UPDATE workspaces SET color = ? WHERE id = ?`, color, id)
}

// SetWorkspaceGitIdentity writes the commit-identity override. Both empty clears it back to NULL,
// which is what "use the global identity" is stored as — an empty string would be a configured
// identity with no name.
func (s *Store) SetWorkspaceGitIdentity(ctx context.Context, id string, name, email *string) error {
	if name != nil && *name == "" {
		name = nil
	}
	if email != nil && *email == "" {
		email = nil
	}
	return s.update(ctx, `UPDATE workspaces SET git_name = ?, git_email = ? WHERE id = ?`,
		storage.NullString(name), storage.NullString(email), id)
}

// SetWorkspaceTicketAccount writes which Azure organisation and board project this workspace's work
// items come from (WI-005).
//
// **Both columns in one write**, always, because a project name without the organisation it was
// listed from addresses nothing: changing the organisation clears the project, and the renderer
// sends the pair. Blank is stored as NULL — an empty string here would read as a chosen board with
// no name and fall through the resolution order to nothing anyway.
func (s *Store) SetWorkspaceTicketAccount(ctx context.Context, id string, org, project *string) error {
	if org != nil && strings.TrimSpace(*org) == "" {
		org = nil
	}
	if project != nil && strings.TrimSpace(*project) == "" {
		project = nil
	}
	return s.update(ctx, `UPDATE workspaces SET ado_org = ?, ado_project = ? WHERE id = ?`,
		storage.NullString(org), storage.NullString(project), id)
}

// ResolveGitIdentity answers the commit identity registered for a repository on disk (WS-008).
//
// The join is what makes the feature work: the user configures a name and email once per
// workspace, and every repository belonging to one of its projects commits under it. A path no
// project owns — a repository opened ad hoc — resolves to (nil, nil), which the git layer reads as
// "use whatever the repository's own config says".
//
// Both halves come back or neither is used: a workspace with a name and no email would otherwise
// produce commits attributed to the machine's email under someone else's name.
func (s *Store) ResolveGitIdentity(ctx context.Context, repoPath string) (*string, *string, error) {
	var name, email *string

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `
			SELECT w.git_name, w.git_email
			FROM projects p JOIN workspaces w ON w.id = p.workspace_id
			WHERE p.local_path = ?
			LIMIT 1`, repoPath)

		switch err := row.Scan(&name, &email); {
		case errors.Is(err, sql.ErrNoRows):
			// Not an error: most repositories are not registered, and committing in one is normal.
			return nil
		case err != nil:
			return fmt.Errorf("resolve git identity: %w", err)
		}
		return nil
	})
	return name, email, err
}

// DeleteWorkspace removes it. Every project, review context, prompt, agent, MCP, skill, API
// collection, environment and cookie goes with it through ON DELETE CASCADE — which only works
// because the connection keeps `foreign_keys` on (STORE-001).
func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
}

// ---- projects ---------------------------------------------------------------------------------

const projectColumns = `id, workspace_id, name, local_path, remote_url, color, icon,
	ado_org, ado_project, ado_repo_id, github_owner, github_repo, github_host, sort_order, created_at`

func scanProject(scan func(...any) error) (Project, error) {
	var p Project
	err := scan(&p.ID, &p.WorkspaceID, &p.Name, &p.LocalPath, &p.RemoteURL, &p.Color, &p.Icon,
		&p.ADOOrg, &p.ADOProject, &p.ADORepoID, &p.GitHubOwner, &p.GitHubRepo, &p.GitHubHost,
		&p.SortOrder, &p.CreatedAt)
	return p, err
}

// ListProjects returns a workspace's projects in display order.
func (s *Store) ListProjects(ctx context.Context, workspaceID string) ([]Project, error) {
	out := make([]Project, 0, 8)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT `+projectColumns+` FROM projects WHERE workspace_id = ? ORDER BY sort_order, created_at`,
			workspaceID)
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			p, err := scanProject(rows.Scan)
			if err != nil {
				return fmt.Errorf("scan project: %w", err)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// ListAllProjects returns every project in every workspace, ordered as ListProjects orders one
// workspace's.
//
// The one caller is resolving a pasted pull-request link: it has a repository and no idea which
// workspace holds the checkout, so it has to look at all of them.
func (s *Store) ListAllProjects(ctx context.Context) ([]Project, error) {
	out := make([]Project, 0, 16)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT `+projectColumns+` FROM projects ORDER BY workspace_id, sort_order, created_at`)
		if err != nil {
			return fmt.Errorf("list every project: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			p, err := scanProject(rows.Scan)
			if err != nil {
				return fmt.Errorf("scan project: %w", err)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// GetProject returns one project, or ErrNotFound.
func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
		found, err := scanProject(row.Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get project: %w", err)
		}
		p = found
		return nil
	})
	return p, err
}

// CreateProject adds a project to a workspace.
func (s *Store) CreateProject(ctx context.Context, input NewProject) (Project, error) {
	created := Project{
		ID:          newID(),
		WorkspaceID: input.WorkspaceID,
		Name:        input.Name,
		LocalPath:   input.LocalPath,
		RemoteURL:   input.RemoteURL,
		Color:       input.Color,
		Icon:        input.Icon,
		ADOOrg:      input.ADOOrg,
		ADOProject:  input.ADOProject,
		ADORepoID:   input.ADORepoID,
		GitHubOwner: input.GitHubOwner,
		GitHubRepo:  input.GitHubRepo,
		GitHubHost:  input.GitHubHost,
		CreatedAt:   s.clock.Now(),
	}
	if created.Color == "" {
		created.Color = "#6366f1" // the schema's own default, for a caller that sent none
	}
	if created.Icon == "" {
		created.Icon = "git-branch"
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var next sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(sort_order) FROM projects WHERE workspace_id = ?`, input.WorkspaceID).Scan(&next); err != nil {
			return fmt.Errorf("find the next sort order: %w", err)
		}
		if next.Valid {
			created.SortOrder = next.Int64 + 1
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (`+projectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			created.ID, created.WorkspaceID, created.Name, created.LocalPath,
			storage.NullString(created.RemoteURL), created.Color, created.Icon,
			storage.NullString(created.ADOOrg), storage.NullString(created.ADOProject),
			storage.NullString(created.ADORepoID), storage.NullString(created.GitHubOwner),
			storage.NullString(created.GitHubRepo), storage.NullString(created.GitHubHost),
			created.SortOrder, created.CreatedAt)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		return nil
	})
	return created, err
}

func (s *Store) SetProjectColor(ctx context.Context, id, color string) error {
	return s.update(ctx, `UPDATE projects SET color = ? WHERE id = ?`, color, id)
}

// MoveProjectToWorkspace re-parents a project. Its review runs carry a denormalised workspace_id
// that is deliberately left alone: a run records which workspace reviewed it at the time, and
// rewriting that would make history disagree with itself.
func (s *Store) MoveProjectToWorkspace(ctx context.Context, id, workspaceID string) error {
	return s.update(ctx, `UPDATE projects SET workspace_id = ? WHERE id = ?`, workspaceID, id)
}

// DeleteProject removes it, and with it every activity row, job history entry, review run, DBML
// layout and ticket link that referenced it.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM projects WHERE id = ?`, id)
}

// ---- the pull-request host link ---------------------------------------------------------------
//
// Six columns, three per host, written only through the three statements below. Each link writes
// **its own** columns and leaves the other host's alone: a project can legitimately carry both
// (`STORE-011`), dispatch prefers GitHub when it does (`REVIEW-001`), and clearing the other side
// here would silently change which host an existing project is reviewed on.

// LinkProjectGitHub points a project at a GitHub repository.
func (s *Store) LinkProjectGitHub(ctx context.Context, id, host, owner, repo string) error {
	return s.update(ctx,
		`UPDATE projects SET github_host = ?, github_owner = ?, github_repo = ? WHERE id = ?`,
		host, owner, repo, id)
}

// LinkProjectADO points a project at an Azure DevOps repository.
func (s *Store) LinkProjectADO(ctx context.Context, id, org, project, repoID string) error {
	return s.update(ctx,
		`UPDATE projects SET ado_org = ?, ado_project = ?, ado_repo_id = ? WHERE id = ?`,
		org, project, repoID, id)
}

// UnlinkProject clears both links at once.
//
// All six columns, whichever was set: the renderer offers one "unlink" for a project that is linked
// to one host as far as the user can see, and leaving the other three populated would re-link it on
// the next dispatch.
func (s *Store) UnlinkProject(ctx context.Context, id string) error {
	return s.update(ctx, `UPDATE projects SET ado_org = NULL, ado_project = NULL, ado_repo_id = NULL,
		github_host = NULL, github_owner = NULL, github_repo = NULL WHERE id = ?`, id)
}

// ---- settings ---------------------------------------------------------------------------------

// GetSetting returns a global setting, or nil when it was never set. The renderer types it as
// `string | null` and branches on the null, so "absent" and "empty" stay distinct.
func (s *Store) GetSetting(ctx context.Context, key string) (*string, error) {
	var value *string
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		var found string
		err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get setting %s: %w", key, err)
		}
		value = &found
		return nil
	})
	return value, err
}

// SetSetting writes a global setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO app_settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		if err != nil {
			return fmt.Errorf("set setting %s: %w", key, err)
		}
		return nil
	})
}

// ---- workspace prompts ------------------------------------------------------------------------

// GetWorkspacePrompt returns a workspace's prompt of a kind, or "" when there is no row.
//
// "" is meaningful rather than missing: an empty stored prompt means "use the built-in default",
// so resetting one is a blank save. The caller substitutes the default.
func (s *Store) GetWorkspacePrompt(ctx context.Context, workspaceID, kind string) (string, error) {
	var content string
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		err := db.QueryRowContext(ctx,
			`SELECT content FROM workspace_prompts WHERE workspace_id = ? AND kind = ?`,
			workspaceID, kind).Scan(&content)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get the %s prompt: %w", kind, err)
		}
		return nil
	})
	return content, err
}

// SetWorkspacePrompt writes one.
func (s *Store) SetWorkspacePrompt(ctx context.Context, workspaceID, kind, content string) error {
	now := s.clock.Now()
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_prompts (workspace_id, kind, content, updated_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(workspace_id, kind) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
			workspaceID, kind, content, now)
		if err != nil {
			return fmt.Errorf("set the %s prompt: %w", kind, err)
		}
		return nil
	})
}

// ---- review contexts, agents, MCPs --------------------------------------------------------------

// ListReviewContexts returns a workspace's contexts.
func (s *Store) ListReviewContexts(ctx context.Context, workspaceID string) ([]ReviewContext, error) {
	out := make([]ReviewContext, 0, 4)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT id, workspace_id, name, content, enabled, created_at
			   FROM review_contexts WHERE workspace_id = ? ORDER BY created_at`, workspaceID)
		if err != nil {
			return fmt.Errorf("list review contexts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var c ReviewContext
			if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Content, &c.Enabled, &c.CreatedAt); err != nil {
				return fmt.Errorf("scan review context: %w", err)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// UpsertReviewContext creates a context when id is nil and updates it otherwise, returning the row
// as it now stands — which is what the renderer puts straight into its store.
func (s *Store) UpsertReviewContext(ctx context.Context, id *string, workspaceID, name, content string, enabled bool) (ReviewContext, error) {
	row := ReviewContext{WorkspaceID: workspaceID, Name: name, Content: content, Enabled: enabled}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if id != nil && *id != "" {
			row.ID = *id
			result, err := tx.ExecContext(ctx,
				`UPDATE review_contexts SET name = ?, content = ?, enabled = ? WHERE id = ?`,
				name, content, boolToInt(enabled), row.ID)
			if err != nil {
				return fmt.Errorf("update review context: %w", err)
			}
			if affected, _ := result.RowsAffected(); affected == 0 {
				return ErrNotFound
			}
			return tx.QueryRowContext(ctx,
				`SELECT created_at FROM review_contexts WHERE id = ?`, row.ID).Scan(&row.CreatedAt)
		}

		row.ID = newID()
		row.CreatedAt = s.clock.Now()
		_, err := tx.ExecContext(ctx,
			`INSERT INTO review_contexts (id, workspace_id, name, content, enabled, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			row.ID, workspaceID, name, content, boolToInt(enabled), row.CreatedAt)
		if err != nil {
			return fmt.Errorf("create review context: %w", err)
		}
		return nil
	})
	return row, err
}

// DeleteReviewContext removes one.
func (s *Store) DeleteReviewContext(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM review_contexts WHERE id = ?`, id)
}

// ListAgents returns a workspace's agents in display order.
func (s *Store) ListAgents(ctx context.Context, workspaceID string) ([]Agent, error) {
	out := make([]Agent, 0, 4)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT id, workspace_id, name, role, provider, model, prompt, enabled, sort_order, created_at
			   FROM workspace_agents WHERE workspace_id = ? ORDER BY sort_order, created_at`, workspaceID)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var a Agent
			if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Role, &a.Provider, &a.Model,
				&a.Prompt, &a.Enabled, &a.SortOrder, &a.CreatedAt); err != nil {
				return fmt.Errorf("scan agent: %w", err)
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// UpsertAgent creates or updates an agent.
func (s *Store) UpsertAgent(ctx context.Context, id *string, a Agent) (Agent, error) {
	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if id != nil && *id != "" {
			a.ID = *id
			result, err := tx.ExecContext(ctx,
				`UPDATE workspace_agents SET name = ?, role = ?, provider = ?, model = ?, prompt = ?, enabled = ?
				  WHERE id = ?`,
				a.Name, a.Role, a.Provider, a.Model, a.Prompt, boolToInt(a.Enabled), a.ID)
			if err != nil {
				return fmt.Errorf("update agent: %w", err)
			}
			if affected, _ := result.RowsAffected(); affected == 0 {
				return ErrNotFound
			}
			return tx.QueryRowContext(ctx,
				`SELECT sort_order, created_at FROM workspace_agents WHERE id = ?`, a.ID).
				Scan(&a.SortOrder, &a.CreatedAt)
		}

		a.ID = newID()
		a.CreatedAt = s.clock.Now()
		var next sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(sort_order) FROM workspace_agents WHERE workspace_id = ?`, a.WorkspaceID).Scan(&next); err != nil {
			return fmt.Errorf("find the next sort order: %w", err)
		}
		if next.Valid {
			a.SortOrder = next.Int64 + 1
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_agents (id, workspace_id, name, role, provider, model, prompt, enabled, sort_order, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.WorkspaceID, a.Name, a.Role, a.Provider, a.Model, a.Prompt,
			boolToInt(a.Enabled), a.SortOrder, a.CreatedAt)
		if err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		return nil
	})
	return a, err
}

// DeleteAgent removes one.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM workspace_agents WHERE id = ?`, id)
}

// ListMCPs returns a workspace's MCP servers.
func (s *Store) ListMCPs(ctx context.Context, workspaceID string) ([]MCP, error) {
	out := make([]MCP, 0, 4)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT id, workspace_id, name, command, args, env, enabled, created_at
			   FROM workspace_mcps WHERE workspace_id = ? ORDER BY created_at`, workspaceID)
		if err != nil {
			return fmt.Errorf("list MCP servers: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var m MCP
			if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.Command, &m.Args, &m.Env,
				&m.Enabled, &m.CreatedAt); err != nil {
				return fmt.Errorf("scan MCP server: %w", err)
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// UpsertMCP creates or updates an MCP server.
func (s *Store) UpsertMCP(ctx context.Context, id *string, m MCP) (MCP, error) {
	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if id != nil && *id != "" {
			m.ID = *id
			result, err := tx.ExecContext(ctx,
				`UPDATE workspace_mcps SET name = ?, command = ?, args = ?, env = ?, enabled = ? WHERE id = ?`,
				m.Name, m.Command, m.Args, m.Env, boolToInt(m.Enabled), m.ID)
			if err != nil {
				return fmt.Errorf("update MCP server: %w", err)
			}
			if affected, _ := result.RowsAffected(); affected == 0 {
				return ErrNotFound
			}
			return tx.QueryRowContext(ctx,
				`SELECT created_at FROM workspace_mcps WHERE id = ?`, m.ID).Scan(&m.CreatedAt)
		}

		m.ID = newID()
		m.CreatedAt = s.clock.Now()
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_mcps (id, workspace_id, name, command, args, env, enabled, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, m.WorkspaceID, m.Name, m.Command, m.Args, m.Env, boolToInt(m.Enabled), m.CreatedAt)
		if err != nil {
			return fmt.Errorf("create MCP server: %w", err)
		}
		return nil
	})
	return m, err
}

// DeleteMCP removes one.
func (s *Store) DeleteMCP(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM workspace_mcps WHERE id = ?`, id)
}

// ---- shared -----------------------------------------------------------------------------------

// update runs a statement that is expected to touch exactly one row, and reports ErrNotFound when
// it touched none. Deletes included: a delete that matched nothing means the caller is working
// from a stale list, which the UI wants to know about.
func (s *Store) update(ctx context.Context, query string, args ...any) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
