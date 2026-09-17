package workspaces_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock pins every timestamp so assertions can compare values rather than shapes.
var fixedClock = storage.FixedClock{At: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}

func newStore(t *testing.T) *workspaces.Store {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return workspaces.NewStore(db, fixedClock)
}

func newService(t *testing.T) (*bridge.Service, *workspaces.Store) {
	t.Helper()
	store := newStore(t)
	r := bridge.NewRegistry()
	workspaces.Register(r, workspaces.Deps{Store: store, Paths: platform.NewPaths(t.TempDir())})
	r.Seal()
	return bridge.NewService(r, nil), store
}

func call(t *testing.T, svc *bridge.Service, method, params string) json.RawMessage {
	t.Helper()
	out, err := svc.Invoke(t.Context(), method, json.RawMessage(params))
	require.NoError(t, err, method)
	return out
}

func seedWorkspace(t *testing.T, store *workspaces.Store) workspaces.Workspace {
	t.Helper()
	w, err := store.CreateWorkspace(t.Context(), "Work", "folder", "#6366f1", nil)
	require.NoError(t, err)
	return w
}

// ---- the wire shape --------------------------------------------------------------------------

// The single most likely defect of this port: a nil slice marshals as `null`, the renderer maps
// over it without guarding, and the sidebar crashes — on an empty install, which is precisely the
// state nobody clicks through by hand.
func TestEmptyListsAreArraysNotNull(t *testing.T) {
	svc, store := newService(t)
	w := seedWorkspace(t, store)
	scoped := `{"workspaceId":"` + w.ID + `"}`

	for method, params := range map[string]string{
		"list_workspaces":       `{}`,
		"list_projects":         scoped,
		"list_review_contexts":  scoped,
		"list_workspace_agents": scoped,
		"list_workspace_mcps":   scoped,
	} {
		t.Run(method, func(t *testing.T) {
			out := call(t, svc, method, params)
			assert.NotEqual(t, "null", string(out), "a nil slice would crash the panel that maps it")
			assert.Equal(t, byte('['), out[0])
		})
	}
}

// Every response type is checked for a nil slice reachable from it, which is the mechanical
// version of the rule above.
func TestResponsesCarryNoNilSlices(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)

	list, err := store.ListWorkspaces(t.Context())
	require.NoError(t, err)
	assert.NoError(t, jsonwire.AssertNoNilSlices(list))

	projects, err := store.ListProjects(t.Context(), w.ID)
	require.NoError(t, err)
	assert.NoError(t, jsonwire.AssertNoNilSlices(projects))
}

// The renderer's types/domain.ts declares these field names by hand and reads them literally. A
// field renamed on this side compiles and arrives as `undefined`, which renders as a blank row.
func TestWorkspaceFieldNamesMatchTheRenderer(t *testing.T) {
	svc, _ := newService(t)

	out := call(t, svc, "create_workspace", `{"name":"Work","icon":"folder","color":"#ff0000"}`)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &fields))
	for _, name := range []string{
		"id", "name", "icon", "color", "sort_order", "created_at",
		"git_name", "git_email", "ado_org", "ado_project",
	} {
		assert.Contains(t, fields, name)
	}
	assert.Len(t, fields, 10, "an extra field is one the renderer's type does not declare")
}

// Nullable columns must arrive as `null`, never omitted: the renderer types them as `T | null` and
// branches on the null, so an absent key is not the same thing to it.
func TestNullableFieldsArriveAsNullRatherThanOmitted(t *testing.T) {
	svc, _ := newService(t)

	out := call(t, svc, "create_workspace", `{"name":"Work"}`)

	assert.Contains(t, string(out), `"git_name":null`)
	assert.Contains(t, string(out), `"ado_org":null`)
}

func TestProjectFieldNamesMatchTheRenderer(t *testing.T) {
	svc, store := newService(t)
	w := seedWorkspace(t, store)

	out := call(t, svc, "create_project", `{"input":{
		"workspace_id":"`+w.ID+`","name":"Repo","local_path":"/tmp/repo","remote_url":null,
		"icon":"git-branch","ado_org":null,"ado_project":null,"ado_repo_id":null,
		"github_owner":"gastonlarap-a11y","github_repo":"code-flow","github_host":null}}`)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &fields))
	for _, name := range []string{
		"id", "workspace_id", "name", "local_path", "remote_url", "color", "icon",
		"ado_org", "ado_project", "ado_repo_id", "github_owner", "github_repo", "github_host",
		"sort_order", "created_at",
	} {
		assert.Contains(t, fields, name)
	}
	assert.Len(t, fields, 15)
}

