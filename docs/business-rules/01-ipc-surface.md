# 01 — IPC surface

The complete contract between the frontend and the backend: every command the frontend can
call, and every event the backend can push. This document owns the *surface*; what each
command actually does is described in the domain document linked from its section heading.

## Scope

- `src/CodeFlow.App/Ipc/CommandRegistry.cs` — the registry every feature contributes to

Each feature folder registers its own commands with an `Add…Commands(…)` extension method, and
`src/CodeFlow.App/Program.cs` calls them; the bootstrap sequence is owned by
`02-bootstrap-platform.md`. Those commands appear in the table below because the table is the
contract; their semantics do not.

## Reconciliation

Established by parsing the tree, not by reading it:

| Set | Count | Source |
|---|---|---|
| Registered on the registry | 235 | the `Register(r, deps)` calls in `backend/app/registry.go`; 2.x: the `Add…Commands(…)` methods `src/CodeFlow.App/Program.cs` called |
| Distinct commands invoked by the frontend | 246 | `frontend/src/lib/ipc/commands.ts`, `apiCommands.ts`, `lib/bridge/updater.ts` |
| Registered but never invoked | 0 | asserted by `TestEveryRegisteredCommandIsCalledByTheRenderer` |
| Invoked but not registered | 11 | the nine `debug_*` and `api_grpc_call` / `api_grpc_describe` — **`DEAD`** |
| Duplicate command names | 0 | the registry panics on one, at composition time |

**The eleven have no sidecar implementation at all**, which is not the same as a dead command:
each is a typed wrapper the frontend can call and that answers `unknown command`. They are the
two features whose backend never arrived — the debugger (`12-debugging.md`, whose `BUG-DBG-a`
describes routing between backends that do not exist) and the API client's gRPC protocol. Nothing
else registered is unreachable, and nothing else invoked is missing.

**Ten names in the tables below do not line up with what the renderer calls**, and the Go port
follows the renderer. Six rows are host surfaces rather than commands and four names had no row at
all; both lists, and why, are under *The 248 rows against the 246 names* below. The
command-coverage contract test (`backend/app/contract_test.go`) re-derives all 246 names from the
three wrapper files on every run, which is what makes the renderer the authority here rather than
this document.

(This paragraph said "three names" and named them until the Phase 9 sweep counted. Two of the three
were right; it had not noticed the three updater rows, the three other host surfaces, or that five
section headings disagreed with their own row counts.)

Three wrapper files, not two: the updater's three commands are called from
`frontend/src/lib/bridge/updater.ts`, which sits beside the shell bridges because that is where
1.7.2 put it, while the commands themselves are ordinary sidecar commands like any other. No
`invoke` from `frontend/src/lib/bridge/host.ts` is imported anywhere outside those three files.

Other frontend files do bypass this boundary, but for *non-command* shell APIs (window controls,
dialogs, opener, OS detection, webview drag-and-drop). They are inventoried in
`02-bootstrap-platform.md`, because their replacements live in the Electron shell rather than in
the C# core.

## Parameter conventions

- **Caller parameters** are what the frontend passes. the transport deserialises them from the
  `invoke` payload object; the frontend sends `camelCase` and the transport maps it to the the sidecar
  `snake_case` parameter name.
- **Injected** parameters are supplied by the shell, not by the caller: `State<Db>`,
  `State<ApiRegistry>`, `State<TerminalRegistry>`, `State<WatcherRegistry>`, `AppHandle`,
  `Window`. They are listed so the C# core knows which ambient dependencies each command
  needs, and are never part of the IPC payload.
- A return type of `T` reaches the frontend as a resolved promise of `T` or
  a rejected promise whose value is the `string`. **Those strings are not free text** —
  several are parsed by the frontend (see `13-cross-language-contracts.md`) and must be
  reproduced exactly.

## Events

Thirteen event names, 23 emit call sites, **19 distinct (name, producer) pairs**. Events
are broadcast globally — `app.emit(...)`; there is no `emit_to` anywhere in the tree, so
every window receives every event and filtering is the frontend's job (each payload
carries the id it belongs to).

| Event | Producer | Payload | Fires when |
|---|---|---|---|
| `ai:output` | `src/CodeFlow.App/Ai/AiRunRegistry.cs` | `{ runId, stream: "stdout" \| "stderr", line }` | Every line an AI CLI writes, as it writes it. The UI dims `stderr` because most CLIs use it for progress chatter, not failures. |
| `git:progress` | `src/CodeFlow.App/Git/GitNetwork.cs` (stdout) | `{ op, line }` | Each line of a streamed `git` network operation. |
| `git:progress` | `src/CodeFlow.App/Git/GitNetwork.cs` (stderr) | `{ op, line }` | Same shape; `git` writes progress to stderr. |
| `git:done` | `src/CodeFlow.App/Git/GitNetwork.cs` | `{ op, success, message }` | The streamed `git` process exited. `message` falls back to `git {op} exited with {status}` when both streams were empty. |
| `terminal:output` | `src/CodeFlow.App/Terminal/TerminalRegistry.cs` | `{ id, data }` | PTY bytes read, decoded lossily as UTF-8. |
| `terminal:exit` | `src/CodeFlow.App/Terminal/TerminalRegistry.cs` | `{ id }` | The PTY reader hit EOF. |
| `debug:paused` | not implemented (deferred) | `PausedEvent { reason, frames[] }` | DAP adapter reported a stop and the stack trace resolved. |
| `debug:paused` | not implemented (deferred) | `PausedEvent { reason, frames[] }` | V8 Inspector `Debugger.paused`. |
| `debug:resumed` | not implemented (deferred) | `()` | Execution continued. |
| `debug:resumed` | not implemented (deferred) | `()` | Execution continued. |
| `debug:output` | not implemented (deferred) | `OutputEvent { kind, text }` | Adapter `output` event. |
| `debug:output` | not implemented (deferred), `:422` | `OutputEvent { kind, text }` | Console API call, and raw process output. |
| `debug:terminated` | not implemented (deferred), `:297` | `()` | Adapter terminated, and reader loop ended. |
| `debug:terminated` | not implemented (deferred) | `()` | Inspector session ended. |
| `repo:fs-changed` | `src/CodeFlow.App/Files/RepoWatcher.cs` | `{ repoPath }` | The working tree changed, subject to the watcher's throttle. |
| `skills:progress` | `src/CodeFlow.App/Workspaces/SkillCommands.cs`, `:71` | `{ line }` | Each line of a skill install subprocess (both streams). |
| `api:stream-message` | `src/CodeFlow.App/ApiClient/WebSocketStream.cs` | `StreamMessage` | A frame arrived on a live WebSocket or Socket.IO connection. |
| `api:stream-message` | `src/CodeFlow.App/ApiClient/MqttConnection.cs` | `StreamMessage` | A message arrived on a subscribed MQTT topic. |
| `api:stream-status` | `src/CodeFlow.App/ApiClient/WebSocketStream.cs` | `StreamStatusEvent` | A WebSocket/Socket.IO connection changed state. |
| `api:stream-status` | `src/CodeFlow.App/ApiClient/MqttConnection.cs` | `StreamStatusEvent` | An MQTT connection changed state. |

The four `debug:*` names each have **two independent producers** with **one shared payload
type** — not implemented (deferred) imports `PausedEvent`, `OutputEvent`, `StackFrame` and `Variable` from
not implemented (deferred). The port must keep that single contract: a frontend consumer cannot tell,
and must not need to tell, which backend produced the event.

The two `api:*` names are the only ones referenced through constants
(`EVENT_STREAM_MESSAGE` / `EVENT_STREAM_STATUS`, `src/CodeFlow.App/ApiClient/ApiModels.cs`) rather than string
literals at the call site.

