# Phase 0 findings — evidence behind MIGRATION-GO.md

> **Measured 2026-09-16/17**, before any production code, to replace desk reasoning with evidence.
> The probes were throwaway programs (not part of this repository). Their essential code and outputs
> are summarised here so each claim in `MIGRATION-GO.md` that says *measured* can be traced back.
> Nothing touched the user's real CodeFlow data: the 2.7.1 core ran with a temporary `HOME`, the
> database was read from a copy, and the keychain was queried for attributes only.

## Environments

| Id | Machine | Versions |
|---|---|---|
| macOS | the operator's Mac | macOS 27.0 (26A428), Safari 27 (WebKit 22625.1.29.11.27), git 2.54.0 (Apple Git-157), Go 1.26.4, Xcode, CodeFlow 2.7.1 installed in `/Applications` |
| Windows | GitHub Actions `windows-latest`, private throwaway repository `codeflow-go-windows-probe` (deleted afterwards) | see W-env below |

Wails `v3.0.0-beta.23` for every probe; libraries at the §4.2 versions.

---

## macOS

### M-1 — Wails webview probe (`wails://localhost`)

A Wails app compiled from the exact API of MIGRATION-GO.md §3.9/§6 (window options, close hook with
`Cancel`, `WindowRuntimeReady`, tray with `SetIcon`/`SetTooltip`/`SetMenu`/`OnClick`, macOS menu with
roles and a custom Quit, `SingleInstanceOptions`, `events.Mac.ApplicationShouldHandleReopen`,
`InvokeSync`, `DisableDefaultSignalHandler`, `Permissions`, `WindowFilesDropped` +
`Context().DroppedFiles()`, dialogs, `Env.OpenFileManager`, `Browser.OpenURL/OpenFile`,
`Clipboard.SetText/Text`) and a one-method bridge `Bridge.Invoke(ctx, method, json.RawMessage)
(json.RawMessage, error)`. `go build` and `go vet` passed for **darwin/arm64 and windows/amd64**. The
page carried the production CSP of §6.7 in a `<meta>` tag, called Go only through
`Call.ByName("main.Bridge.Invoke", …)`, and reported back before quitting by itself.

| Check | Result |
|---|---|
| Origin / secure context | `wails://localhost`, **`isSecureContext: true`** |
| `crypto.randomUUID`, `crypto.subtle`, `navigator.clipboard`, `ClipboardItem` | all present; `randomUUID()` returned 36 chars; `subtle.digest("SHA-256","abc")` = `ba7816bf…15ad` |
| CSS | `anchor-name` ✔ `position-area` ✔ `position-try-fallbacks` ✔ `light-dark()` ✔ `color-mix(in oklch)` ✔ `container-type` ✔; `-webkit-app-region` ✘ `app-region` ✘; **unprefixed `user-select` ✘** |
| Popover anchored with `position-area: bottom` | placed directly under its anchor (top 116 = anchor bottom 116) |
| `--wails-draggable` read back | `drag` |
| Module worker from `new URL("./worker.js", import.meta.url)` | loaded (`wails://localhost/worker.js`), replied |
| CSP violations during the run | **0** |
| Params round trip | `{repoPath:"/x", maxSafe: 9007199254740991, nested:{snake_case:null}}` returned identical |
| Void command returning `json.RawMessage("null")` | JS received `null` |
| Bound method returning an untyped `nil` | JS received **`{}`** (the trap of §3.4) |
| `errors.New("NOTHING_TO_ANALYZE: No hay cambios sin commitear para analizar")` | `RuntimeError`, `message` identical, `startsWith("NOTHING_TO_ANALYZE: ")` true |
| Unknown command | `message` = `unknown command 'nope'` |
| Event `app.Event.Emit("probe:event", {run_id,…})` | `ev.data.run_id === "r-1"` |
| Two calls sleeping 1 s each, in parallel | **1 002 ms** total (calls run concurrently) |
| 20 MiB string response | 137 ms |
| 5 MiB request | 79 ms |
| `ByteArray` from a 1 MiB JS number array | decoded, 132 ms |
| 70 MiB request | rejected: `Invalid runtime call: assembled body too large` (64 MiB cap) |
| Go `Clipboard.SetText` → `Text` | round-trip equal; the user's clipboard restored afterwards |
| Page `navigator.clipboard.writeText` without a gesture | **`NotAllowedError`** |
| `requestIdleCallback` | undefined (not used by the renderer) |
| `ongesturestart` on `window` | false — pinch handling stays a manual check (W8) |
| User-Agent | `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) wails.io` |
| Asset server | `AssetFileServerFS` over `//go:embed all:assets` served `assets/index.html` at `/` without `fs.Sub` |
| Wails log for a failed call | `ERR Binding call failed: Bound method returned an error: NOTHING_TO_ANALYZE: …` (log only; never in `message`) |