// create_project is the one command taking a whole record, and its fields arrive in snake_case
// while every other command's arguments are camelCase. That inconsistency is 2.x's and is kept.
func TestCreateProjectReadsSnakeCaseInput(t *testing.T) {
	svc, store := newService(t)
	w := seedWorkspace(t, store)

	out := call(t, svc, "create_project", `{"input":{
		"workspace_id":"`+w.ID+`","name":"Repo","local_path":"/tmp/repo","remote_url":"https://x/y.git",
		"icon":"git-branch","color":"#00ff00","ado_org":null,"ado_project":null,"ado_repo_id":null,
		"github_owner":null,"github_repo":null,"github_host":null}}`)

	var project workspaces.Project
	require.NoError(t, json.Unmarshal(out, &project))
	assert.Equal(t, w.ID, project.WorkspaceID)
	assert.Equal(t, "/tmp/repo", project.LocalPath)
	assert.Equal(t, "#00ff00", project.Color)
	require.NotNil(t, project.RemoteURL)
	assert.Equal(t, "https://x/y.git", *project.RemoteURL)
}

// ---- behaviour -------------------------------------------------------------------------------

func TestCreateWorkspaceSeedsItsGlobalsEnvironmentAndPrompts(t *testing.T) {
	store := newStore(t)

	w, err := store.CreateWorkspace(t.Context(), "Work", "folder", "#6366f1",
		map[string]string{"review_standard": "the standard", "pr_description": "the template"})
	require.NoError(t, err)

	got, err := store.GetWorkspacePrompt(t.Context(), w.ID, "review_standard")
	require.NoError(t, err)
	assert.Equal(t, "the standard", got)
	assert.Equal(t, fixedClock.Now(), w.CreatedAt)
}

// Sort order appends rather than restarting, or a new workspace would jump to the top of a list
// the user has already arranged.
func TestWorkspacesAppendToTheEnd(t *testing.T) {
	store := newStore(t)

	first, err := store.CreateWorkspace(t.Context(), "A", "folder", "#1", nil)
	require.NoError(t, err)
	second, err := store.CreateWorkspace(t.Context(), "B", "folder", "#2", nil)
	require.NoError(t, err)

	assert.Equal(t, int64(0), first.SortOrder)
	assert.Equal(t, int64(1), second.SortOrder)

	list, err := store.ListWorkspaces(t.Context())
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "A", list[0].Name)
}

// Both empty clears the override back to NULL: an empty string would be a configured identity
// with no name, which git would then use.
func TestClearingTheGitIdentityStoresNullRatherThanEmpty(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)
	name, email := "Gastón", "g@example.com"

	require.NoError(t, store.SetWorkspaceGitIdentity(t.Context(), w.ID, &name, &email))
	got, err := store.GetWorkspace(t.Context(), w.ID)
	require.NoError(t, err)
	require.NotNil(t, got.GitName)
	assert.Equal(t, "Gastón", *got.GitName)

	empty := ""
	require.NoError(t, store.SetWorkspaceGitIdentity(t.Context(), w.ID, &empty, &empty))
	got, err = store.GetWorkspace(t.Context(), w.ID)
	require.NoError(t, err)
	assert.Nil(t, got.GitName, "empty means 'use the global identity', which is stored as NULL")
	assert.Nil(t, got.GitEmail)
}

// The lookup git makes before every commit: which identity does this directory commit under?
func TestResolveGitIdentityFollowsTheProjectToItsWorkspace(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)
	name, email := "Gastón Lara P.", "gaston@example.com"
	require.NoError(t, store.SetWorkspaceGitIdentity(t.Context(), w.ID, &name, &email))

	_, err := store.CreateProject(t.Context(), workspaces.NewProject{
		WorkspaceID: w.ID, Name: "Repo", LocalPath: "/repos/thing", Icon: "git-branch",
	})
	require.NoError(t, err)

	gotName, gotEmail, err := store.ResolveGitIdentity(t.Context(), "/repos/thing")

	require.NoError(t, err)
	require.NotNil(t, gotName)
	assert.Equal(t, "Gastón Lara P.", *gotName)
	require.NotNil(t, gotEmail)
	assert.Equal(t, "gaston@example.com", *gotEmail)
}

