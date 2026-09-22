-- The schema batch. Run in full on every launch; `IF NOT EXISTS` on every statement is the entire
-- mechanism that makes that safe (STORE-002 — there is no version table anywhere).
--
-- Transcribed from `docs/business-rules/03-storage.md`, which transcribes it from 2.x's Schema.cs,
-- comments included. Two tables (`dbml_layouts`, `db_connections`) and three indexes are not in
-- that document — `15-dbml.md` names them but never carries their DDL — so they were read from a
-- real 2.7.1 database instead, which is also what fixed the count: 23 tables and 10 indexes, not
-- the 22/10 the document claims. See `03-storage.md` § Schema, corrected in the same change.
--
-- Column order differs from an upgraded 2.7.x database for `workspaces`, `projects`,
-- `activity_log`, `job_history`, `workspace_skills` and `workspace_agents`: there the columns
-- listed here inline arrived later as `ALTER TABLE ADD COLUMN`, so SQLite appended them. Only the
-- order differs — every column, type and default matches, and SQLite addresses columns by name.

CREATE TABLE IF NOT EXISTS workspaces (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    icon        TEXT NOT NULL DEFAULT 'folder',
    color       TEXT NOT NULL DEFAULT '#6366f1',
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    -- Commit-identity override, both null = use the global identity (WS-008). Added by
    -- AddGitIdentityToWorkspaces for pre-existing databases.
    git_name    TEXT,
    git_email   TEXT,
    -- Which Azure DevOps organisation and board project this workspace's tickets come from; null =
    -- not chosen, and the resolution falls through to the project's own link (WI-005). The project
    -- needs its own column because a repository hosted on GitHub has no projects.ado_project at
    -- all. Both added by AddAdoOrgToWorkspaces for pre-existing databases.
    ado_org     TEXT,
    ado_project TEXT
);

CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    local_path  TEXT NOT NULL,
    remote_url  TEXT,
    color       TEXT NOT NULL DEFAULT '#6366f1',
    icon        TEXT NOT NULL DEFAULT 'git-branch',
    ado_org      TEXT,
    ado_project  TEXT,
    ado_repo_id  TEXT,
    github_owner TEXT,
    github_repo  TEXT,
    github_host  TEXT,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL
);

-- Review context is scoped per WORKSPACE (see migrate_review_contexts_to_workspace
-- below for the project_id -> workspace_id column migration for pre-existing rows).
CREATE TABLE IF NOT EXISTS review_contexts (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    content      TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL
);

-- Per-workspace, provider-independent prompt overrides keyed by `kind`
-- (`review_standard` = the PR review methodology, `pr_description` = the PR-description
-- generator). One row per (workspace, kind), seeded with the built-in default on creation
-- and backfilled for pre-existing workspaces (see backfill_workspace_prompts). Empty
-- content means "use the built-in default", so resetting is just a blank save. These are
-- deliberately NOT per-provider — the same text applies to whatever engine a task routes to.
CREATE TABLE IF NOT EXISTS workspace_prompts (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    content      TEXT NOT NULL DEFAULT '',
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (workspace_id, kind)
);