Every one of the 13 names has exactly one `listen` wrapper in `frontend/src/lib/ipc/events.ts`.

**A fourteenth name is emitted and is not in that file.** `update:progress`
(`{ downloaded, total, done }`, every 256 KiB plus a final `done`) is subscribed to inline by
`frontend/src/lib/bridge/updater.ts`, beside the three updater commands and for the same reason they
sit outside `commands.ts`: 1.7.2 put the updater there and the port left it where it was. It is
excluded from the count above because that count is about `events.ts`, and it is worth naming here
because in 2.x the Electron shell never forwarded it — the event was emitted, nothing carried it,
and the download bar stayed at 0 % until the command returned. In Wails it arrives
(`DIVERGENCE-BOOT-g`); the producer is `backend/update/download.go`.

## Commands

Grouped by the C# file that defined them, in registration order — **which is history, not
structure**. The Go port regrouped them, and the table below is where they actually live now. Each
`###` heading names both: the Go file that registers those commands today, and the 2.x file they
came from. The `<sub>` under each command name repeats that 2.x origin and is kept as provenance;
it is not where the code is.

`Injected` lists the shell-supplied dependencies of the 2.x core; it was never part of the payload
and in Go it is the feature package's `Deps` struct.

### Where the 247 are registered

Derived from the registry, not transcribed: every name below was found as a literal registration in
exactly one non-test file under `backend/`, with zero ambiguity, and the total reconciles with
`bridge.Registry.Len()` (`tools/inventory` and `backend/app/contract_test.go` both re-check it).

| Go file | Commands | Owning document |
|---|---:|---|
| `backend/git/commands.go` | 47 | `04-git.md` |
| `backend/apiclient/commands.go` | 29 | `08-api-client.md` |
| `backend/workspaces/commands.go` | 27 | `09-workspace-scoped.md` |
| `backend/providers/commands.go` | 18 | `06-providers.md`, `07-review-pipeline.md` |
| `backend/tickets/commands.go` | 17 | `14-work-items.md` |
| `backend/files/commands.go` | 15 | `11-files-search-terminal.md` |
| `backend/ai/commands.go` | 14 | `05-ai-engines.md` |
| `backend/workspaces/skills.go` | 10 | `09-workspace-scoped.md` |
| `backend/dbml/commands.go` | 10 | `15-dbml.md` |
| `backend/security/commands.go` | 10 | `10-security.md` |
| `backend/apiclient/streamcommands.go` | 9 | `08-api-client.md` |
| `backend/review/commands.go` | 7 | `07-review-pipeline.md` |
| `backend/activity/commands.go` | 7 | `09-workspace-scoped.md` |
| `backend/terminal/commands.go` | 4 | `11-files-search-terminal.md` |
| `backend/update/commands.go` | 3 | `02-bootstrap-platform.md` |
| `backend/apiclient/httpcommands.go` | 3 | `08-api-client.md` |
| `backend/review/commands_run.go` | 2 | `07-review-pipeline.md` |
| `backend/review/commands_publish.go` | 2 | `07-review-pipeline.md` |
| `backend/platform/commands.go` | 1 | `02-bootstrap-platform.md` |
| `backend/diagram/commands.go` | 1 | `16-diagrams.md` |
| **registered** | **236** | |
| deferred — never registered, answer `unknown command` | 11 | `12-debugging.md`, `08-api-client.md` |
| **called by the renderer** | **247** | |

**One of these did not come from 2.x.** `diagram_list_documents` is the first command this
repository added on its own, after the port shipped; there is no C# file under it, and its row
carries no `<sub>` provenance because there is none to carry. `backend/app/contract_test.go` keeps
the two apart in the same way: `portedCommandCount` is closed at 246 and `newSincePort` lists what
came after.

Four regroupings are worth naming, because they are the reason a reader looking for a command in
the file its heading names will not find it:

- **`ApiCommands.cs` (45) became three files.** The stores stayed together in `commands.go` (29);
  sending one HTTP request is `httpcommands.go` (3); the three streaming transports share one
  connection registry in `streamcommands.go` (9). The two gRPC names are deferred.
- **`ReviewCommands.cs` (22) became five.** Fifteen of them turned out to be *provider* calls —
  reading a pull request, listing its threads, acting on it — and live in
  `backend/providers/commands.go`, dispatched by host. Running a review is `commands_run.go` (2),
  publishing one is `commands_publish.go` (2), and `resolve_finding_with_ai` is an AI operation.
- **`WorkspaceCommands.cs`'s second block (21) split in two.** Fourteen are workspace state; the
  other seven are the review-run *history store*, which is storage rather than pipeline and landed
  in `backend/review/commands.go` with Phase 2.
- **`Checkpoints.cs` (3) merged into `backend/git/commands.go`.** A checkpoint is a git ref; there
  was no reason for it to be a second file.

### The 248 rows against the 246 names

The tables below hold **248 rows** and the renderer calls **246 names**. The difference is six rows
that are not sidecar commands and four names that have no row, and both halves are listed here
rather than left to be rediscovered:

**Six rows that the renderer never `invoke`s.** All six are *shell* surfaces — native dialogs, the
system browser, quitting — which in 2.x were routed through the sidecar and in Go are methods on
`desktop.HostService`, reached directly from `frontend/src/lib/bridge/host.ts`. They are kept in the
tables with their shape, marked **`HOST`**, because the shape is still the contract; they are simply
not in the registry and `contract_test.go` would fail if they were.

`quit_app` · `pick_folder` · `open_external_url` · `open_repo_in_browser` · `api_pick_file` ·
`api_save_file`

**Four names with no row**, now added below: `repo_web_url` (§Reconciliation already flagged it) and
the three updater commands, which are called from `lib/bridge/updater.ts` rather than `commands.ts`
and were never tabulated.

### `backend/platform/commands.go` — 1 command, + 1 `HOST` → [02-bootstrap-platform](02-bootstrap-platform.md)
<sub>2.x: `src/CodeFlow.App/Platform/AppCommands.cs`. `quit_app` is now `desktop.HostService`.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `quit_app` **`HOST`**<br><sub>`src/CodeFlow.App/Platform/AppCommands.cs` → `HostService.Quit`</sub> | — | `()` | AppHandle | `host.quit(reason)` — every exit names who asked (BOOT-036) |
| `reset_app_data`<br><sub>`src/CodeFlow.App/Platform/AppCommands.cs`</sub> | — | `Result&lt;(), string&gt;` | AppHandle | `resetAppData` |

### `backend/workspaces/commands.go` — 13 commands, + 1 `HOST` → [09-workspace-scoped](09-workspace-scoped.md)
<sub>2.x: `src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`, whose heading said 12 and listed 14. `pick_folder` is now `desktop.HostService`.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `pick_folder` **`HOST`**<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs` → `HostService.OpenDirectory`</sub> | — | `Option&lt;string&gt;` | AppHandle | `host.dialog().openDirectory()` |
| `default_clone_dir`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | — | `string` | — | `defaultCloneDir` |
| `create_workspace`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `name: string`<br>`icon: string`<br>`color: string` | `Result&lt;Workspace, string&gt;` | State | `createWorkspace` |
| `list_workspaces`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | — | `Result&lt;Vec&lt;Workspace&gt;, string&gt;` | State | `listWorkspaces` |
| `delete_workspace`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteWorkspace` |
| `rename_workspace`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string`<br>`name: string` | `Result&lt;(), string&gt;` | State | `renameWorkspace` |
| `update_workspace_color`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string`<br>`color: string` | `Result&lt;(), string&gt;` | State | `updateWorkspaceColor` |
| `update_workspace_git_identity`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string`<br>`name: Option&lt;string&gt;`<br>`email: Option&lt;string&gt;` | `Result&lt;(), string&gt;` | State | `updateWorkspaceGitIdentity` |
| `create_project`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `input: NewProject` | `Result&lt;Project, string&gt;` | State | `createProject` |
| `list_projects`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;Project&gt;, string&gt;` | State | `listProjects` |
| `get_project`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;Option&lt;Project&gt;, string&gt;` | State | `getProject` |
| `delete_project`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteProject` |
| `move_project_to_workspace`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string`<br>`workspace_id: string` | `Result&lt;(), string&gt;` | State | `moveProjectToWorkspace` |
| `update_project_color`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string`<br>`color: string` | `Result&lt;(), string&gt;` | State | `updateProjectColor` |