// A repository no project owns is the normal case, not an error: most repositories a user opens
// are not registered, and committing in one has to keep working.
func TestResolveGitIdentityForAnUnregisteredPath(t *testing.T) {
	store := newStore(t)

	name, email, err := store.ResolveGitIdentity(t.Context(), "/somewhere/else")

	require.NoError(t, err)
	assert.Nil(t, name)
	assert.Nil(t, email)
}

// A workspace with no override resolves to nothing, which the git layer reads as "use the
// repository's own config" — not as an empty name.
func TestResolveGitIdentityWithNoOverride(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)
	_, err := store.CreateProject(t.Context(), workspaces.NewProject{
		WorkspaceID: w.ID, Name: "Repo", LocalPath: "/repos/plain", Icon: "git-branch",
	})
	require.NoError(t, err)

	name, email, err := store.ResolveGitIdentity(t.Context(), "/repos/plain")

	require.NoError(t, err)
	assert.Nil(t, name)
	assert.Nil(t, email)
}

// The cascade only works because the connection keeps foreign_keys on for every statement.
func TestDeletingAWorkspaceTakesItsProjectsAndContexts(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)

	_, err := store.CreateProject(t.Context(), workspaces.NewProject{
		WorkspaceID: w.ID, Name: "Repo", LocalPath: "/tmp/repo", Icon: "git-branch",
	})
	require.NoError(t, err)
	_, err = store.UpsertReviewContext(t.Context(), nil, w.ID, "Conventions", "text", true)
	require.NoError(t, err)

	require.NoError(t, store.DeleteWorkspace(t.Context(), w.ID))

	projects, err := store.ListProjects(t.Context(), w.ID)
	require.NoError(t, err)
	assert.Empty(t, projects)
	contexts, err := store.ListReviewContexts(t.Context(), w.ID)
	require.NoError(t, err)
	assert.Empty(t, contexts)
}