-- Durable memory of every completed PR review — one row per run, kept in the DB (not on
-- disk) so it moves/backs up with codeflow.db. Holds the rendered review, the exact diff
-- reviewed, run metadata and the parsed findings (JSON), which is what a re-review reads
-- back to reconcile new/still-present/resolved. Timestamped rows, never overwritten, so the
-- code a finding referred to stays recoverable even after the branch is gone.
CREATE TABLE IF NOT EXISTS review_runs (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL,
    pr_id        INTEGER NOT NULL,
    iter         INTEGER NOT NULL,
    level        TEXT NOT NULL,
    meta         TEXT NOT NULL DEFAULT '{}',
    review_md    TEXT NOT NULL,
    diff         TEXT NOT NULL DEFAULT '',
    findings     TEXT NOT NULL DEFAULT '[]',
    created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_review_runs_pr ON review_runs (project_id, pr_id, created_at);

-- Skills installed via `npx skills add`, scoped per workspace; synced into whichever
-- project is actually being reviewed at review time (Claude Code only discovers
-- skills from a project's own .claude/skills, there's no cross-directory flag for it).
CREATE TABLE IF NOT EXISTS workspace_skills (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    skill_name   TEXT NOT NULL,
    source_repo  TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    installed_at TEXT NOT NULL
);

-- User-defined SDD/Harness agents (roles) per workspace — name + role + model + prompt.
-- Deliberately empty by default (no preset roster); the user creates their own.
CREATE TABLE IF NOT EXISTS workspace_agents (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL DEFAULT '',
    model        TEXT NOT NULL DEFAULT '',
    prompt       TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL
);

-- MCP servers configured per workspace; written out as a --mcp-config JSON file for
-- headless `claude -p` invocations against any project in the workspace.
CREATE TABLE IF NOT EXISTS workspace_mcps (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    command      TEXT NOT NULL,
    args         TEXT NOT NULL DEFAULT '',
    env          TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Persisted record of every AI chat question/answer turn, scoped per project — the
-- chat itself (chatStore) only lives in memory for the session, so without this a
-- restart silently loses everything that was ever asked. `session_id` is the Claude
-- Code session these turns can be `--resume`d under; rows sharing one `session_id`
-- reconstruct a full conversation, letting the UI list/reopen/continue past chats
-- instead of only ever having one ongoing conversation per project.
CREATE TABLE IF NOT EXISTS activity_log (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    session_id  TEXT,
    question    TEXT NOT NULL,
    answer      TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_activity_log_project ON activity_log (project_id, created_at);

-- Persisted record of every finished PR review / pre-commit analysis run — like
-- `activity_log` above, `jobsStore` on the frontend only lives in memory for the
-- session, so without this a restart silently loses every past review/analysis
-- result. Only successful/errored *completed* runs are recorded (there's nothing
-- meaningful to reopen from a run that was still in flight when the app closed).
CREATE TABLE IF NOT EXISTS job_history (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    label        TEXT NOT NULL,
    custom_label TEXT,
    status       TEXT NOT NULL,
    result       TEXT,
    error        TEXT,
    meta         TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_history_project ON job_history (project_id, created_at);

-- A user-given rename for a chat conversation (`activity_log` rows grouped by
-- `session_id`) — conversations don't otherwise have a row of their own to attach a
-- title to, since they're just a GROUP BY over individual question/answer turns.
CREATE TABLE IF NOT EXISTS conversation_titles (
    session_id  TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- ===================== API client (per workspace) =====================
-- Scoped to a WORKSPACE, not to a project: a collection describes a *service*, and the
-- several repos of one workspace (frontend, backend, infra) normally talk to the same
-- one — scoping per repo would mean re-creating the same collection in each. Scoping per
-- workspace also keeps environments and the cookie jar from leaking a staging session
-- from one client's workspace into another's.
--
-- Only the roots carry `workspace_id`: folders and requests reach it through their
-- collection, so there is exactly one place a row's workspace can be wrong.
--
-- The editable content of a request lives in one `spec` JSON blob rather than in
-- columns, so adding a protocol, an auth scheme or a body mode never needs a migration.

CREATE TABLE IF NOT EXISTS api_collections (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT '' REFERENCES workspaces(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- JSON AuthConfig; '' = nothing configured (children fall through to "none").
    auth        TEXT NOT NULL DEFAULT '',
    pre_script  TEXT NOT NULL DEFAULT '',
    post_script TEXT NOT NULL DEFAULT '',
    -- JSON ApiVariable[] — collection-scoped variables.
    variables   TEXT NOT NULL DEFAULT '[]',
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- Folders nest arbitrarily (`parent_id` self-references); NULL means "directly under the
-- collection". Kept as a separate table from requests so a folder can carry its own
-- auth/scripts, which requests inherit.
CREATE TABLE IF NOT EXISTS api_folders (
    id            TEXT PRIMARY KEY,
    collection_id TEXT NOT NULL REFERENCES api_collections(id) ON DELETE CASCADE,
    parent_id     TEXT REFERENCES api_folders(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    auth          TEXT NOT NULL DEFAULT '',
    pre_script    TEXT NOT NULL DEFAULT '',
    post_script   TEXT NOT NULL DEFAULT '',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_api_folders_parent
    ON api_folders (collection_id, parent_id, sort_order);

CREATE TABLE IF NOT EXISTS api_requests (
    id            TEXT PRIMARY KEY,
    collection_id TEXT NOT NULL REFERENCES api_collections(id) ON DELETE CASCADE,
    folder_id     TEXT REFERENCES api_folders(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    -- http | graphql | websocket | socketio | grpc | mqtt
    protocol      TEXT NOT NULL DEFAULT 'http',
    -- Denormalized out of `spec` purely so the tree can render method+URL without
    -- parsing every blob.
    method        TEXT NOT NULL DEFAULT 'GET',
    url           TEXT NOT NULL DEFAULT '',
    -- JSON ApiRequestSpec: params, headers, body, auth, scripts, protocol settings.
    spec          TEXT NOT NULL DEFAULT '{}',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_api_requests_parent
    ON api_requests (collection_id, folder_id, sort_order);

-- Environments are global too. Exactly one row has `is_global = 1`: the "Globals"
-- pseudo-environment, which is always in scope and can't be deleted or switched away
-- from (see `ensure_globals_environment`).
CREATE TABLE IF NOT EXISTS api_environments (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT '' REFERENCES workspaces(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    -- JSON ApiVariable[] — initial vs current value, secret flag, enabled flag.
    variables   TEXT NOT NULL DEFAULT '[]',
    is_global   INTEGER NOT NULL DEFAULT 0,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);

-- Every send, whether or not it came from a saved request (`request_id` is NULL for
-- ad-hoc sends). `snapshot` holds the full request spec + response so an old entry can
-- be replayed or restored into the builder exactly as it was.
CREATE TABLE IF NOT EXISTS api_history (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT '' REFERENCES workspaces(id) ON DELETE CASCADE,
    request_id  TEXT,
    name        TEXT NOT NULL DEFAULT '',
    protocol    TEXT NOT NULL DEFAULT 'http',
    method      TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT '',
    status      INTEGER,
    duration_ms INTEGER,
    size_bytes  INTEGER,
    snapshot    TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_api_history_time ON api_history (workspace_id, created_at DESC);

-- The cookie jar. Persisted rather than kept in the reqwest client because the client is
-- rebuilt per request (per-request SSL/proxy/redirect overrides make a shared client
-- impossible), so nothing in the transport layer can hold jar state across sends.
CREATE TABLE IF NOT EXISTS api_cookies (
    id         TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT '' REFERENCES workspaces(id) ON DELETE CASCADE,
    domain     TEXT NOT NULL,
    path       TEXT NOT NULL DEFAULT '/',
    name       TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    secure     INTEGER NOT NULL DEFAULT 0,
    http_only  INTEGER NOT NULL DEFAULT 0,
    expires    TEXT,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_cookies_key
    ON api_cookies (workspace_id, domain, path, name);

CREATE TABLE IF NOT EXISTS tickets (
    id             TEXT PRIMARY KEY,           -- {provider}:{org}:{project}:{external_id}
    provider       TEXT NOT NULL DEFAULT 'azure',
    org            TEXT NOT NULL,
    project        TEXT NOT NULL,
    external_id    TEXT NOT NULL,              -- TEXT: Azure numbers work items, Jira names them
    title          TEXT NOT NULL,
    state          TEXT NOT NULL,
    work_item_type TEXT NOT NULL,
    assigned_to    TEXT,
    web_url        TEXT NOT NULL,
    rev            INTEGER NOT NULL DEFAULT 0,
    raw_json       TEXT NOT NULL DEFAULT '{}', -- what the mirror is rewritten from, with no fetch
    mirror_path    TEXT NOT NULL,
    synced_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_identity
    ON tickets (provider, org, project, external_id);

CREATE TABLE IF NOT EXISTS ticket_links (
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    branch      TEXT NOT NULL,
    ticket_id   TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    linked_at   TEXT NOT NULL,
    PRIMARY KEY (project_id, branch)
);

CREATE TABLE IF NOT EXISTS ticket_review_runs (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    workspace_id     TEXT NOT NULL,
    ticket_id        TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    branch           TEXT NOT NULL,
    base_ref         TEXT NOT NULL,
    head_sha         TEXT NOT NULL,
    level            TEXT NOT NULL,
    meta             TEXT NOT NULL DEFAULT '{}',
    review_md        TEXT NOT NULL,
    diff             TEXT NOT NULL DEFAULT '',
    findings         TEXT NOT NULL DEFAULT '[]',  -- same JSON shape as review_runs.findings
    criteria         TEXT NOT NULL DEFAULT '[]',
    coverage_verdict TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ticket_review_runs_branch
    ON ticket_review_runs (project_id, branch, created_at);

-- ===================== Schema designer (DBML) =====================
-- Read from a real 2.7.1 database rather than from `15-dbml.md`, which names these two tables
-- (DBML-005) but never carries their DDL.

-- Card positions per document, keyed by (project, file, table name). Outlive the tables they
-- place: a position survives its table being renamed away and coming back (DBML-005).
CREATE TABLE IF NOT EXISTS dbml_layouts (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    rel_path   TEXT NOT NULL,
    table_key  TEXT NOT NULL,
    x          REAL NOT NULL,
    y          REAL NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_dbml_layouts_key
    ON dbml_layouts (project_id, rel_path, table_key);

-- Saved database connections for introspection. No password column, deliberately: the secret
-- goes to the OS credential store under `db-password:{id}` (DBML-024, SEC-014).
CREATE TABLE IF NOT EXISTS db_connections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    driver     TEXT NOT NULL,
    host       TEXT,
    port       INTEGER,
    database   TEXT,
    username   TEXT,
    file_path  TEXT,
    use_tls    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- The AI usage ceilings this app has watched the user reach (USAGE-006).
--
-- No provider publishes the token limit of a subscription window, so the indicator never assumes
-- one: it records what the window held the moment a provider answered "limit reached", and divides
-- by that from then on. One row per provider, plan and window; the value only ever moves up,
-- because running out early proves the window held at least that much and a later, larger number
-- is the better reading of the same limit.
CREATE TABLE IF NOT EXISTS ai_usage_ceiling (
    provider        TEXT NOT NULL,
    plan            TEXT NOT NULL,
    window_kind     TEXT NOT NULL,
    observed_tokens INTEGER NOT NULL,
    observed_at     TEXT NOT NULL,
    PRIMARY KEY (provider, plan, window_kind)
);