Why the secure context is not luck: WebKit's own API test `URLSchemeHandler.isSecureContext`
(`Tools/TestWebKitAPI/Tests/WebKit/WKWebView/WKURLSchemeHandler-1.mm`) asserts that a page served by a
`WKURLSchemeHandler` is a secure context. The private `_registerURLSchemeAsSecure:` fallback that was
prepared (M-1b) was therefore not needed.

**Decisions taken from M-1**: §3.4 (nil trap, 64 MiB), §3.5 (error text verified), §3.7 (concurrency),
§6.1/§6.7 (CSP and drag region), §7.3 (`Call.ByName`, no generated bindings), §7.4 W1/W2/W4/W5/W10/W11
(status per item), §14 D3.

### M-2 — git CLI parity (git 2.54, isolated `HOME` and `GIT_CONFIG_GLOBAL`, `LC_ALL=C`)

A shell script executed every row of MIGRATION-GO.md §8.6 against real temporary repositories.
**55 / 55 checks passed**:

`stash-list-order`, `vector-rename-top-stash-keeps-order`, `vector-rename-non-top-stash-moves-to-top`,
`stash-apply-applied`, `stash-apply-not-found`, `stash-apply-uncommitted-changes`,
`stash-apply-conflicts`, `stash-conflict-marks-index-without-merge-head`,
`is-merging-false-after-stash-conflict`, `stash-pop-conflict-keeps-entry`,
`vector-checkout-blocked-by-uncommitted-changes`, `vector-stash-then-checkout-succeeds`,
`checkout-detached`, `porcelain-detached-head`, `upstream-track-in-sync-is-empty`,
`ahead-behind-cannot-nest-upstream-atom`, `upstream-track-ahead-count`, `rev-list-left-right-count`,
`checkout-remote-tracking`, `unpushed-commits`, `push-streams-stderr`, `push-u-detached-note`,
`porcelain-staged-and-modified-single-entry`, `porcelain-rename-in-index`, `porcelain-worktree-delete`,
`porcelain-untracked-recursive`, `diff-full-file-context-U1000000`, `diff-cached-rename`,
`diff-tree-root-commit`, `commit-file-diff-with-old-path`, `diff-binary-no-hunks`,
`diff-untracked-no-index`, `vector-discard-all-reverts-tracked-removes-untracked`,
`vector-discard-all-keeps-staged-content`, `vector-restore-reverts-edits-and-deletes-created-files`,
`checkpoint-create-does-not-touch-dot-git-index`, `vector-snapshot-leaves-index-alone`,
`vector-unchanged-checkpoint-auto-drops`, `merge-not-up-to-date-detected`, `merge-fast-forward`,
`merge-up-to-date`, `merge-clean-merge-commit`, `merge-conflicts-outcome`, `conflict-stages-1-2-3`,
`conflict-versions-base-ours-theirs`, `resolve-conflict-theirs`, `complete-merge-two-parents`,
`abort-merge`, `commit-no-verify-author-equals-committer`, `reset-mixed-keeps-worktree`,
`set-remote-url-fetch-and-push`, `identity-missing-key-exit-1`, `check-ignore-stdin-z`,
`ls-files-walk-exclude-standard-sorted`, `unified-patch-from-blobs`.