// A delete that matched nothing means the caller is working from a stale list, which the UI wants
// to know about rather than silently succeed on.
func TestActingOnSomethingAlreadyGoneSaysSo(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.Invoke(t.Context(), "delete_workspace", json.RawMessage(`{"id":"nope"}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace nope no longer exists")
}

// get_project answers `Project | null`, so a missing one is null rather than an error: the
// renderer asks for a project it may no longer have and branches on the null.
func TestGetProjectAnswersNullForAMissingOne(t *testing.T) {
	svc, _ := newService(t)

	out := call(t, svc, "get_project", `{"id":"nope"}`)

	assert.Equal(t, "null", string(out))
}

func TestSettingsRoundTripAndReportAbsenceAsNull(t *testing.T) {
	svc, _ := newService(t)

	assert.Equal(t, "null", string(call(t, svc, "get_setting", `{"key":"theme"}`)))

	call(t, svc, "set_setting", `{"key":"theme","value":"dark"}`)
	assert.Equal(t, `"dark"`, string(call(t, svc, "get_setting", `{"key":"theme"}`)))

	// An empty value is a value, not an absence.
	call(t, svc, "set_setting", `{"key":"theme","value":""}`)
	assert.Equal(t, `""`, string(call(t, svc, "get_setting", `{"key":"theme"}`)))
}

// The configured directory wins; the fallback is {base}/repos, which start-up already created.
func TestDefaultCloneDirPrefersTheSetting(t *testing.T) {
	store := newStore(t)
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "CodeFlow"))
	r := bridge.NewRegistry()
	workspaces.Register(r, workspaces.Deps{Store: store, Paths: paths})
	r.Seal()
	svc := bridge.NewService(r, nil)

	out := call(t, svc, "default_clone_dir", `{}`)
	var fallback string
	require.NoError(t, json.Unmarshal(out, &fallback))
	assert.Equal(t, paths.Repos(), fallback)

	call(t, svc, "set_setting", `{"key":"default_clone_dir","value":"/elsewhere"}`)
	out = call(t, svc, "default_clone_dir", `{}`)
	var configured string
	require.NoError(t, json.Unmarshal(out, &configured))
	assert.Equal(t, "/elsewhere", configured)
}

// An empty stored prompt means "use the built-in default", so the command resolves it rather than
// handing the renderer an empty string it would have to interpret.
func TestGetWorkspacePromptFallsBackToTheBuiltInDefault(t *testing.T) {
	svc, store := newService(t)
	w := seedWorkspace(t, store)

	out := call(t, svc, "get_workspace_prompt", `{"workspaceId":"`+w.ID+`","kind":"review_standard"}`)

	var content string
	require.NoError(t, json.Unmarshal(out, &content))
	assert.NotEmpty(t, content)

	defaultOut := call(t, svc, "default_workspace_prompt", `{"kind":"review_standard"}`)
	var builtIn string
	require.NoError(t, json.Unmarshal(defaultOut, &builtIn))
	assert.Equal(t, builtIn, content)
}

func TestSetWorkspacePromptWins(t *testing.T) {
	svc, store := newService(t)
	w := seedWorkspace(t, store)

	call(t, svc, "set_workspace_prompt", `{"workspaceId":"`+w.ID+`","kind":"review_standard","content":"mine"}`)
	out := call(t, svc, "get_workspace_prompt", `{"workspaceId":"`+w.ID+`","kind":"review_standard"}`)

	assert.Equal(t, `"mine"`, string(out))
}

// An unrecognised kind answers the review methodology rather than failing, and `sdd_stages`
// answers "" — its guide is static content in the renderer and was never persisted. Returning an
// error would put a red banner where the user expects an editor.
func TestUnknownPromptKindFallsBackRatherThanFailing(t *testing.T) {
	svc, _ := newService(t)

	standard := call(t, svc, "default_workspace_prompt", `{"kind":"review_standard"}`)
	unknown := call(t, svc, "default_workspace_prompt", `{"kind":"something-else"}`)
	assert.Equal(t, string(standard), string(unknown))

	assert.Equal(t, `""`, string(call(t, svc, "default_workspace_prompt", `{"kind":"sdd_stages"}`)))
}

// "Restore default" in the UI is a blank save, not a delete — and whitespace counts as blank.
func TestAWhitespaceOnlyPromptFallsBackToTheDefault(t *testing.T) {
	svc, store := newService(t)
	w := seedWorkspace(t, store)

	call(t, svc, "set_workspace_prompt", `{"workspaceId":"`+w.ID+`","kind":"review_standard","content":"   \n  "}`)

	out := call(t, svc, "get_workspace_prompt", `{"workspaceId":"`+w.ID+`","kind":"review_standard"}`)
	builtIn := call(t, svc, "default_workspace_prompt", `{"kind":"review_standard"}`)
	assert.Equal(t, string(builtIn), string(out))
}

// Upsert creates when id is absent and updates when it is present, returning the row as it now
// stands — which is what the renderer puts straight into its store.
func TestUpsertCreatesThenUpdates(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)

	created, err := store.UpsertReviewContext(t.Context(), nil, w.ID, "Conventions", "v1", true)
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "v1", created.Content)

	updated, err := store.UpsertReviewContext(t.Context(), &created.ID, w.ID, "Conventions", "v2", false)
	require.NoError(t, err)
	assert.Equal(t, created.ID, updated.ID, "an update must not mint a new id")
	assert.Equal(t, created.CreatedAt, updated.CreatedAt, "nor a new creation time")
	assert.False(t, updated.Enabled)

	list, err := store.ListReviewContexts(t.Context(), w.ID)
	require.NoError(t, err)
	require.Len(t, list, 1, "an update must not leave a second row behind")
	assert.Equal(t, "v2", list[0].Content)
}

// `enabled` is an INTEGER in SQLite and a real boolean in the renderer's type. Scanning it into
// anything other than a bool is how a checkbox ends up always ticked.
func TestEnabledSurvivesAsABoolean(t *testing.T) {
	store := newStore(t)
	w := seedWorkspace(t, store)

	_, err := store.UpsertAgent(t.Context(), nil, workspaces.Agent{
		WorkspaceID: w.ID, Name: "Reviewer", Enabled: false,
	})
	require.NoError(t, err)

	agents, err := store.ListAgents(t.Context(), w.ID)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.False(t, agents[0].Enabled)

	encoded, err := json.Marshal(agents[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"enabled":false`, "not 0")
}

func TestCommandsReportAMissingParameterByName(t *testing.T) {
	svc, _ := newService(t)

	for _, tc := range []struct{ method, params, missing string }{
		{"create_workspace", `{}`, "name"},
		{"list_projects", `{}`, "workspaceId"},
		{"get_setting", `{}`, "key"},
		{"set_setting", `{"key":"a"}`, "value"},
		{"rename_workspace", `{"id":"w1"}`, "name"},
		{"create_project", `{}`, "input"},
		{"upsert_review_context", `{"workspaceId":"w1"}`, "name"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), tc.method, json.RawMessage(tc.params))
			require.Error(t, err)
			assert.Equal(t, "missing required parameter '"+tc.missing+"'", err.Error())
		})
	}
}
