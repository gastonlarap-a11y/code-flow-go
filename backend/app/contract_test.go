package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

/*
The command-coverage contract, and the port's progress meter.

The authoritative list of commands is not the specification and not this repository's Go code: it
is what the renderer actually calls. 246 distinct names across three files, each reached through a
typed wrapper. A command the renderer calls and Go does not register answers
`unknown command '<name>'` at runtime — a dead button with no build-time warning anywhere, which is
precisely the failure a 36 000-line rewrite is most likely to produce.

So the list is re-derived from the renderer's source on every run, and compared against the real
registry. Three outcomes are possible and each means something different:

  - registered and called      — ported, nothing to do
  - called, not registered     — either still to port (notYetPorted) or deliberately deferred (deferred)
  - registered, never called   — dead code, or a name that drifted from the renderer's spelling

`notYetPorted` shrinks by one phase's worth of names at a time. When it is empty and only the
eleven deferred names remain, the port's command surface is complete.
*/

// invokeCall matches `invoke<Result>("command_name"` across a line break, which is how the
// formatter wraps the longer wrappers.
var invokeCall = regexp.MustCompile(`invoke<[^(]*\(\s*"([a-z_0-9]+)"`)

// rendererCommandFiles are the three files that hold every call site.
var rendererCommandFiles = []string{
	filepath.Join("..", "..", "frontend", "src", "lib", "ipc", "commands.ts"),
	filepath.Join("..", "..", "frontend", "src", "lib", "ipc", "apiCommands.ts"),
	filepath.Join("..", "..", "frontend", "src", "lib", "bridge", "updater.ts"),
}

// deferred are the eleven names the renderer calls on purpose and the backend deliberately does
// not answer. The debugger (12-debugging.md) and gRPC were never implemented in 2.x either; the
// renderer handles the refusal, so registering them would be the change, not leaving them out.
var deferred = []string{
	"api_grpc_call", "api_grpc_describe",
	"debug_continue", "debug_evaluate", "debug_pause", "debug_properties",
	"debug_set_breakpoints", "debug_start", "debug_start_adapter", "debug_step", "debug_stop",
}