All 9 git scenario vectors (`git_stash` ×2, `git_branch` ×2, `git_diff` ×2, `git_checkpoint` ×3) pass
when implemented with the CLI commands of §8.6.

Corrections it forced into §8.6:
- `%(ahead-behind:%(upstream))` is **invalid** (`fatal: failed to find '%(upstream'`): use
  `%(upstream:track,nobracket)` or `git rev-list --left-right --count b...upstream`.
- `git ls-files --cached --others` prints the two groups separately → sort.
- Stash outcome texts under `LC_ALL=C`: not found = exit 128 `log for 'stash' only has N entries`;
  uncommitted = exit 1 `Your local changes to the following files would be overwritten by merge`;
  conflicts = exit 1 `CONFLICT (content)` with **no** `MERGE_HEAD`.
- Checkout blocked = exit 1 `Your local changes to the following files would be overwritten by checkout`.
- `-U1000000` (the C# `ContextLines`) yields one whole-file hunk.
- A failing `pre-commit` hook blocks `git commit` but not `git commit --no-verify` (libgit2 parity).

### M-3 — parity oracle against the installed 2.7.1 core

A Go client implementing §2.3 started `/Applications/CodeFlow.app/Contents/Resources/core/codeflow-core
--app-version 2.7.1` with `HOME` set to a temporary directory.

| Check | Result |
|---|---|
| Ready line | `codeflow-core ready <endpoint>` |
| Endpoint | `<tmp HOME>/CodeFlow/.ipc-<pid>.sock` |
| Database created under the temporary base | yes |
| Frames | uint32 LE length + JSON; hello `{"channel":"rpc","token":…}` accepted |
| `list_workspaces` on a new base | `{"id":1,"result":[]}` |
| `create_workspace` result keys | `id, name, icon, color, sort_order, created_at, git_name, git_email, ado_org, ado_project` |
| `created_at` format | matches `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{7}\+00:00$` |
| `get_status` on a repo with one untracked file | `{"conflicted":[],"current_branch":"main","is_detached":false,"staged":[],"unstaged":[],"untracked":[{"path":"new.txt","status":"untracked"}]}` |
| `list_branches` | `[{"ahead":0,"behind":0,"is_head":true,"is_remote":false,"name":"main","target":"…","upstream":null}]` — explicit `null`, not omitted |
| Missing parameter | `{"error":"missing required parameter 'repoPath'"}` |
| Unknown command | `{"error":"unknown command 'definitely_not_a_command'"}` |
| `update_current_version` | `"2.7.1"` |
| `default_commit_template` | byte-identical (SHA-256) to `docs/verbatim/prompts/DEFAULT_COMMIT_TEMPLATE.txt` |
| Closing the rpc connection | the core exited cleanly |

This also proves the differential oracle of MIGRATION-GO.md §9.7 is practical.

### M-4 — SQLite: a copy of the real 2.7.1 database opened with `modernc.org/sqlite` v1.59.0

DSN `file:<copy>?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)`,
one connection. Only schema facts were printed.

| Check | Result |
|---|---|
| Tables | 23, identical to `Storage/Schema.cs` (`activity_log`, `api_collections`, `api_cookies`, `api_environments`, `api_folders`, `api_history`, `api_requests`, `app_settings`, `conversation_titles`, `db_connections`, `dbml_layouts`, `job_history`, `projects`, `review_contexts`, `review_runs`, `ticket_links`, `ticket_review_runs`, `tickets`, `workspace_agents`, `workspace_mcps`, `workspace_prompts`, `workspace_skills`, `workspaces`) |
| Indexes | 10, identical (`idx_activity_log_project`, `idx_api_cookies_key`, `idx_api_folders_parent`, `idx_api_history_time`, `idx_api_requests_parent`, `idx_dbml_layouts_key`, `idx_job_history_project`, `idx_review_runs_pr`, `idx_ticket_review_runs_branch`, `idx_tickets_identity`) |
| `journal_mode` / `foreign_keys` / `synchronous` | `wal` / `1` / `1` (NORMAL) |
| `integrity_check` | `ok` |
| Newest `created_at` in `workspaces`, `projects`, `job_history` | Clock format ✔ (`review_runs`, `activity_log`: no rows) |

The copy was deleted afterwards.

### M-5 — keychain attributes (no data, no prompt)

`keybase/go-keychain` v0.0.1 query: class generic password, service `com.codeflow.app`, `MatchLimitAll`,
`ReturnAttributes` only.

| Check | Result |
|---|---|
| Items | 5 — account prefixes `ado-pat:` ×2, `github-token:` ×1, `codeflow-test:` ×2 (left by the C# test suite) |
| Exact single-item query (class + service + account) | matches |
| Query for a missing account | empty result, not an error |
| Prompt shown | none |

Not measured on purpose: reading the secret data from the Go binary (it would show the keychain password
prompt described in MIGRATION-GO.md §5.3). Phase 8's upgrade drill records it.

### M-6 — PTY (`charmbracelet/x/xpty` v0.1.4, `$SHELL` = `/bin/zsh`)

| Check | Result |
|---|---|
| `NewPty(100, 30)`, `Start`, `Resize(100, 24)` | ok |
| Marker echoed through the PTY | yes |
| Reads | 70 reads, largest 943 bytes (4 096-byte buffer) |
| Shell exit observed by `xpty.WaitProcess` | yes |
| Reader ended by itself after the exit | **no** — it returned `EOF` only after `pty.Close()` |

### M-7 — recursive watcher (`syncthing/notify`, FSEvents)

| Check | Result |
|---|---|
| Tree | 50 000 files, 4 levels deep |
| Watch set-up | < 1 ms |
| Create 4 levels deep → event | 11 ms (`notify.Create`, path matches) |
| Burst of 5 000 writes | 4 110 events in 1.8 s (FSEvents coalesces; the throttle needs only a dirty flag) |
| Idle | heap ~2 MB, 3 goroutines |

---

## Windows (GitHub Actions `windows-latest`)

### W-env

Windows Server 2025 Datacenter 10.0.26100 · git 2.55.0.windows.5 (`git --exec-path` =
`C:/Program Files/Git/mingw64/libexec/git-core`) · WebView2 Runtime 152.0.4191.66 · Go 1.26.8 ·
.NET SDK 10.0.401 · NSIS installed during the run (Chocolatey). The workflow ran all seven probes in one
job (≈ 10 minutes); the repository was deleted afterwards.

### W-1 — what a real 2.7.1 install writes (`CodeFlow-Setup-2.7.1-x64.exe /S /currentuser`)

| Item | Value |
|---|---|
| Install exit code | 0 |
| Install directory | `%LOCALAPPDATA%\Programs\CodeFlow` (Electron files, `resources\`, `Uninstall CodeFlow.exe`) |
| Uninstall key | `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\e452a328-6f16-5dfd-9ae4-7f7f7761c215` — the GUID derived in MIGRATION-GO.md §5.4 |
| `DisplayName` | **`CodeFlow 2.7.1`** (the version is part of the name) |
| `UninstallString` | `"%LOCALAPPDATA%\Programs\CodeFlow\Uninstall CodeFlow.exe" /currentuser` |
| `QuietUninstallString` | same + ` /S` |
| Install key | `HKCU\Software\e452a328-6f16-5dfd-9ae4-7f7f7761c215`: `InstallLocation`, `KeepShortcuts=true`, `ShortcutName=CodeFlow` |
| Shortcuts | Start Menu `Programs\CodeFlow.lnk` and Desktop `CodeFlow.lnk` → `…\CodeFlow.exe` |
| After launching the app for 20 s | 4 `CodeFlow.exe` processes + 1 `codeflow-core.exe`; `C:\CodeFlow\codeflow.db` created |

### W-2 — removing 2.7.1 silently

| Variant | Result |
|---|---|
| A — copied `Uninstall CodeFlow.exe` with `/S /KEEP_APP_DATA /currentuser --updated _?=<dir>` **while the app was running** | exit 0; the old uninstaller **closed the running app itself** (electron-builder's `CHECK_APP_RUNNING` in silent mode); directory, both registry keys and both shortcuts removed; `C:\CodeFlow` kept |
| B — `taskkill` both processes first, then the same command | exit 0; same end state |
| C — same without `--updated` | exit 0; same end state (shortcuts are removed either way) |

### W-3 — Wails NSIS installer replacing a running 2.7.1

- `wails3 generate build-assets -dir build -name CodeFlow -binaryname CodeFlow -productname CodeFlow
  -productidentifier com.codeflow.app -productversion 3.0.0 -productcompany "CodeFlow Probe"` → exit 0,
  generated `project.nsi` + `wails_tools.nsh`.
- `project.nsi` patched to `!include "codeflow_replace_electron.nsh"` and call
  `cf.closeRunningCodeFlow` + `cf.uninstallElectronCodeFlow` from `.onInit` (the script is transcribed in
  MIGRATION-GO.md §10.3).
- `makensis -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user -DARG_WAILS_AMD64_BINARY=<exe>
  project.nsi` → exit 0; output `bin\CodeFlow-amd64-installer.exe`, 10.4 MB (probe app + WebView2
  bootstrapper).

| Step | Result |
|---|---|
| Before | 2.7.1 installed and **running** (4 + 1 processes) |
| Wails installer `/S` | exit 0 |
| Old uninstall/install keys | removed |
| Install directory | `%LOCALAPPDATA%\Programs\CodeFlow` now contains only `CodeFlow.exe` and `uninstall.exe`; no `resources\`, no `Uninstall CodeFlow.exe` |
| Shortcuts | Start Menu and Desktop recreated → the new `CodeFlow.exe` |
| Processes afterwards | none left |
| New uninstall key | `HKCU\…\Uninstall\CodeFlow ProbeCodeFlow` (`${INFO_COMPANYNAME}${INFO_PRODUCTNAME}`), `DisplayName=CodeFlow`, `DisplayVersion=3.0.0`, `UninstallString="…\uninstall.exe"` |
| `C:\CodeFlow` and `codeflow.db` | intact |
| Wails uninstaller `/S` | exit 0; directory, key and shortcuts removed; **`C:\CodeFlow` intact** |

### W-4 — ConPTY with Git Bash (`xpty`)

| Check | Result |
|---|---|
| Shell resolution | `git --exec-path` + 3 parents → `C:\Program Files\Git\bin\bash.exe` (`--login -i`) |
| `NewPty(100,30)`, `Start`, `Resize(100,24)` | ok |
| Interactive session | colour prompt `runneradmin@… MINGW64 …`, bracketed-paste and win32-input-mode sequences (`ESC[?2004h`, `ESC[?9001l`) |
| Input | the probe typed before bash was ready: the first character was lost (`cho: command not found`); `exit` then ran (`logout`), so the process exit code was 127 — an artefact of typing too early, not of ConPTY |
| Reader ended by itself after the exit | **no** — it ended only after `pty.Close()` (`The handle is invalid.`), exactly like macOS |

### W-5 — recursive watcher (`syncthing/notify`, ReadDirectoryChangesW)

| Check | Result |
|---|---|
| Tree | 50 000 files, generated in 9.9 s |
| Watch set-up | < 1 ms |
| Write 4 levels deep → event | < 1 ms (`notify.Write`, path matches) |
| Burst of 5 000 writes | 4 115 events in 2.8 s, no error |
| Idle | heap ~2 MB, 3 goroutines |

### W-6 — WebView2 probe (`http://wails.localhost`)

The same app and page as M-1, built for windows/amd64 with `-H=windowsgui`, ran on the runner's
session.

| Check | Result |
|---|---|
| Origin / secure context | `http://wails.localhost`, **`isSecureContext: true`** |
| `crypto.randomUUID`, `crypto.subtle`, `navigator.clipboard`, `ClipboardItem`, `requestIdleCallback` | all present |
| CSS | everything of M-1 ✔, and (Chromium) `-webkit-app-region` ✔, unprefixed `user-select` ✔ |
| Anchored popover | placed under its anchor |
| Module worker | loaded from `http://wails.localhost/worker.js` |
| CSP violations with `connect-src 'self'` | **0** |
| Params / `null` / untyped `nil` → `{}` / sentinel error / unknown command / event `data` | identical to M-1 |
| Two parallel 1 s calls | 1 010 ms |
| 20 MiB response / 5 MiB request / 1 MiB `ByteArray` | 543 ms / 1 159 ms (chunked) / 876 ms |
| 70 MiB request | rejected `Invalid runtime call: assembled body too large` |
| Go clipboard round trip | equal |
| Page clipboard write without a gesture | `NotAllowedError` |
| User-Agent | `… (Windows NT 10.0; Win64; x64) … Chrome/152.0.0.0 … Edg/152.0.0.0` |

### W-7 — Credential Manager: Go `wincred` ⇄ CodeFlow's own C# `WindowsCredentialManager.cs`

The C# class was copied verbatim from 2.7.1 into a console app.

| Direction | Result |
|---|---|
| Go writes `com.codeflow.app.probe:go` (UTF-8 value with `é`, `UserName`, `PersistLocalMachine`) → C# `Get` | found, value identical |
| C# `Set("probe:dotnet")` → Go `GetGenericCredential("com.codeflow.app.probe:dotnet")` | found, value identical, `UserName=probe:dotnet`, `Persist=2` |
| Missing item | C# returns `null`; Go returns `wincred.ErrElementNotFound` |
| Cleanup | both deleted |

This also closes the "Not verified on Windows" note carried by the C# class since 2.x.

---

## Confidence computation (MIGRATION-GO.md §0.5)

Start at 100 and subtract per residual risk that no probe removed: high −3, medium −1, low −0.5.

| Residual risk | Why a probe could not remove it | Impact | Points |
|---|---|---|---|
| The real renderer (Monaco, xterm, 78 k lines) in WKWebView/WebView2 — only a probe page and the mechanisms it uses were measured | needs the renderer wired to a Go bridge (Phase 0.5/0.6) | medium | −1 |
| Older WebKit (Safari 26.0–26.x on macOS 14/15) | only Safari 27 available; decided by Apple/WebKit release notes and D3 | low | −0.5 |
| First data read of a 2.7.1 keychain item from the Go binary | needs the user's keychain password; behaviour documented from Apple forum evidence | medium | −1 |
| Trackpad pinch in WKWebView (W8) | needs a human gesture | low | −0.5 |
| Windows 10 without WebView2 (bootstrapper path) and SmartScreen on an unsigned installer | runner is Windows Server 2025 with WebView2 present | low | −0.5 |
| The end-to-end 2.7.x updater → 3.0.0 on a published release | requires publishing a real `latest` release | medium | −1 |
| Size of the rewrite itself (R5, R8, R17 in §12): parity is proven for git/storage/credentials/transport, not yet for providers, API client and AI engines | only the port proves it; vectors and oracle make it checkable | medium | −1 |
| Wails v3 still beta (R1) | pinned; mitigated, not removed | low | −0.5 |

**Result: 100 − 6 = 94 %.**