### `backend/git/commands.go` — 44 of its 47 → [04-git](04-git.md)
<sub>2.x: `src/CodeFlow.App/Git/GitCommands.cs`, whose heading said 42 and listed 44. The other three are the checkpoints below, merged into the same file.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `is_git_repo`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `path: string` | `Result&lt;bool, string&gt;` | — | `isGitRepo` |
| `get_status`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;repo.RepoStatusInfo, string&gt;` | — | `getStatus` |
| `list_commits`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`all_refs: bool`<br>`limit: int` | `Result&lt;Vec&lt;graph.CommitInfo&gt;, string&gt;` | — | `listCommits` |
| `list_unpushed_commits`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;graph.CommitInfo&gt;, string&gt;` | — | `listUnpushedCommits` |
| `list_branches`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;branch.BranchInfo&gt;, string&gt;` | — | `listBranches` |
| `create_branch`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`name: string`<br>`start_point: Option&lt;string&gt;` | `Result&lt;(), string&gt;` | — | `createBranch` |
| `delete_branch`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`name: string`<br>`is_remote: bool` | `Result&lt;(), string&gt;` | — | `deleteBranch` |
| `checkout_local_branch`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`name: string` | `Result&lt;(), string&gt;` | — | `checkoutLocalBranch` |
| `checkout_detached`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`refname: string` | `Result&lt;(), string&gt;` | — | `checkoutDetached` |
| `checkout_remote_tracking`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`remote_branch: string` | `Result&lt;string, string&gt;` | — | `checkoutRemoteTracking` |
| `list_stashes`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;stash.StashInfo&gt;, string&gt;` | — | `listStashes` |
| `stash_save`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`message: Option&lt;string&gt;`<br>`include_untracked: bool` | `Result&lt;(), string&gt;` | — | `stashSave` |
| `stash_apply`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`index: int` | `Result&lt;string, string&gt;`<br><sub>`"applied"` \| `"conflicts"` \| `"not_found"` \| `"uncommitted_changes"` \| `"unknown"`</sub> | — | `stashApply` |
| `stash_pop`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`index: int` | `Result&lt;string, string&gt;`<br><sub>same five values</sub> | — | `stashPop` |
| `stash_drop`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`index: int` | `Result&lt;(), string&gt;` | — | `stashDrop` |
| `rename_stash`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`index: int`<br>`new_message: string` | `Result&lt;(), string&gt;` | — | `renameStash` |
| `get_working_diff`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;`FileDiffInfo`&gt;, string&gt;` | — | `getWorkingDiff` |
| `get_staged_diff`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;`FileDiffInfo`&gt;, string&gt;` | — | `getStagedDiff` |
| `get_commit_diff`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`oid: string` | `Result&lt;Vec&lt;`FileDiffInfo`&gt;, string&gt;` | — | `getCommitDiff` |
| `list_commit_files`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`oid: string` | `Result&lt;Vec&lt;`CommitFileInfo`&gt;, string&gt;` | — | `listCommitFiles` |
| `get_commit_file_diff`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`oid: string`<br>`file_path: string`<br>`old_path: string?` | `Result&lt;Vec&lt;`FileDiffInfo`&gt;, string&gt;` | — | `getCommitFileDiff` |
| `stage_file`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`file_path: string` | `Result&lt;(), string&gt;` | — | `stageFile` |
| `stage_all`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | — | `stageAll` |
| `unstage_file`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`file_path: string` | `Result&lt;(), string&gt;` | — | `unstageFile` |
| `unstage_all`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | — | `unstageAll` |
| `discard_file_changes`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`file_path: string` | `Result&lt;(), string&gt;` | — | `discardFileChanges` |
| `discard_all_changes`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | — | `discardAllChanges` |
| `commit`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`message: string`<br>`author_name: Option&lt;string&gt;`<br>`author_email: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | — | `commitChanges` |
| `reset_to_commit`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`oid: string`<br>`mode: string` | `Result&lt;(), string&gt;` | — | `resetToCommit` |
| `list_remotes`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;remotes.RemoteInfo&gt;, string&gt;` | — | `listRemotes` |
| `set_remote_url`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`name: string`<br>`url: string` | `Result&lt;(), string&gt;` | — | `setRemoteUrl` |
| `get_git_identity`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | — | `Result&lt;identity.GitIdentity, string&gt;` | — | `getGitIdentity` |
| `set_git_identity`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `name: string`<br>`email: string` | `Result&lt;(), string&gt;` | — | `setGitIdentity` |
| `merge_branch`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`branch_name: string` | `Result&lt;merge.MergeOutcome, string&gt;` | — | `mergeBranch` |
| `is_merging`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;bool, string&gt;` | — | `isMerging` |
| `list_conflicts`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;merge.ConflictFile&gt;, string&gt;` | — | `listConflicts` |
| `resolve_conflict_side`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string`<br>`side: string` | `Result&lt;(), string&gt;` | — | `resolveConflictSide` |
| `mark_conflict_resolved`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string` | `Result&lt;(), string&gt;` | — | `markConflictResolved` |
| `complete_merge`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string`<br>`message: string` | `Result&lt;string, string&gt;` | — | `completeMerge` |
| `abort_merge`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs`</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | — | `abortMerge` |
| `git_clone`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs` · async</sub> | `url: string`<br>`dest: string` | `Result&lt;(), string&gt;` | AppHandle | `gitClone` |
| `git_fetch`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs` · async</sub> | `repo_path: string`<br>`remote_name: Option&lt;string&gt;` | `Result&lt;(), string&gt;` | AppHandle | `gitFetch` |
| `git_pull`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs` · async</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | AppHandle | `gitPull` |
| `git_push`<br><sub>`src/CodeFlow.App/Git/GitCommands.cs` · async</sub> | `repo_path: string`<br>`set_upstream: bool` | `Result&lt;(), string&gt;` | AppHandle | `gitPush` |

### `backend/git/commands.go` — the remaining 3 → [04-git](04-git.md)
<sub>2.x: `src/CodeFlow.App/Git/Checkpoints.cs`, a separate file. A checkpoint is a git ref, so the port kept it with git.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `list_ai_checkpoints`<br><sub>`src/CodeFlow.App/Git/Checkpoints.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;checkpoint.CheckpointInfo&gt;, string&gt;` | — | `listAiCheckpoints` |
| `restore_ai_checkpoint`<br><sub>`src/CodeFlow.App/Git/Checkpoints.cs`</sub> | `repo_path: string`<br>`checkpoint_id: string` | `Result&lt;Vec&lt;string&gt;, string&gt;` | — | `restoreAiCheckpoint` |
| `delete_ai_checkpoint`<br><sub>`src/CodeFlow.App/Git/Checkpoints.cs`</sub> | `repo_path: string`<br>`checkpoint_id: string` | `Result&lt;(), string&gt;` | — | `deleteAiCheckpoint` |

### `backend/workspaces/commands.go` — 14 · `backend/review/commands.go` — 7 → [09-workspace-scoped](09-workspace-scoped.md)
<sub>2.x: `src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`. The seven `*_review_run*` names are the review-run **history store** — storage, not pipeline — and landed in `backend/review` with Phase 2, which is why they answer even when the pipeline cannot run.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `get_setting`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `key: string` | `Result&lt;Option&lt;string&gt;, string&gt;` | State | `getSetting` |
| `set_setting`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `key: string`<br>`value: string` | `Result&lt;(), string&gt;` | State | `setSetting` |
| `get_workspace_prompt`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string`<br>`kind: string` | `Result&lt;string, string&gt;` | State | `getWorkspacePrompt` |
| `set_workspace_prompt`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string`<br>`kind: string`<br>`content: string` | `Result&lt;(), string&gt;` | State | `setWorkspacePrompt` |
| `default_workspace_prompt`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `kind: string` | `string` | — | `defaultWorkspacePrompt` |
| `list_review_runs`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;ReviewRunSummary&gt;, string&gt;` | State | `listReviewRuns` |
| `get_review_run`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;Option&lt;ReviewRunDetail&gt;, string&gt;` | State | `getReviewRun` |
| `mark_review_finding`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `run_id: string`<br>`finding_id: string`<br>`estado: string`<br>`motivo: Option&lt;string&gt;` | `Result&lt;(), string&gt;` | State | `markReviewFinding` |
| `delete_review_run`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteReviewRun` |
| `delete_review_runs_for_pr`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `project_id: string`<br>`pr_id: long` | `Result&lt;(), string&gt;` | State | `deleteReviewRunsForPr` |
| `purge_workspace_review_runs`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string` | `Result&lt;(), string&gt;` | State | `purgeWorkspaceReviewRuns` |
| `export_review_runs`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string`<br>`id: Option&lt;string&gt;`<br>`dest_dir: string` | `Result&lt;int, string&gt;` | State | `exportReviewRuns` |
| `list_workspace_agents`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;WorkspaceAgent&gt;, string&gt;` | State | `listWorkspaceAgents` |
| `upsert_workspace_agent`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: Option&lt;string&gt;`<br>`workspace_id: string`<br>`name: string`<br>`role: string`<br>`provider: string`<br>`model: string`<br>`prompt: string`<br>`enabled: bool` | `Result&lt;WorkspaceAgent, string&gt;` | State | `upsertWorkspaceAgent` |
| `delete_workspace_agent`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteWorkspaceAgent` |
| `list_review_contexts`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;ReviewContext&gt;, string&gt;` | State | `listReviewContexts` |
| `upsert_review_context`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: Option&lt;string&gt;`<br>`workspace_id: string`<br>`name: string`<br>`content: string`<br>`enabled: bool` | `Result&lt;ReviewContext, string&gt;` | State | `upsertReviewContext` |
| `delete_review_context`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteReviewContext` |
| `list_workspace_mcps`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;WorkspaceMcp&gt;, string&gt;` | State | `listWorkspaceMcps` |
| `upsert_workspace_mcp`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: Option&lt;string&gt;`<br>`workspace_id: string`<br>`name: string`<br>`command: string`<br>`args: string`<br>`env: string`<br>`enabled: bool` | `Result&lt;WorkspaceMcp, string&gt;` | State | `upsertWorkspaceMcp` |
| `delete_workspace_mcp`<br><sub>`src/CodeFlow.App/Workspaces/WorkspaceCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteWorkspaceMcp` |