// notYetPorted is the work remaining: 234 commands the renderer calls today and this backend does
// not answer yet. 234 + 11 deferred + 1 ported = 246.
//
// The names are transcribed from the renderer, never from the specification — the two do not
// always agree on spelling (the renderer says `git_clone`, the spec's prose says "clone"), and the
// renderer is the side that does the calling. The phase labels are indicative and exist so each
// phase can see its own remaining surface; moving a name between phases is free, inventing one is
// what this whole file exists to prevent.
var notYetPorted = map[string][]string{
	// Phase 2 is complete: storage and its migrations, credentials, workspaces, projects,
	// settings, prompts, review contexts, agents, MCP servers, skills, chat and job history, and
	// the review-run store. Its block is empty and stays here as the marker that it is done.
	"phase 2": {},
	// Phase 3 — git, files, watcher, secret scanner, terminal.
	//
	// Phase 3 is complete: git in full, the file operations, the palette's file list, search and
	// replace, the watcher, the secret gate and the terminal. Its block is empty and stays as the
	// marker that it is done.
	//
	// `repo_web_url` moved to phase 5 rather than being ported here. It looked like a file command
	// and is not: it rebuilds a repository's home page from its live remote, which needs the
	// GitHub Enterprise host allowlist and the Azure org/project parsing that 06-providers.md owns.
	"phase 3": {},
	// Phase 4 — AI engines and the run lifecycle. The chat and job-history *stores* came early,
	// with activity in Phase 2; what is left here is the engines that fill them.
	"phase 4": {
		"cancel_ai_run", "check_ai_provider", "default_analyze_template", "default_commit_template",
		"default_pr_description_template", "default_resolve_conflict_template",
		"default_review_template", "generate_commit_message", "generate_pr_description",
		"inline_edit_with_ai", "list_ai_models", "resolve_conflict_with_ai",
		"resolve_finding_with_ai", "send_chat_message",
	},
	// Phase 5 — providers, PR review pipeline, work items
	"phase 5": {
		"act_on_pr_link", "act_on_pull_request", "ado_list_projects", "ado_list_repos",
		"auto_link_project", "comment_ticket", "create_pull_request", "get_ticket",
		"get_ticket_criteria", "github_authenticated_user", "link_branch_ticket", "link_project_ado",
		"link_project_github", "list_my_tickets", "list_pr_comment_threads", "list_pull_requests",
		"list_sprint_tickets", "list_ticket_reviews", "list_tickets", "post_pr_link_review_comment",
		"post_pr_review_comment", "pr_link_comment_threads", "pr_link_decision", "pr_link_pull_request",
		"pr_review_decision", "preview_ticket", "repo_web_url", "resolve_pr_link", "resolve_ticket_account",
		"resolve_ticket_link", "review_changes", "review_pr_from_link", "review_pull_request",
		"suggest_ticket_for_branch", "sync_ticket", "ticket_for_branch", "unlink_branch_ticket",
		"unlink_project", "update_workspace_ticket_account",
	},
	// Phase 6 — API client (HTTP, GraphQL, WebSocket, Socket.IO, MQTT) and its stores
	"phase 6": {
		"api_add_history", "api_cancel_http", "api_clear_cookies", "api_clear_history",
		"api_create_collection", "api_create_environment", "api_create_folder", "api_create_request",
		"api_delete_collection", "api_delete_cookie", "api_delete_environment", "api_delete_folder",
		"api_delete_history", "api_delete_request", "api_duplicate_collection",
		"api_duplicate_environment", "api_duplicate_request", "api_list_cookies",
		"api_list_environments", "api_list_history", "api_load_tree", "api_move_node",
		"api_mqtt_connect", "api_mqtt_publish", "api_mqtt_subscribe", "api_mqtt_unsubscribe",
		"api_read_file_base64", "api_read_text_file", "api_reorder_collections", "api_send_http",
		"api_send_http_tracked", "api_socketio_connect", "api_socketio_emit", "api_stream_disconnect",
		"api_update_collection", "api_update_environment", "api_update_folder", "api_update_request",
		"api_upsert_cookie", "api_ws_connect", "api_ws_send",
	},
	// Phase 7 — schema designer (DBML)
	"phase 7": {
		"dbml_assist", "dbml_clear_layout", "dbml_delete_connection", "dbml_introspect_database",
		"dbml_list_connections", "dbml_list_documents", "dbml_load_layout", "dbml_save_connection",
		"dbml_save_positions", "dbml_test_connection",
	},
	// Phase 8 — updater
	"phase 8": {
		"update_check", "update_current_version", "update_download",
	},
}

// fullRegistry builds the registry the running app would have.
//
// A real database rather than a nil one, because half the commands are only registered when
// storage opened — and a contract test that inspected the degraded registry would happily report
// that everything is accounted for while the app was missing a feature.
func fullRegistry(t *testing.T) *bridge.Registry {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return app.BuildRegistry(app.Deps{Paths: platform.NewPaths(t.TempDir()), DB: db})
}

