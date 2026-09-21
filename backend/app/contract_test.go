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
The command-coverage contract.

The authoritative list of commands is not the specification and not this repository's Go code: it
is what the renderer actually calls, each name reached through a typed wrapper. A command the
renderer calls and Go does not register answers `unknown command '<name>'` at runtime — a dead
button with no build-time warning anywhere, which is precisely the failure a 36 000-line rewrite is
most likely to produce.

So the list is re-derived from the renderer's source on every run, and compared against the real
registry. Three outcomes are possible and each means something different:

  - registered and called      — nothing to do
  - called, not registered     — either deliberately deferred (deferred) or a gap
  - registered, never called   — dead code, or a name that drifted from the renderer's spelling

This file was the port's progress meter, and `notYetPorted` shrank by one phase's worth of names at
a time until it was empty. **That job is finished**: 235 registered plus the eleven deferred
account for the 246 the 2.x renderer called, and the port shipped. What the tests guard now is
drift in both directions, and one thing more — a command this repository invents.

`portedCommandCount` is closed history and never moves. A feature written here rather than ported
puts its name in `newSincePort`, and the expected totals move by exactly that much. The arithmetic
is not the point: the point is that growing the command surface is a line somebody wrote down
rather than a number that drifted.
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

// portedCommandCount is what the renderer called when the port finished: the 235 the 2.x core
// registered plus the 11 it answered `unknown command` for. It is a fact about a finished piece of
// work, so it never moves — everything this repository adds afterwards is counted separately.
const portedCommandCount = 246

// newSincePort are the commands this repository grew after the port: features the 2.x core never
// had, so there is no C# original to compare them against and nothing to defer. A name belongs
// here from the moment the renderer calls it, and its handler is registered like any other.
var newSincePort = []string{
	// The diagram editor (16-diagrams.md). One command, because a diagram is a file in the user's
	// folder and everything except walking for it is a file command that already existed.
	"diagram_list_documents",
}

// deferred are the eleven names the renderer calls on purpose and the backend deliberately does
// not answer. The debugger (12-debugging.md) and gRPC were never implemented in 2.x either; the
// renderer handles the refusal, so registering them would be the change, not leaving them out.
var deferred = []string{
	"api_grpc_call", "api_grpc_describe",
	"debug_continue", "debug_evaluate", "debug_pause", "debug_properties",
	"debug_set_breakpoints", "debug_start", "debug_start_adapter", "debug_step", "debug_stop",
}

// notYetPorted was the work remaining: the commands the renderer called and this backend did not
// answer yet. Registered + deferred + pending is asserted below to equal what the renderer calls,
// which is what kept this list honest while the count moved.
//
// **It is empty, and kept as the record of how the port was tracked** — the phase blocks below say
// what each one covered. A gap found from here is not a phase's leftover, so it does not belong in
// this map; it is a missing handler, and the test that finds it says so.
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
	//
	// Phase 4 is complete: the routing cascade, binary discovery, the quota and auth signals, the
	// built-in templates, the Settings queries, the run lifecycle, all six engines, and every
	// operation including the chat turn and the pull-request description. Its block is empty and
	// stays as the marker that it is done.
	"phase 4": {},
	// Phase 5 — providers, PR review pipeline, work items.
	//
	// Phase 5 is complete: both provider clients, everything that reads or writes a pull request
	// through them, the review memory, running and publishing a review, and the work items —
	// accounts, the cache, the mirror, the criteria, both halves of `review_changes` and the one
	// command that writes to a board. Its block is empty and stays as the marker that it is done.
	"phase 5": {},
	// Phase 6 — API client (HTTP, GraphQL, WebSocket, Socket.IO, MQTT) and its stores.
	//
	// Phase 6 is complete: everything the workbench stores, the two file readers, the HTTP send
	// with its Digest handshake and SigV4 signing, and all three streaming transports behind one
	// connection registry. Its block is empty and stays as the marker that it is done.
	"phase 6": {},
	// Phase 7 is complete: the document walk, the layout store, the connection store with its
	// credential ordering, the assistant, and the four introspectors behind one snapshot builder.
	// Its block is empty and stays as the marker that it is done.
	"phase 7": {},
	// Phase 8 is complete: the three updater commands — the running version, the feed check with
	// its five reasons, and the download that verifies an artefact against the digest the release
	// published before handing it to the operating system. Its block is empty and stays as the
	// marker that it is done.
	//
	// With it, `notYetPorted` emptied: every command the 2.x renderer called is either registered
	// or one of the eleven deferred on purpose. Phase 9 — the parity audit, the differential
	// oracle and the cutover — added no command, and the port shipped. The map stays empty.
	"phase 8": {},
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
// renderer side and this test's expectations need looking at rather than silently adjusting: a new
// feature earns a line in `newSincePort`, and a deleted wrapper has to be explained.
func TestRendererCallsTheExpectedNumberOfCommands(t *testing.T) {
	assert.Len(t, rendererCommands(t), portedCommandCount+len(newSincePort),
		"%d ported + %d added since the port", portedCommandCount, len(newSincePort))
}

// `newSincePort` raises the expected total, so a name left in it after its wrapper was deleted
// would hide exactly the drift the count exists to catch. Both directions, for each entry.
func TestEveryCommandAddedSinceThePortIsRealInBothDirections(t *testing.T) {
	registry := fullRegistry(t)
	called := rendererCommands(t)

	for _, name := range newSincePort {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, called, name, "listed as added since the port, but no renderer wrapper calls it")
			_, registered := registry.Lookup(name)
			assert.True(t, registered, "listed as added since the port, but nothing registers it")
		})
	}
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

// The arithmetic that makes the count trustworthy: everything the renderer calls is in exactly one
// of the three buckets, and they add up to what the two lists above say they should.
func TestTheThreeBucketsAccountForEveryCommand(t *testing.T) {
	registry := fullRegistry(t)

	assert.Equal(t, portedCommandCount+len(newSincePort), registry.Len()+len(deferred)+len(pendingCommands()),
		"registered + deferred + not yet ported must equal what the renderer calls")
}

// Housekeeping, kept now that the map is empty: an entry both registered and listed as pending
// would subtract from the bucket arithmetic twice, so the totals above would agree while a real
// gap sat behind them.
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