### `backend/workspaces/skills.go` — 10 commands → [09-workspace-scoped](09-workspace-scoped.md)
<sub>2.x: `src/CodeFlow.App/Workspaces/SkillCommands.cs`. Registered by `workspaces.RegisterSkills`, separately from the rest of the package.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `install_workspace_skill`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs` · async</sub> | `workspace_id: string`<br>`source_repo: string`<br>`skill_name: string` | `Result&lt;WorkspaceSkill, string&gt;` | AppHandle, State | `installWorkspaceSkill` |
| `list_workspace_skills`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;WorkspaceSkill&gt;, string&gt;` | State | `listWorkspaceSkills` |
| `remove_workspace_skill`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `removeWorkspaceSkill` |
| `set_workspace_skill_enabled`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `id: string`<br>`enabled: bool` | `Result&lt;(), string&gt;` | State | `setWorkspaceSkillEnabled` |
| `create_custom_skill`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string`<br>`name: string`<br>`skill_md: string` | `Result&lt;WorkspaceSkill, string&gt;` | State | `createCustomSkill` |
| `import_skill_from_folder`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string`<br>`src_dir: string` | `Result&lt;WorkspaceSkill, string&gt;` | State | `importSkillFromFolder` |
| `list_skill_files`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string`<br>`skill_name: string` | `Result&lt;Vec&lt;string&gt;, string&gt;` | — | `listSkillFiles` |
| `read_skill_file`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string`<br>`skill_name: string`<br>`rel_path: string` | `Result&lt;string, string&gt;` | — | `readSkillFile` |
| `write_skill_file`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string`<br>`skill_name: string`<br>`rel_path: string`<br>`content: string` | `Result&lt;(), string&gt;` | — | `writeSkillFile` |
| `delete_skill_file`<br><sub>`src/CodeFlow.App/Workspaces/SkillCommands.cs`</sub> | `workspace_id: string`<br>`skill_name: string`<br>`rel_path: string` | `Result&lt;(), string&gt;` | — | `deleteSkillFile` |

### `backend/activity/commands.go` — 7 commands → [09-workspace-scoped](09-workspace-scoped.md)
<sub>2.x: `src/CodeFlow.App/Activity/ActivityCommands.cs`</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `list_chat_conversations`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `project_id: string`<br>`search: Option&lt;string&gt;` | `Result&lt;Vec&lt;ChatConversationSummary&gt;, string&gt;` | State | `listChatConversations` |
| `get_chat_conversation`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `project_id: string`<br>`session_id: string` | `Result&lt;Vec&lt;ActivityLogEntry&gt;, string&gt;` | State | `getChatConversation` |
| `delete_chat_conversation`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `project_id: string`<br>`session_id: string` | `Result&lt;(), string&gt;` | State | `deleteChatConversation` |
| `rename_chat_conversation`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `project_id: string`<br>`session_id: string`<br>`title: string` | `Result&lt;(), string&gt;` | State | `renameChatConversation` |
| `list_job_history`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `project_id: string` | `Result&lt;Vec&lt;JobHistoryEntry&gt;, string&gt;` | State | `listJobHistory` |
| `rename_job_history_entry`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `id: string`<br>`label: string` | `Result&lt;(), string&gt;` | State | `renameJobHistoryEntry` |
| `delete_job_history_entry`<br><sub>`src/CodeFlow.App/Activity/ActivityCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `deleteJobHistoryEntry` |

### `backend/security/commands.go` — 9 of its 10 → [10-security](10-security.md)
<sub>2.x: `src/CodeFlow.App/Security/SecretCommands.cs`. All nine are registered from one loop over the three key kinds — `r.Add("set_"+prefix, …)` — so grepping the literal name finds nothing; the tenth is `scan_staged_secrets` below.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `set_ado_pat`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `org: string`<br>`pat: string` | `Result&lt;(), string&gt;` | — | `setAdoPat` |
| `has_ado_pat`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `org: string` | `Result&lt;bool, string&gt;` | — | `hasAdoPat` |
| `delete_ado_pat`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `org: string` | `Result&lt;(), string&gt;` | — | `deleteAdoPat` |
| `set_github_token`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `host: string`<br>`token: string` | `Result&lt;(), string&gt;` | — | `setGithubToken` |
| `has_github_token`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `host: string` | `Result&lt;bool, string&gt;` | — | `hasGithubToken` |
| `delete_github_token`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `host: string` | `Result&lt;(), string&gt;` | — | `deleteGithubToken` |
| `set_ai_api_key`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `provider: string`<br>`key: string` | `Result&lt;(), string&gt;` | — | `setAiApiKey` |
| `has_ai_api_key`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `provider: string` | `Result&lt;bool, string&gt;` | — | `hasAiApiKey` |
| `delete_ai_api_key`<br><sub>`src/CodeFlow.App/Security/SecretCommands.cs`</sub> | `provider: string` | `Result&lt;(), string&gt;` | — | `deleteAiApiKey` |