func rendererCommands(t *testing.T) []string {
	t.Helper()

	seen := map[string]bool{}
	for _, path := range rendererCommandFiles {
		source, err := os.ReadFile(path)
		require.NoError(t, err, "the renderer is the authoritative command list; %s must exist", path)

		for _, match := range invokeCall.FindAllStringSubmatch(string(source), -1) {
			seen[match[1]] = true
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func pendingCommands() map[string]string {
	pending := map[string]string{}
	for phase, names := range notYetPorted {
		for _, name := range names {
			pending[name] = phase
		}
	}
	return pending
}

// The renderer's list is the contract. If this number moves, a command was added or removed on the
// renderer side and this test's expectations need looking at rather than silently adjusting.
func TestRendererCallsTheExpectedNumberOfCommands(t *testing.T) {
	assert.Len(t, rendererCommands(t), 246,
		"246 = 235 the 2.x core registered + the 11 it answered 'unknown command' for")
}

// The point of the whole file: nothing the renderer calls may be missing without being accounted
// for, as either deferred or scheduled.
func TestEveryRendererCommandIsRegisteredOrAccountedFor(t *testing.T) {
	registry := fullRegistry(t)
	pending := pendingCommands()

	var unaccounted []string
	for _, name := range rendererCommands(t) {
		if _, registered := registry.Lookup(name); registered {
			continue
		}
		if slices.Contains(deferred, name) {
			continue
		}
		if _, scheduled := pending[name]; scheduled {
			continue
		}
		unaccounted = append(unaccounted, name)
	}

	assert.Empty(t, unaccounted,
		"the renderer calls these and nothing answers them: add the handler, or list it in notYetPorted with its phase")
}

// The other direction. A registered command the renderer never calls is either dead weight or —
// far more likely during a port — a name that was transcribed with a typo, which would otherwise
// only show up as a dead button.
func TestEveryRegisteredCommandIsCalledByTheRenderer(t *testing.T) {
	registry := fullRegistry(t)
	called := rendererCommands(t)

	var orphaned []string
	for _, name := range registry.Names() {
		if !slices.Contains(called, name) {
			orphaned = append(orphaned, name)
		}
	}

	assert.Empty(t, orphaned, "registered but never called by the renderer — a typo, or dead code")
}

// A deferred command must stay unregistered: the renderer branches on the refusal.
func TestDeferredCommandsAreNotRegistered(t *testing.T) {
	registry := fullRegistry(t)

	for _, name := range deferred {
		_, registered := registry.Lookup(name)
		assert.False(t, registered, "%s is deferred (12-debugging.md / gRPC) and must answer 'unknown command'", name)
	}
	assert.Len(t, deferred, 11, "nine debug_* and two api_grpc_*")
}

// The arithmetic that makes the progress meter trustworthy: everything the renderer calls is in
// exactly one of the three buckets, and they add up.
func TestTheThreeBucketsAccountForEveryCommand(t *testing.T) {
	registry := fullRegistry(t)

	assert.Equal(t, 246, registry.Len()+len(deferred)+len(pendingCommands()),
		"registered + deferred + not yet ported must equal what the renderer calls")
}

// Housekeeping: an entry that has been ported but left in notYetPorted makes the progress meter
// lie, and the meter is the only thing tracking how much of the port is left.
func TestNotYetPortedDoesNotListSomethingAlreadyDone(t *testing.T) {
	registry := fullRegistry(t)

	for name, phase := range pendingCommands() {
		_, registered := registry.Lookup(name)
		assert.False(t, registered, "%s is registered but still listed under %s; remove it from notYetPorted", name, phase)
	}
}

// Phase 2's exit criterion, stated as a test: every feature it covers actually answers.
//
// One name per feature rather than all 61. The exhaustive list was worth keeping while there were
// ten; now the three tests above already catch drift in both directions — a command registered
// that the renderer never calls, and one still listed as pending — so restating every name here
// would only be a second place to update.
func TestPhaseTwoFeaturesAnswer(t *testing.T) {
	registry := fullRegistry(t)

	for feature, command := range map[string]string{
		"app lifecycle":     "reset_app_data",
		"credentials":       "has_github_token",
		"workspaces":        "list_workspaces",
		"projects":          "create_project",
		"settings":          "get_setting",
		"workspace prompts": "get_workspace_prompt",
		"review contexts":   "upsert_review_context",
		"agents":            "list_workspace_agents",
		"MCP servers":       "list_workspace_mcps",
		"skills":            "list_workspace_skills",
		"skill files":       "read_skill_file",
		"chat history":      "list_chat_conversations",
		"job history":       "list_job_history",
		"review runs":       "list_review_runs",
	} {
		t.Run(feature, func(t *testing.T) {
			_, registered := registry.Lookup(command)
			assert.True(t, registered, "%s is not registered", command)
		})
	}

	assert.True(t, registry.Sealed(), "the registry must be sealed before the window opens")
}

// Storage is the only stage that can fail and leave the app running, so what happens then is
// worth pinning in both directions.
//
// The storage-backed commands are simply not registered: the renderer gets `unknown command`
// rather than a nil dereference, and the banner tells the user why. Everything that needs no
// database keeps working — credentials, so "reconnect your account" is still offered, and all of
// git, because a broken install is exactly when someone is trying to get their work out.
func TestADegradedStartUpKeepsWhatNeedsNoDatabase(t *testing.T) {
	registry := app.BuildRegistry(app.Deps{Paths: platform.NewPaths(t.TempDir())})

	for _, name := range []string{
		"reset_app_data", "has_github_token", "set_ado_pat",
		"get_status", "get_working_diff", "list_branches", "commit", "stage_file",
	} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s needs no database and must survive a failed start-up", name)
	}

	for _, name := range []string{
		"list_workspaces", "get_setting", "list_chat_conversations", "list_review_runs",
		"list_workspace_skills",
	} {
		_, registered := registry.Lookup(name)
		assert.False(t, registered, "%s needs the database and must not be registered without one", name)
	}
}
