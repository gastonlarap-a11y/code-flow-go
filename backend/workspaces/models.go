// Package workspaces owns the top of CodeFlow's data model: a workspace, the projects inside it,
// and the settings, prompts, review contexts, agents and MCP servers scoped to it.
//
// A workspace is the unit everything else hangs from. Review contexts, prompts, agents, MCP
// servers, API collections and the cookie jar are all workspace-scoped rather than project-scoped,
// because the several repositories of one workspace — frontend, backend, infra — normally share a
// service, a review methodology and a set of conventions. Scoping per repository would mean
// re-creating the same thing in each.
package workspaces

// Every struct here carries explicit snake_case tags, because the renderer's `types/domain.ts`
// declares these shapes by hand and reads the fields literally. A field renamed on this side
// compiles and arrives as `undefined`, which renders as a blank row rather than an error.
//
// Nullable columns are pointers so they marshal as `null`, never omitted: the renderer types them
// as `T | null` and branches on the null, so an absent key and a null one are not the same thing
// to it.

// Workspace is a top-level grouping of projects.
type Workspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	Color     string `json:"color"`
	SortOrder int64  `json:"sort_order"`
	CreatedAt string `json:"created_at"`

	// Commit-identity override (WS-008). Both null means "use the global git identity", which is
	// why they are pointers rather than empty strings — an empty name is a choice the user could
	// conceivably make, and "not chosen" has to stay distinguishable from it.
	GitName  *string `json:"git_name"`
	GitEmail *string `json:"git_email"`

	// Which Azure DevOps organisation and board project this workspace's work items come from
	// (WI-005). Null falls through to the linked project's own — which a GitHub-hosted repository
	// does not have, so the workspace is the only place it can be said.
	ADOOrg     *string `json:"ado_org"`
	ADOProject *string `json:"ado_project"`
}

// Project is one repository inside a workspace.
type Project struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	LocalPath   string  `json:"local_path"`
	RemoteURL   *string `json:"remote_url"`
	Color       string  `json:"color"`
	Icon        string  `json:"icon"`

	ADOOrg     *string `json:"ado_org"`
	ADOProject *string `json:"ado_project"`
	ADORepoID  *string `json:"ado_repo_id"`

	GitHubOwner *string `json:"github_owner"`
	GitHubRepo  *string `json:"github_repo"`
	// Null is read elsewhere as github.com, which is why the three GitHub columns always travel
	// together and why a migration that added the first two without this one was a bug.
	GitHubHost *string `json:"github_host"`

	SortOrder int64  `json:"sort_order"`
	CreatedAt string `json:"created_at"`
}

// NewProject is what create_project receives. It has no id, sort order or creation time — those
// are the backend's to assign — and its colour is optional, because the renderer picks the
// least-used hue when the user did not choose one.
type NewProject struct {
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	LocalPath   string  `json:"local_path"`
	RemoteURL   *string `json:"remote_url"`
	Color       string  `json:"color"`
	Icon        string  `json:"icon"`

	ADOOrg     *string `json:"ado_org"`
	ADOProject *string `json:"ado_project"`
	ADORepoID  *string `json:"ado_repo_id"`

	GitHubOwner *string `json:"github_owner"`
	GitHubRepo  *string `json:"github_repo"`
	GitHubHost  *string `json:"github_host"`
}

// ReviewContext is a piece of workspace-wide context handed to the reviewer — conventions, a
// glossary, an architecture note.
type ReviewContext struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
}

// Agent is a user-defined role: a name, what it is for, and which model answers as it. There is no
// preset roster on purpose — the user creates their own.
type Agent struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	Enabled     bool   `json:"enabled"`
	SortOrder   int64  `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
}

// MCP is an MCP server configured for a workspace, written out as a --mcp-config file for the
// headless engine invocations that accept one.
type MCP struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Command     string `json:"command"`
	Args        string `json:"args"`
	Env         string `json:"env"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
}

// Skill is a skill installed into a workspace and synced into whichever project is being reviewed.
type Skill struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	SkillName   string `json:"skill_name"`
	SourceRepo  string `json:"source_repo"`
	Enabled     bool   `json:"enabled"`
	InstalledAt string `json:"installed_at"`
}