### `backend/security/commands.go` — the remaining 1 → [10-security](10-security.md)
<sub>2.x: `src/CodeFlow.App/Files/WatcherCommands.cs`. The staged-secret gate was filed under the watcher there; in Go it is where the scanner is.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `scan_staged_secrets`<br><sub>`src/CodeFlow.App/Files/WatcherCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;SecretHit&gt;, string&gt;` | — | `scanStagedSecrets` |

### `backend/ai/commands.go` — 13 of its 14 → [05-ai-engines](05-ai-engines.md)
<sub>2.x: `src/CodeFlow.App/Ai/AiCommands.cs`, whose heading said 14 and listed 13. The fourteenth is `resolve_finding_with_ai`, tabulated under the review commands below because that is where it is called from. The five `default_*_template` names register from one loop over a name→prompt map.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `generate_commit_message`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `diff: string`<br>`run_id: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | AppHandle, State | `generateCommitMessage` |
| `cancel_ai_run`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs`</sub> | `run_id: string` | `bool` | — | `cancelAiRun` |
| `list_ai_models`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `provider: Option&lt;string&gt;` | `Result&lt;Vec&lt;string&gt;, string&gt;` | State | `listAiModels` |
| `check_ai_provider`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `provider: string` | `Result&lt;ProviderStatus, string&gt;` | State | `checkAiProvider` |
| `resolve_conflict_with_ai`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `repo_path: string`<br>`rel_path: string`<br>`run_id: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | AppHandle, State | `resolveConflictWithAi` |
| `default_commit_template`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs`</sub> | — | `string` | — | `defaultCommitTemplate` |
| `default_review_template`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs`</sub> | — | `string` | — | `defaultReviewTemplate` |
| `default_analyze_template`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs`</sub> | — | `string` | — | `defaultAnalyzeTemplate` |
| `default_pr_description_template`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs`</sub> | — | `string` | — | `defaultPrDescriptionTemplate` |
| `default_resolve_conflict_template`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs`</sub> | — | `string` | — | `defaultResolveConflictTemplate` |
| ~~`analyze_working_changes`~~ | — | — | — | Superseded by `review_changes` (`Tickets/TicketCommands.cs`), which carries the scope and the ticket axes together. Its body still lives in `AiTurn.AnalyzeChangesAsync`. |
| `resolve_finding_with_ai`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `project_id: string`<br>`finding_prompt: string`<br>`run_id: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | AppHandle, State | `resolveFindingWithAi` |
| `send_chat_message`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `project_id: string`<br>`message: string`<br>`session_id: Option&lt;string&gt;`<br>`conversation_id: Option&lt;string&gt;`<br>`run_id: Option&lt;string&gt;`<br>`agent_provider: Option&lt;string&gt;`<br>`agent_model: Option&lt;string&gt;`<br>`agent_prompt: Option&lt;string&gt;` | `Result&lt;ChatReply, string&gt;` | AppHandle, State | `sendChatMessage` |
| `inline_edit_with_ai`<br><sub>`src/CodeFlow.App/Ai/AiCommands.cs` · async</sub> | `rel_path: string`<br>`file_content: string`<br>`selection: string`<br>`instruction: string`<br>`run_id: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | AppHandle, State | `inlineEditWithAi` |

### `backend/providers/commands.go` — 15 · `review/commands_run.go` — 2 · `review/commands_publish.go` — 2 · `ai/commands.go` — 1 → [07-review-pipeline](07-review-pipeline.md)
<sub>2.x: `src/CodeFlow.App/Review/ReviewCommands.cs`, and the largest regrouping of the port. Fifteen of these twenty-two never were review commands: reading a pull request, listing its threads and acting on it are *provider* calls, dispatched by host, and publishing a review cannot reach a host the sidebar would not have listed from. Two more rows here are `HOST` (`open_external_url`, `open_repo_in_browser`).</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `auto_link_project`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs`</sub> | `project_id: string` | `Result&lt;AutoLinkResult, string&gt;` | State | `autoLinkProject` |
| `ado_list_projects`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `org: string` | `Result&lt;Vec&lt;`AdoProject`&gt;, string&gt;` | — | `adoListProjects` |
| `ado_list_repos`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `org: string`<br>`project: string` | `Result&lt;Vec&lt;`AdoRepo`&gt;, string&gt;` | — | `adoListRepos` |
| `link_project_ado`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs`</sub> | `id: string`<br>`ado_org: string`<br>`ado_project: string`<br>`ado_repo_id: string` | `Result&lt;(), string&gt;` | State | `linkProjectAdo` |
| `unlink_project`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `unlinkProject` |
| `open_repo_in_browser` **`HOST`**<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` → split in two</sub> | `project_id: string` | `Result&lt;(), string&gt;` | State | the renderer composes `repo_web_url` + `host.openExternal` |
| `open_external_url` **`HOST`**<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` → `HostService.OpenExternal`</sub> | `url: string` | `Result&lt;(), string&gt;` | — | `host.openExternal(url)`; a capture-phase listener in `main.tsx` routes every external link there, because WKWebView ignores `target="_blank"` |
| `list_pull_requests`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string` | `Result&lt;Vec&lt;`PullRequestSummary`&gt;, string&gt;` | State | `listPullRequests` |
| `resolve_pr_link`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string` | `Result&lt;PrLinkResolution, string&gt;` | State | `resolvePrLink` |
| `review_pr_from_link`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string`<br>`job_id: string`<br>`level: string`<br>`workspace_id: string`<br>`agent_provider: Option&lt;string&gt;`<br>`agent_model: Option&lt;string&gt;`<br>`agent_prompt: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | AppHandle, State | `reviewPrFromLink` |
| `pr_link_pull_request`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string` | `Result&lt;`PullRequestSummary`, string&gt;` | State | `prLinkPullRequest` |
| `pr_link_comment_threads`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string` | `Result&lt;Vec&lt;`PrCommentThread`&gt;, string&gt;` | State | `prLinkCommentThreads` |
| `pr_link_decision`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string` | `Result&lt;string, string&gt;` | State | `prLinkDecision` |
| `act_on_pr_link`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string`<br>`action: string`<br>`body: Option&lt;string&gt;` | `Result&lt;`PullRequestSummary`, string&gt;` | State | `actOnPrLink` |
| `post_pr_link_review_comment`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `url: string`<br>`items: Vec&lt;PostFindingItem&gt;`<br>`post_summary: bool`<br>`summary: Option&lt;string&gt;` | `Result&lt;(), string&gt;` | State | `postPrLinkReviewComment` |
| `generate_pr_description`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`source_branch: string`<br>`target_branch: string`<br>`run_id: Option&lt;string&gt;` | `Result&lt;PrDescriptionDraft, string&gt;` | AppHandle, State | `generatePrDescription` |
| `create_pull_request`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`title: string`<br>`description: string`<br>`source_branch: string`<br>`target_branch: string`<br>`draft: bool` | `Result&lt;`PullRequestSummary`, string&gt;` | State | `createPullRequest` |
| `list_pr_comment_threads`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`pr_id: long` | `Result&lt;Vec&lt;`PrCommentThread`&gt;, string&gt;` | State | `listPrCommentThreads` |
| `review_pull_request`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`pr_id: long`<br>`job_id: string`<br>`level: string`<br>`// When an SDD/Harness agent runs this review`<br>`its provider + model + prompt for this run. agent_provider: Option&lt;string&gt;`<br>`agent_model: Option&lt;string&gt;`<br>`agent_prompt: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | AppHandle, State | `reviewPullRequest` |
| `post_pr_review_comment`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`pr_id: long`<br>`run_id: string`<br>`items: Vec&lt;PostFindingItem&gt;`<br>`post_summary: bool`<br>`summary: Option&lt;string&gt;` | `Result&lt;(), string&gt;` | State | `postPrReviewComment` |
| `pr_review_decision`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`pr_id: long` | `Result&lt;string, string&gt;` | State | `prReviewDecision` |
| `act_on_pull_request`<br><sub>`src/CodeFlow.App/Review/ReviewCommands.cs` · async</sub> | `project_id: string`<br>`pr_id: long`<br>`action: string`<br>`body: Option&lt;string&gt;` | `Result&lt;PrActionOutcome, string&gt;` | State | `actOnPullRequest` |

### `backend/providers/commands.go` — 2 of its 18 → [06-providers](06-providers.md)
<sub>2.x: `src/CodeFlow.App/Providers/ProviderCommands.cs`. The other sixteen are the fifteen above plus `repo_web_url`, which no 2.x table row ever carried.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `link_project_github`<br><sub>`src/CodeFlow.App/Providers/ProviderCommands.cs`</sub> | `id: string`<br>`github_owner: string`<br>`github_repo: string`<br>`github_host: string` | `Result&lt;(), string&gt;` | State | `linkProjectGithub` |
| `github_authenticated_user`<br><sub>`src/CodeFlow.App/Providers/ProviderCommands.cs` · async</sub> | `host: string` | `Result&lt;string, string&gt;` | — | `githubAuthenticatedUser` |

### `backend/files/commands.go` — 13 of its 15 → [11-files-search-terminal](11-files-search-terminal.md)
<sub>2.x: `src/CodeFlow.App/Files/FileCommands.cs`. Most register through a `withRepo` helper that binds `repoPath` before the handler runs, so the literal name sits at that call rather than at `r.Add`.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `list_dir`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`sub_path: Option&lt;string&gt;` | `Result&lt;Vec&lt;fsops.FileEntry&gt;, string&gt;` | — | `listDir` |
| `read_file_text`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string` | `Result&lt;string, string&gt;` | — | `readFileText` |
| `write_file_text`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string`<br>`content: string` | `Result&lt;(), string&gt;` | — | `writeFileText` |
| `write_file_bytes`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `path: string`<br>`contents: Vec&lt;byte&gt;` | `Result&lt;(), string&gt;` | — | `writeFileBytes` |
| `move_path`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`from_rel: string`<br>`dest_dir: string` | `Result&lt;string, string&gt;` | — | `movePath` |
| `create_dir`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string` | `Result&lt;(), string&gt;` | — | `createDir` |
| `create_file`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string` | `Result&lt;(), string&gt;` | — | `createFile` |
| `open_in_default_app`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`rel_path: string` | `Result&lt;(), string&gt;` | — | `openInDefaultApp` |
| `reveal_in_file_manager`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `path: string` | `Result&lt;(), string&gt;` | — | `revealInFileManager` |
| `open_in_vscode`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `path: string` | `Result&lt;(), string&gt;` | — | `openInVsCode` |
| `list_repo_files`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string` | `Result&lt;Vec&lt;string&gt;, string&gt;` | — | `listRepoFiles` |
| `search_repo`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`query: string`<br>`options: `SearchOptions`<br>`max_results: int` | `Result&lt;SearchOptions, string&gt;` | — | `searchRepo` |
| `replace_in_repo`<br><sub>`src/CodeFlow.App/Files/FileCommands.cs`</sub> | `repo_path: string`<br>`query: string`<br>`replacement: string`<br>`options: `SearchOptions`<br>`only_path: Option&lt;string&gt;` | `Result&lt;SearchOptions, string&gt;` | — | `replaceInRepo` |

### `backend/files/commands.go` — the remaining 2 → [11-files-search-terminal](11-files-search-terminal.md)
<sub>2.x: `src/CodeFlow.App/Files/WatcherCommands.cs`. Registered by `files.RegisterWatcher`, which takes the watcher registry rather than the `Deps` the rest of the package uses.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `start_watching`<br><sub>`src/CodeFlow.App/Files/WatcherCommands.cs`</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | AppHandle, State | `startWatching` |
| `stop_watching`<br><sub>`src/CodeFlow.App/Files/WatcherCommands.cs`</sub> | `repo_path: string` | `Result&lt;(), string&gt;` | State | `stopWatching` |

### `backend/dbml/commands.go` — 10 commands → [15-dbml](15-dbml.md)
<sub>2.x: `src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `dbml_list_documents`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `root_path: string` | `Result&lt;Vec&lt;string&gt;, string&gt;` | — | `dbmlListDocuments` |
| `dbml_load_layout`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `project_id: string`<br>`rel_path: string` | `Result&lt;Vec&lt;DbmlTablePosition&gt;, string&gt;` | State | `dbmlLoadLayout` |
| `dbml_save_positions`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `project_id: string`<br>`rel_path: string`<br>`positions: Vec&lt;DbmlTablePosition&gt;` | `Result&lt;(), string&gt;` | State | `dbmlSavePositions` |
| `dbml_clear_layout`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `project_id: string`<br>`rel_path: string` | `Result&lt;(), string&gt;` | State | `dbmlClearLayout` |
| `dbml_assist`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `mode: string`<br>`dbml: string`<br>`instruction: string?`<br>`run_id: string?` | `Result&lt;string, string&gt;` | AI | `dbmlAssist` |
| `dbml_list_connections`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | — | `Result&lt;Vec&lt;DbmlConnection&gt;, string&gt;` | State | `dbmlListConnections` |
| `dbml_save_connection`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `connection: NewDbmlConnection` | `Result&lt;DbmlConnection, string&gt;` | State, Keychain | `dbmlSaveConnection` |
| `dbml_delete_connection`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `connection_id: string` | `Result&lt;(), string&gt;` | State, Keychain | `dbmlDeleteConnection` |
| `dbml_test_connection`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `connection_id: string` | `Result&lt;(), string&gt;` | State, Keychain | `dbmlTestConnection` |
| `dbml_introspect_database`<br><sub>`src/CodeFlow.App/Dbml/DbmlCommands.cs`</sub> | `connection_id: string` | `Result&lt;DbmlSchemaSnapshot, string&gt;` | State, Keychain | `dbmlIntrospectDatabase` |

### `backend/diagram/commands.go` — 1 command → [16-diagrams](16-diagrams.md)
<sub>2.x: none — this feature was written here, after the port.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `diagram_list_documents` | `rootPath: string` | `Result&lt;Vec&lt;string&gt;, string&gt;` | — | `diagramListDocuments` |

The parameter is listed as `rootPath`, which is the name the handler actually reads and the wrapper
actually sends. Elsewhere in this table the `Caller parameters` column carries the 2.x signature in
snake_case — provenance, like the `<sub>` lines, and not always what crosses the wire today: the
schema designer's `dbml_list_documents` is listed as `root_path` and is read as `rootPath`. This row
has no 2.x signature to record, so it states the live one.

### `backend/terminal/commands.go` — 4 commands → [11-files-search-terminal](11-files-search-terminal.md)
<sub>2.x: `src/CodeFlow.App/Terminal/TerminalCommands.cs`</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `open_terminal`<br><sub>`src/CodeFlow.App/Terminal/TerminalCommands.cs`</sub> | `cwd: string` | `Result&lt;string, string&gt;` | AppHandle, State | `openTerminal` |
| `write_terminal`<br><sub>`src/CodeFlow.App/Terminal/TerminalCommands.cs`</sub> | `id: string`<br>`data: string` | `Result&lt;(), string&gt;` | State | `writeTerminal` |
| `resize_terminal`<br><sub>`src/CodeFlow.App/Terminal/TerminalCommands.cs`</sub> | `id: string`<br>`cols: ushort`<br>`rows: ushort` | `Result&lt;(), string&gt;` | State | `resizeTerminal` |
| `close_terminal`<br><sub>`src/CodeFlow.App/Terminal/TerminalCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `closeTerminal` |

### deferred — 9 of the 11, never registered → [12-debugging](12-debugging.md)
<sub>2.x had no implementation either. Ten rows: the nine the renderer calls, plus `debug_is_running`, which nothing calls and nothing registers (`DEAD`, DBG-037) and which is why the old heading said 10. The other two of the eleven are `api_grpc_call` and `api_grpc_describe`, tabulated with the API client below. `backend/app/contract_test.go` asserts all eleven stay unregistered: the renderer branches on the refusal, so registering one would be the change.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `debug_start`<br><sub>not implemented (deferred) · async</sub> | `cwd: string`<br>`node_binary: Option&lt;string&gt;`<br>`program: string`<br>`args: Vec&lt;string&gt;`<br>`breakpoints: HashMap&lt;string, Vec&lt;uint&gt;&gt;` | `Result&lt;(), string&gt;` | AppHandle | `debugStart` |
| `debug_start_adapter`<br><sub>not implemented (deferred) · async</sub> | `cwd: string`<br>`command: string`<br>`args: Vec&lt;string&gt;`<br>`launch_config: `JsonElement`<br>`breakpoints: HashMap&lt;string, Vec&lt;uint&gt;&gt;` | `Result&lt;(), string&gt;` | AppHandle | `debugStartAdapter` |
| `debug_stop`<br><sub>not implemented (deferred) · async</sub> | — | `Result&lt;(), string&gt;` | — | `debugStop` |
| `debug_continue`<br><sub>not implemented (deferred) · async</sub> | — | `Result&lt;(), string&gt;` | — | `debugContinue` |
| `debug_pause`<br><sub>not implemented (deferred) · async</sub> | — | `Result&lt;(), string&gt;` | — | `debugPause` |
| `debug_step`<br><sub>not implemented (deferred) · async</sub> | `kind: string` | `Result&lt;(), string&gt;` | — | `debugStep` |
| `debug_set_breakpoints`<br><sub>not implemented (deferred) · async</sub> | `breakpoints: HashMap&lt;string, Vec&lt;uint&gt;&gt;` | `Result&lt;(), string&gt;` | — | `debugSetBreakpoints` |
| `debug_properties`<br><sub>not implemented (deferred) · async</sub> | `object_id: string` | `Result&lt;Vec&lt;Variable&gt;, string&gt;` | — | `debugProperties` |
| `debug_evaluate`<br><sub>not implemented (deferred) · async</sub> | `frame_id: string`<br>`expression: string` | `Result&lt;Variable, string&gt;` | — | `debugEvaluate` |
| `debug_is_running` `DEAD`<br><sub>not implemented (deferred)</sub> | — | `bool` | — | **none — `DEAD`** |

### `backend/apiclient/commands.go` — 29 · `streamcommands.go` — 9 · `httpcommands.go` — 3 → [08-api-client](08-api-client.md)
<sub>2.x: `src/CodeFlow.App/ApiClient/ApiCommands.cs`. Split three ways, along the line that matters at start-up: the stores need the database, the transports do not — so `RegisterHTTP` and `RegisterStreams` sit outside the composition root's `if deps.DB != nil` block and an install whose database failed can still send one request by hand. Two rows here are `HOST` (`api_pick_file`, `api_save_file`) and two are the deferred gRPC pair.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `api_load_tree`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string` | `Result&lt;ApiTree, string&gt;` | State | `apiLoadTree` |
| `api_create_collection`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string`<br>`name: string` | `Result&lt;ApiCollection, string&gt;` | State | `apiCreateCollection` |
| `api_update_collection`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `collection: ApiCollection` | `Result&lt;(), string&gt;` | State | `apiUpdateCollection` |
| `api_delete_collection`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiDeleteCollection` |
| `api_duplicate_collection`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;ApiCollection, string&gt;` | State | `apiDuplicateCollection` |
| `api_create_folder`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `collection_id: string`<br>`parent_id: Option&lt;string&gt;`<br>`name: string` | `Result&lt;ApiFolder, string&gt;` | State | `apiCreateFolder` |
| `api_update_folder`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `folder: ApiFolder` | `Result&lt;(), string&gt;` | State | `apiUpdateFolder` |
| `api_delete_folder`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiDeleteFolder` |
| `api_create_request`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `collection_id: string`<br>`folder_id: Option&lt;string&gt;`<br>`name: string`<br>`protocol: string`<br>`spec: string` | `Result&lt;ApiRequestRow, string&gt;` | State | `apiCreateRequest` |
| `api_update_request`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `request: ApiRequestRow` | `Result&lt;(), string&gt;` | State | `apiUpdateRequest` |
| `api_delete_request`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiDeleteRequest` |
| `api_duplicate_request`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;ApiRequestRow, string&gt;` | State | `apiDuplicateRequest` |
| `api_move_node`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `kind: string`<br>`id: string`<br>`collection_id: string`<br>`parent_id: Option&lt;string&gt;`<br>`index: long` | `Result&lt;(), string&gt;` | State | `apiMoveNode` |
| `api_reorder_collections`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string`<br>`ids: Vec&lt;string&gt;` | `Result&lt;(), string&gt;` | State | `apiReorderCollections` |
| `api_list_environments`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;ApiEnvironment&gt;, string&gt;` | State | `apiListEnvironments` |
| `api_create_environment`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string`<br>`name: string` | `Result&lt;ApiEnvironment, string&gt;` | State | `apiCreateEnvironment` |
| `api_update_environment`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `environment: ApiEnvironment` | `Result&lt;(), string&gt;` | State | `apiUpdateEnvironment` |
| `api_delete_environment`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiDeleteEnvironment` |
| `api_duplicate_environment`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;ApiEnvironment, string&gt;` | State | `apiDuplicateEnvironment` |
| `api_list_history`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string`<br>`limit: long` | `Result&lt;Vec&lt;ApiHistoryEntry&gt;, string&gt;` | State | `apiListHistory` |
| `api_add_history`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `entry: ApiHistoryEntry` | `Result&lt;(), string&gt;` | State | `apiAddHistory` |
| `api_delete_history`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiDeleteHistory` |
| `api_clear_history`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string` | `Result&lt;(), string&gt;` | State | `apiClearHistory` |
| `api_list_cookies`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string` | `Result&lt;Vec&lt;ApiCookie&gt;, string&gt;` | State | `apiListCookies` |
| `api_upsert_cookie`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `cookie: ApiCookie` | `Result&lt;(), string&gt;` | State | `apiUpsertCookie` |
| `api_delete_cookie`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiDeleteCookie` |
| `api_clear_cookies`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `workspace_id: string` | `Result&lt;(), string&gt;` | State | `apiClearCookies` |
| `api_send_http`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `request: HttpSendRequest` | `Result&lt;HttpResponse, string&gt;` | — | `apiSendHttp` |
| `api_send_http_tracked`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `id: string`<br>`request: HttpSendRequest` | `Result&lt;HttpResponse, string&gt;` | State | `apiSendHttpTracked` |
| `api_cancel_http`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiCancelHttp` |
| `api_ws_connect`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `id: string`<br>`request: WsConnectRequest` | `Result&lt;(), string&gt;` | AppHandle | `apiWsConnect` |
| `api_ws_send`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string`<br>`payload: string`<br>`binary: bool` | `Result&lt;(), string&gt;` | State | `apiWsSend` |
| `api_socketio_connect`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `id: string`<br>`request: SocketIoConnectRequest` | `Result&lt;(), string&gt;` | AppHandle | `apiSocketioConnect` |
| `api_socketio_emit`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string`<br>`event: string`<br>`payload_json: string` | `Result&lt;(), string&gt;` | State | `apiSocketioEmit` |
| `api_mqtt_connect`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `id: string`<br>`request: MqttConnectRequest` | `Result&lt;(), string&gt;` | AppHandle | `apiMqttConnect` |
| `api_mqtt_publish`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string`<br>`topic: string`<br>`payload: string`<br>`qos: byte`<br>`retain: bool` | `Result&lt;(), string&gt;` | State | `apiMqttPublish` |
| `api_mqtt_subscribe`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string`<br>`topic: string`<br>`qos: byte` | `Result&lt;(), string&gt;` | State | `apiMqttSubscribe` |
| `api_mqtt_unsubscribe`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string`<br>`topic: string` | `Result&lt;(), string&gt;` | State | `apiMqttUnsubscribe` |
| `api_stream_disconnect`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `id: string` | `Result&lt;(), string&gt;` | State | `apiStreamDisconnect` |
| `api_grpc_describe`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `request: GrpcDescribeRequest` | `Result&lt;Vec&lt;GrpcServiceInfo&gt;, string&gt;` | — | `apiGrpcDescribe` |
| `api_grpc_call`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` · async</sub> | `id: string`<br>`request: GrpcCallRequest` | `Result&lt;GrpcResponse, string&gt;` | State | `apiGrpcCall` |
| `api_read_file_base64`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `path: string` | `Result&lt;FileBase64, string&gt;` | — | `apiReadFileBase64` |
| `api_pick_file` **`HOST`**<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` → `HostService.OpenFile`</sub> | `extensions: Vec&lt;string&gt;` | `Option&lt;string&gt;` | AppHandle | `host.dialog().openFile(…)` |
| `api_save_file` **`HOST`**<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs` → `HostService.SaveFile`</sub> | `default_name: string`<br>`contents: string` | `Result&lt;Option&lt;string&gt;, string&gt;` | AppHandle | `host.dialog().save(…)`; the bytes are written by `write_file_bytes` |
| `api_read_text_file`<br><sub>`src/CodeFlow.App/ApiClient/ApiCommands.cs`</sub> | `path: string` | `Result&lt;string, string&gt;` | — | `apiReadTextFile` |

### `backend/tickets/commands.go` — 17 commands → [14-work-items](14-work-items.md)
<sub>2.x: `src/CodeFlow.App/Tickets/TicketCommands.cs`, whose heading said 13 and listed 17.</sub>

Every command here reads. Commenting and state transitions are a later, separately requested step,
so nothing on this surface can alter a board.

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `update_workspace_ticket_account`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `workspaceId: string`<br>`org: Option&lt;string&gt;`<br>`project: Option&lt;string&gt;` | `()` | State | `updateWorkspaceTicketAccount` |
| `resolve_ticket_account`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `projectId: string` | `TicketAccount` | State | `resolveTicketAccount` |
| `resolve_ticket_link`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `text: string` | `Option&lt;TicketLinkRef&gt;` | — | `resolveTicketLink` |
| `suggest_ticket_for_branch`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `branch: string` | `Option&lt;TicketSuggestion&gt;` | — | `suggestTicketForBranch` |
| `sync_ticket`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs` · async</sub> | `org: string`<br>`project: string`<br>`externalId: string` | `Result&lt;Ticket, string&gt;` | State, HttpClient | `syncTicket` |
| `get_ticket`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `ticketId: string` | `Option&lt;Ticket&gt;` | State | `getTicket` |
| `list_tickets`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `projectId: string` | `Vec&lt;TicketWithLinks&gt;` | State | `listTickets` |
| `get_ticket_criteria`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `ticketId: string` | `TicketCriteria` | State | `getTicketCriteria` |
| `link_branch_ticket`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `projectId: string`<br>`branch: string`<br>`ticketId: string` | `()` | State | `linkBranchTicket` |
| `unlink_branch_ticket`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `projectId: string`<br>`branch: string` | `()` | State | `unlinkBranchTicket` |
| `ticket_for_branch`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `projectId: string`<br>`branch: string` | `Option&lt;Ticket&gt;` | State | `ticketForBranch` |
| `list_sprint_tickets`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs` · async</sub> | `org: string`<br>`project: string`<br>`team: Option&lt;string&gt;` | `Result&lt;Vec&lt;TicketSummary&gt;, string&gt;` | HttpClient | `listSprintTickets` |
| `list_my_tickets`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs` · async</sub> | `org: string`<br>`project: string` | `Result&lt;Vec&lt;TicketSummary&gt;, string&gt;` | HttpClient | `listMyTickets` |
| `preview_ticket`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs` · async</sub> | `org: string`<br>`project: string`<br>`externalId: string` | `Result&lt;Option&lt;TicketSummary&gt;, string&gt;` | HttpClient | `previewTicket` |
| `list_ticket_reviews`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs`</sub> | `projectId: string`<br>`branch: string` | `Result&lt;Vec&lt;TicketReviewResult&gt;, string&gt;` | Database | `listTicketReviews` |
| `review_changes`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs` · async</sub> | `projectId: string`<br>`jobId: string`<br>`branch: string`<br>`scope: "working" \| "branch"`<br>`withTicket: bool`<br>`baseRef: Option&lt;string&gt;`<br>`level: string`<br>`agentProvider: Option&lt;string&gt;`<br>`agentModel: Option&lt;string&gt;`<br>`agentPrompt: Option&lt;string&gt;` | `Result&lt;string, string&gt;` | Database, AiRunRegistry, HttpClient | `reviewChanges` |
| `comment_ticket`<br><sub>`src/CodeFlow.App/Tickets/TicketCommands.cs` · async</sub> | `ticketId: string`<br>`body: string` | `Result&lt;string, string&gt;` — the work item's URL | Database, HttpClient | `commentTicket` |

### `backend/update/commands.go` — 3 commands → [02-bootstrap-platform](02-bootstrap-platform.md)

<sub>2.x: `src/CodeFlow.App/Update/UpdateService.cs`. **These four rows are new to this document.**
They are ordinary registry commands and always were; they had no table because the renderer calls
them from `frontend/src/lib/bridge/updater.ts` rather than from `commands.ts`, and this document's
tables were generated from the latter. `backend/app/contract_test.go` reads all three wrapper files,
which is how the omission surfaced.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `update_current_version`<br><sub>`backend/update/commands.go`</sub> | — | `string` — the build's own version, `0.0.0` when unstamped | — | `getVersion` |
| `update_check`<br><sub>`backend/update/commands.go` · async</sub> | — | `Availability` — **never rejects**; `available` + `reason` carry the three outcomes (`XLANG-019`) | HttpClient, CredentialStore | `check` |
| `update_download`<br><sub>`backend/update/commands.go` · async</sub> | `assetUrl: string`<br>`assetName: string` | `Result&lt;string, string&gt;` — where the artefact landed. Emits `update:progress` | HttpClient, CredentialStore, Opener | `downloadAndInstall` |

### `backend/providers/commands.go` — the row 2.x never had → [07-review-pipeline](07-review-pipeline.md)

<sub>`repo_web_url` is called by `openRepoInBrowser` and registered (REVIEW-005), and no 2.x table row
carried it. It rebuilds a repository's home page from its **live remote**, not from the saved
columns, which is why it is a provider command and not a file one.</sub>

| Command | Caller parameters | Returns | Injected | TS wrapper |
|---|---|---|---|---|
| `repo_web_url`<br><sub>`backend/providers/commands.go` · async</sub> | `projectId: string` | `Result&lt;string, string&gt;` — the renderer opens it through `host.openExternal` | Database | `openRepoInBrowser` |
