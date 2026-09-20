# CodeFlow → Go + Wails v3 — migration analysis and step-by-step plan

> **Status**: analysis — no production code exists yet. The risky points were **measured** on macOS and
> Windows on 2026-09-16/17 with throwaway probes; evidence in `docs/phase0-findings.md`. **Confidence
> in this plan: 94 %** (§0.5).
> **Written**: 2026-09-16, against `gastonlarap-a11y/code-flow` at commit `2f86d34` (v2.7.1); revised
> 2026-09-17 after a source fact-check and the probes.
> **Versions**: every version in this file was read that day from proxy.golang.org, the GitHub
> releases API or the npm registry — never recalled. Wails v3 publishes a beta almost daily, so
> **re-verify §4 at the start of Phase 0**.
> **Goal of this file**: nobody executing the migration should need to open the original repository,
> except for the one-time copy of `renderer/` at the start of the work (§11, Phase 0, step 0.3).

---

## Table of contents

0. [How to use this document](#0-how-to-use-this-document)
1. [Executive summary and verdict](#1-executive-summary-and-verdict)
2. [The current system (as-is)](#2-the-current-system-as-is)
3. [Target architecture (to-be)](#3-target-architecture-to-be)
4. [Technology stack, verified 2026-09-16](#4-technology-stack-verified-2026-09-16)
5. [The drop-in contract](#5-the-drop-in-contract)
6. [Electron shell → Wails v3 mapping](#6-electron-shell--wails-v3-mapping)
7. [Renderer adaptation](#7-renderer-adaptation)
8. [Backend porting guide, feature by feature](#8-backend-porting-guide-feature-by-feature)
9. [Testing strategy](#9-testing-strategy)
10. [Build, packaging, CI/CD and release](#10-build-packaging-cicd-and-release)
11. [Phased plan, step by step](#11-phased-plan-step-by-step)
12. [Risk register](#12-risk-register)
13. [Keeping the specification true](#13-keeping-the-specification-true)
14. [Open decisions for the operator](#14-open-decisions-for-the-operator)
15. [Appendices](#15-appendices)

---

## 0. How to use this document

### 0.1 What this directory contains

| Path | What it is |
|---|---|
| `MIGRATION-GO.md` | This file. |
| `docs/BUSINESS_RULES.md` | Index of the specification. Copied verbatim. |
| `docs/business-rules/00…91-*.md` | **The specification** (~11 000 lines): 236 commands, 13 event names, the storage schema and migrations, every rule with its `BUG-*` / `DIVERGENCE-*` / `AMBIGUOUS-*` / `VERBATIM` markers. Copied verbatim. |
| `docs/business-rules/test-vectors/` | 24 `*.vectors.json` fixture files (133 cases), `prompts/review_standard.v2.5.1.txt`, `sql/` seeds (3 files). Language-neutral: the Go tests consume them exactly as the xUnit tests did. |
| `docs/UX-REDESIGN.md`, `docs/REDESIGN-PROPOSAL.md` | The renderer's UX contract and proposal. Copied verbatim. |
| `docs/verbatim/prompts/*.txt` | The 17 prompt files from `src/CodeFlow.App/Ai/Prompts/`, **byte-identical** (UTF-8, no BOM). They are `VERBATIM` contracts: two parsers match on what they make the model emit (`XLANG-001`, `XLANG-016`). Phase 2 (step 3) moves them to `backend/ai/prompts/`. |
| `docs/verbatim/assets/` | `icon.svg` (master), `icon.png` (1024²), `icon.icns`, `icon.ico` (16/24/32/48/64/128/256), `tray.svg`, `tray.png` (32²). Phase 1 moves them to `build/`. |
| `docs/phase0-findings.md` | Evidence: what the macOS probes (M-1…M-7) and the Windows probes (W-1…W-7) measured, and how the confidence figure is computed. Every "measured" in this file points there. |
| `docs/verbatim/test-inventory.md` | The 1 232 xUnit test methods of `tests/CodeFlow.Tests`, grouped by folder and class, generated from the source. The checklist for porting the tests (§9); delete it when the port is complete. |

**Not copied**: `renderer/` (React 19, ~78 000 lines). It is copied once, into its final place
`frontend/`, in §11 Phase 0, step 0.3 — the Phase 0 spike needs the real UI, and the operator's
"copy the renderer in the first phase of the plan" is satisfied there.
Everything else the port needs from the C# and Electron code (literals that live only in code, exact
protocol shapes, build names) is transcribed into this file, mostly §2, §5 and §15.

### 0.2 Reading order

1. §1 — the verdict and the trade-offs, including where the move makes things *worse*.
2. §5 — the drop-in contract: the list of things that must stay byte-identical.
3. §3 — the target architecture and the one pattern that holds it together (command registry).
4. The phase you are executing in §11, then every spec document that phase names, then `docs/business-rules/90-ambiguities.md` and `91-known-bugs.md` for what the code must *not* assume or fix.
5. §8 for the feature you are porting; §9 for how to prove it.

### 0.3 The specification's rules still apply, unchanged

From `docs/business-rules/00-conventions.md`:

- **Do not guess.** A behaviour the spec marks `AMBIGUOUS-*` is not resolved by the port.
- **Do not fix.** `BUG-*` rows still open in `91-known-bugs.md` are preserved on purpose — existing
  installs and the renderer depend on them. Fixing one is a separate, named change.
- **`VERBATIM` is byte-level.** Prompts, regexes, error prefixes, keychain key formats, the review
  markdown format. The cross-language pairs in `13-cross-language-contracts.md` (`XLANG-*`) are now
  **Go ↔ TypeScript** instead of C# ↔ TypeScript; nothing else about them changes.
- **Changed behaviour → update the owning document in the same change.** §13 lists the documents
  this migration itself must update.

### 0.4 Translating the paths the specification cites

The spec cites C#, Electron and renderer paths. Read them through this table:

| Spec cites | In the Go repository |
|---|---|
| `src/CodeFlow.App/Program.cs` | `main.go` (composition) + `backend/app/` (startup stages) |
| `src/CodeFlow.App/<Feature>/…` | `backend/<feature>/…` (table below) |
| `src/CodeFlow.App/Ipc/…` | `backend/bridge/` (the transport itself is gone — §3.5) |
| `shell/src/main.ts`, `permissions.ts`, `app-protocol.ts`, `login-path.ts` | `backend/desktop/` and `backend/platform/` |
| `shell/src/shell-log.ts` | `backend/diagnostics/` |
| `shell/src/preload.ts` + `renderer/src/lib/bridge/*` | `frontend/src/lib/bridge/*` |
| `renderer/src/…` | `frontend/src/…` |
| `tests/CodeFlow.Tests/<Feature>/<X>Tests.cs` | `backend/<feature>/<x>_test.go` |
| `shell/electron-builder.yml`, `scripts/build-app.sh` | `build/config.yml`, `build/<os>/`, `Taskfile.yml` |
| `.github/workflows/ci.yml` | `.github/workflows/ci.yml` (same shape, §10.5) |
| `installer/hooks.nsh` | **Does not exist in the source repo either** (§2.11); `build/windows/nsis/project.nsi` + `codeflow_replace_electron.nsh` (§10.3) |

| C# folder | Go package | Owning spec document |
|---|---|---|
| `Platform/` | `backend/platform` | `02-bootstrap-platform.md` |
| `Diagnostics/` | `backend/diagnostics` | `02-bootstrap-platform.md` |
| `Ipc/` | `backend/bridge` | `01-ipc-surface.md` |
| `Storage/` | `backend/storage` | `03-storage.md` |
| `Security/` | `backend/security` | `10-security.md` |
| `Workspaces/` | `backend/workspaces` | `09-workspace-scoped.md` |
| `Activity/` | `backend/activity` | `09-workspace-scoped.md`, `03-storage.md` |
| `Git/` | `backend/git` | `04-git.md` |
| `Files/` | `backend/files` | `11-files-search-terminal.md`, `10-security.md` (scan) |
| `Terminal/` | `backend/terminal` | `11-files-search-terminal.md` |
| `Ai/` (+ `Engines/`, `Prompts/`) | `backend/ai` (+ `engines/`, `prompts/`) | `05-ai-engines.md` |
| `Providers/` (+ `GitHub/`, `Azure/`) | `backend/providers` (+ `github/`, `azure/`) | `06-providers.md` |
| `Review/` | `backend/review` | `07-review-pipeline.md` |
| `Tickets/` | `backend/tickets` | `14-work-items.md` |
| `ApiClient/` | `backend/apiclient` | `08-api-client.md` |
| `Dbml/` (+ `Introspectors/`) | `backend/dbml` (+ `introspect/`) | `15-dbml.md` |
| `Update/` | `backend/update` | `02-bootstrap-platform.md` (BOOT-021) |
| (Electron main) | `backend/desktop` | `02-bootstrap-platform.md` |
| (debugger, deferred) | — | `12-debugging.md` (stays deferred) |

### 0.5 How much of this is proven, and the confidence figure

This file went through two verification passes after it was first written:

1. **A fact-check against the source** (C#, Electron, renderer, Wails `v3.0.0-beta.23`, electron-builder
   26.16.1, Go's `crypto/tls`): 25 corrections and additions, e.g. the macOS update hand-off mounts the
   `.dmg` instead of revealing it, 2.7.x leaves the app running when it starts the Windows installer, the
   exact electron-builder uninstall key, the `{}`-for-`nil` transport trap, the ConPTY/PTY exit behaviour.
2. **Probes** (`docs/phase0-findings.md`): a Wails app compiled from §3.9/§6 and run on macOS (WKWebView)
   and Windows (WebView2); every git command of §8.6 against real repositories (55/55); the installed
   2.7.1 core driven through its own protocol; a copy of a real 2.7.1 database opened by the Go driver;
   the real keychain items matched by the Go query shape; PTY and recursive watcher on both OSes; a
   Wails NSIS installer replacing a **running** 2.7.1 install on Windows; Credential Manager items read
   and written across the Go library and CodeFlow's own C# class.

Where the text says **measured**, it points at one of those probes. Where it says **verified in the
source**, the file and function are named.

**Confidence rule**: start at 100 and subtract for every residual risk no probe removed — high −3,
medium −1, low −0.5. The table and the arithmetic are at the end of `docs/phase0-findings.md`.

**Confidence: 94 %** — still unverified: the real renderer (Monaco, xterm) inside both webviews rather
than a probe page, older Safari 26.x WebKit, the first keychain data read by the Go binary (password
prompt), trackpad pinch (W8), Windows 10 without WebView2 plus SmartScreen, the published 2.7.x → 3.0.0
update path, and parity of the features only the port itself can prove (providers, API client, AI
engines). Each has a Phase 0/1/8 step.

---

## 1. Executive summary and verdict

### 1.1 What is being done

Replace the Electron 44 shell **and** the .NET 10 sidecar with **one Go binary** that hosts a
**Wails v3** window, keep the React 19 renderer with a rewritten bridge layer, and ship it as
**CodeFlow 3.0.0**, a drop-in replacement that installs over 2.7.x and keeps every user's data and
credentials.

### 1.2 What the move buys

- **Size.** Today: Electron runtime + a self-contained .NET 10 runtime + three native library sets
  (SQLitePCLRaw, LibGit2Sharp, Porta.Pty) + the renderer. Target: one Go binary with the built
  renderer embedded. Measure both in Phase 0 (§11, step 0.9); the renderer bundle (Monaco,
  `@dbml/core`) will dominate the new size.
- **Memory and start-up.** One process instead of Electron main + renderer + GPU + utility processes
  + the .NET host. No 15-second IPC connect budget, no stdin token handshake.
- **A defect class disappears by construction.** The named-pipe address bug (`BUG-BOOT-a`), socket
  errors ending the app (`BUG-BOOT-b`), the "core is down" state (`BOOT-032`), the 64 MiB frame cap,
  and `update:progress` never reaching the renderer (§2.11) all live in the transport being removed.
- **One backend language and one toolchain.** Today the shell is TypeScript and the core is C#;
  `dotnet publish` has to carry native libraries per RID. Go cross-compiles pure-Go code and the
  Windows build needs no CGO (§4).

### 1.3 What the move does *not* buy — the counter-proposal on "better compatibility"

The request assumed Go + Wails brings **better compatibility on Windows and macOS**. That is only
half true, and the half that is false is where the work is:

- Electron **ships its own Chromium**: today the renderer is pixel- and API-identical on both OSes.
- Wails uses **the operating system's webview**:
  - **Windows → WebView2** (Chromium/Edge, evergreen). Practically the same engine as today. Needs the
    WebView2 runtime (preinstalled on Windows 11; the NSIS installer carries the bootstrapper for
    Windows 10 — §10.3).
  - **macOS → WKWebView**, i.e. the system **WebKit** (Safari's engine), whose version follows the
    installed Safari. The renderer was written "for Electron, so there is no fallback path" and uses
    things WebKit supports late or differently: CSS anchor positioning (Safari 26+), the Popover API
    (17+), pinch-zoom as ctrl+wheel (WebKit may send `gesture*` events), `navigator.clipboard` without a
    user gesture, unprefixed `user-select`, `-webkit-app-region`, `target="_blank"` links, and CDP-based
    smoke verification (Chromium only). Full list in §7.4.

**Measured, this turned out smaller than feared** (M-1/W-6): on current macOS (Safari 27) anchor
positioning, popovers, `light-dark()`, module workers and WebCrypto all work, and `wails://` is a secure
context. What really needs work on macOS is the short list W3–W9 (drag region, `-webkit-user-select`,
clipboard through Go, pinch, links) plus a minimum Safari version (§14 D3). It is still work the
Electron build never needed, and older Safari versions stay the one compatibility risk.

Other costs, stated plainly:

- **Full backend rewrite**: 36 679 lines of C# in 200 files, and 27 900 lines of tests (1 101 `[Fact]`
  + 124 `[Theory]`). The specification and the 133 test vectors are what make this tractable.
- **Wails v3 is pre-release**: `v3.0.0-beta.23` (2026-09-16), nightly cadence. **Wails v2 is not an
  alternative**: v2.16.0 is stable but its tray is not usable — `v2/pkg/menu/tray.go` declares a
  `TrayMenu` type, yet the feature sits in `v2/pkg/buildassets/onhold/tray` and is not wired into
  `options.App` (checked in the v2.16.0 tree) — and close-hides-to-tray (`BOOT-007`, `BOOT-012`) is core
  behaviour. Mitigation: exact pin, deliberate upgrades only (§12, R1).
- **Git moves from in-process libgit2 to the `git` CLI** (§8.6). `git` becomes a prerequisite for
  local operations too; it already was for clone/fetch/pull/push and for the Windows terminal (Git
  Bash).

### 1.4 Decisions already taken with the operator (2026-09-16)

| Question | Decision |
|---|---|
| How self-contained must `code-flow-go/` be? | This file + a verbatim copy of `docs/` + the verbatim prompts, icon assets and test inventory. The renderer is copied at the start of the plan (Phase 0, step 0.3). |
| Replace the existing app or ship a new one? | **Drop-in replacement**: same data directory and database, same keychain entries, same app id, update path 2.7.x → 3.0.0. |
| Scope of the first Go release | **Full feature parity**, delivered in phases. The debugger (`12-debugging.md`) and gRPC stay deferred exactly as today (they answer `unknown command`). |

### 1.5 Verdict

**Proceed.** The five points that could not be proven from a desk were probed on 2026-09-16/17
(`docs/phase0-findings.md`):

| # | Go/no-go point | Result |
|---|---|---|
| 1 | The webview can host the renderer (secure context, anchor positioning, popover, module workers, CSP, bridge errors/events/payloads) | **Pass on both OSes.** `wails://localhost` (macOS 27) and `http://wails.localhost` (WebView2 152) are secure contexts; anchor positioning, popovers and module workers work; zero CSP violations; sentinel errors arrive intact; 20 MiB responses in 137 ms / 543 ms. Remaining: the real renderer end to end (Phase 0.5/0.6) and older Safari (§14 D3). |
| 2 | Go reads 2.7.x keychain items on macOS | **Item shape pass** (the Go query matches the 5 real items without a prompt). The first *data* read will show the keychain password prompt (ad-hoc `cdhash` partition, §5.3) — expected, handled by *Always Allow* or the existing `CREDENTIAL_REFUSED: ` path. On Windows, Go ⇄ C# Credential Manager read/write **pass**. |
| 3 | The Wails NSIS installer replaces a per-user electron-builder install | **Pass**, with 2.7.1 installed **and running**: old install, keys and processes gone; shortcuts point to the new exe; `C:\CodeFlow` intact; uninstalling 3.0 also keeps it. |
| 4 | `xpty` drives Git Bash through ConPTY | **Pass** (interactive login shell, resize, output, `exit`). The reader ends only after `pty.Close()` on both OSes — §8.8 encodes that. |
| 5 | A recursive watcher holds on a large repository | **Pass** on both OSes: 50 000 files, deep changes reported in ≤ 11 ms, 5 000-write bursts delivered without errors, ~2 MB heap. |

Phase 0 is therefore reduced to the steps listed as *still open* in §11.

---

## 2. The current system (as-is)

### 2.1 Process model

```
┌─────────────────────── Electron 44 main process (shell/src/main.ts) ────────────────────────┐
│ BrowserWindow → app://codeflow/index.html   preload: window.codeflow (contextBridge)       │
│ tray · macOS menu · single-instance lock · login-shell PATH · shell.log · permissions       │
│ ipc-client.ts: two sockets "rpc" + "stream", frames = uint32 LE length + UTF-8 JSON  ──┐    │
└────────────────────────────────────────────────────────────────────────────────────────┼────┘
      spawn(codeflow-core, ["--app-version", v], detached: true), token written to stdin    │
┌──────────────────────── codeflow-core (.NET 10, self-contained) ───────────────────────▼────┐
│ IpcListener (unix socket / named pipe) → IpcServer → CommandRegistry (235 handlers)        │
│ SQLite (one connection) · LibGit2Sharp · Porta.Pty · MQTTnet · Npgsql/SqlClient/MySqlConnector │
│ spawns: git · claude · codex · agy · opencode · npx · gh                                    │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
React 19 renderer: lib/bridge/host.ts → window.codeflow.invoke(method, params) / .on(event, fn)
```

### 2.2 Start-up sequence

**Core (`Program.cs`)**

1. `--smoke-test` as first argument → `SmokeTest.RunAsync`, exit `0`/`1`. Three probes: SQLite (temp
   DB, `workspaces` DDL, one-row round trip); LibGit2Sharp (`Repository.Discover` from the app or
   current directory, then status); PTY (`cmd.exe` or `$SHELL`//bin/sh, 120×30 then resize 100×24,
   `echo marker; exit`, 4096-byte reads, 15 s timeout, 5 s wait for exit).
   **Ported, all three** (`backend/app/smoketest.go`): git replaces the libgit2 probe and is
   stricter — `git --version` plus a repository initialised and read back under an isolated `HOME`,
   which is the corporate-laptop failure; storage opens a temp database and runs every migration;
   PTY allocates and releases one without spawning a shell, so the probe does not depend on which
   shell the machine has or on that shell's start-up files. The storage and PTY probes were left
   `TODO` when Phases 2 and 3 landed and their absence outlived the comment saying so — found in
   Phase 9 and fixed, which matters because the release workflow runs this against every installer
   before uploading it.
2. Read the IPC token from **the first line of stdin**; blank → print
   `codeflow-core: the IPC token is read from the first line of stdin…` and exit `2` (BOOT-018b).
3. Run each stage through `Stage(name, work)`, which records a failure to `{base}/logs/startup.log`
   (and stderr, falling back to the temp directory) and rethrows (BOOT-030):
   1. `reset-marker` — if `{base}/.reset-pending` exists, delete the whole base directory; the result
      is ignored (BOOT-001).
   2. `directories` — create `{base}`, `{base}/logs`, `{base}/repos` (BOOT-005).
   3. `scratch-sweep` — `EngineScratch.SweepOrphans(tmp)` deletes AI temp payloads older than 1 h.
   4. `storage` — `Database.Open()` including migrations (BOOT-002, `03-storage.md`).
4. Build long-lived services: `IpcListener`, `IpcServer(registry, token, ErrorLog.Record)`,
   `TerminalRegistry`, `RepoWatcher`, `ApiRegistry`, `StreamRegistry`, `AiRunRegistry`, `GitNetwork`,
   and **one shared `HttpClient`**: `TransientRetryHandler` → `SocketsHttpHandler`
   (`PooledConnectionLifetime = 15 min`), `Timeout = 5 min`.
5. Register commands in this order: App, Workspace, Skill, Secret, Terminal, Ai, Activity, Provider,
   Ticket, Review, Git, File, Watcher, Dbml, Api, ApiHttp, ApiStream, Update (`--app-version`,
   default `"0.0.0"`), then `Seal()`.
6. Print `codeflow-core ready <endpoint>` on stdout, then serve.

**Shell (`app.whenReady`)**, in order: `applyLoginShellPath()` (awaited) → `answerPermissions` →
`registerAppProtocol` → `registerBridge` → `installMenu` → `createTray` → `createWindow` →
`await startSidecar()` → `ipc.connect(endpoint)` (15 s, 50 ms retries) → on failure `ipc.markDown(detail)`.
The sidecar is **never restarted**; a dead core is reported as `down`.

### 2.3 The transport being removed

Documented so nobody rebuilds it by accident.

| Aspect | Value |
|---|---|
| Endpoint | macOS `~/CodeFlow/.ipc-<pid>.sock` (stale file deleted, backlog 4, `chmod 0600`); Windows `\\.\pipe\codeflow-<pid>` (`CurrentUserOnly`, `MaxInstances = 4`) |
| Frame | uint32 **little-endian** length + UTF-8 JSON; max 64 MiB |
| Hello (per connection) | `{"channel":"rpc"|"stream","token":"<uuid>"}` within 10 s; token compared in fixed time; wrong token closes silently |
| Request (rpc) | `{"id":1,"method":"get_status","params":{"repoPath":"/x"}}` |
| Response | `{"id":1,"result":{…}}` (void → `"result":null`) or `{"id":1,"error":"<message, verbatim>"}` |
| Event (stream) | `{"event":"terminal:output","payload":{…}}` |
| Concurrency | the pump does **not** await handlers — requests run in parallel, replies match by id |
| Cancellation | none on the wire; per feature: `cancel_ai_run`, `api_cancel_http`, `close_terminal`, `api_stream_disconnect` |
| Errors | `DispatchAsync` catches everything, calls `ErrorLog.Record(method, ex)`, replies `ex.Message` unchanged |
| Shutdown | rpc channel closed → exit 0; shell sends SIGTERM, then SIGKILL after `SIGKILL_GRACE_MS = 3000` |

### 2.4 Sizes

**C# lines by feature** (`wc -l`, 36 679 total, 200 files):

| Folder | LOC | Files | Folder | LOC | Files |
|---|---:|---:|---|---:|---:|
| Providers | 5 704 | 24 | Storage | 1 262 | 6 |
| Ai | 5 482 | 26 (+17 prompts) | Security | 1 010 | 5 |
| ApiClient | 4 322 | 21 | Ipc | 855 | 7 |
| Git | 3 785 | 15 | Update | 826 | 7 |
| Tickets | 3 145 | 14 | Diagnostics | 607 | 4 |
| Review | 2 435 | 7 | Activity | 527 | 4 |
| Workspaces | 2 027 | 14 | Terminal | 419 | 3 |
| Dbml | 1 914 | 17 | Platform | 299 | 4 |
| Files | 1 794 | 10 | `Program.cs` | 266 | 1 |

**Registered commands by C# registration file** (235, counted from the code): Git 47 · Workspace 27 ·
ApiStorage 27 · Provider 19 · Ticket 17 · Ai 13 · File 13 · Review 11 · Skill 10 · Dbml 10 ·
Secret 9 · ApiStream 9 · Activity 7 · ApiHttp 5 · Terminal 4 · Update 3 · Watcher 3 · App 1.

`01-ipc-surface.md` groups some commands under older file names; **the authoritative client list is
the renderer**: 246 distinct command names across `lib/ipc/commands.ts` (200), `lib/ipc/apiCommands.ts`
(43) and `lib/bridge/updater.ts` (3). 246 = 235 registered + 11 that answer
`unknown command '<name>'` (the nine `debug_*` and `api_grpc_call` / `api_grpc_describe`). The Go
contract test (§9.4) is built from the renderer files, not from the document.

The renderer is ~78 000 lines; shell `src/` is 1 903 lines; tests are 27 900 lines of C#, 39 shell
`node --test` cases, 66 renderer `*.test.ts` + 4 `scripts/*.test.mjs`.

### 2.5 Events

Emitted by the core (10 names), all broadcast to every window:

| Event | Producer | Payload (wire names) |
|---|---|---|
| `ai:output` | `Ai/AiRunRegistry` | `{ run_id, stream: "stdout"\|"stderr", line }` — the spec's table writes `runId`; the renderer reads `run_id` (the AI JSON context is snake_case). Keep `run_id`. |
| `git:progress` | `Git/GitNetwork` (stdout and stderr) | `{ op, line }` |
| `git:done` | `Git/GitNetwork` | `{ op, success, message }` — `message` falls back to `git {op} exited with {status}` |
| `terminal:output` | `Terminal/TerminalRegistry` | `{ id, data }` |
| `terminal:exit` | `Terminal/TerminalRegistry` | `{ id }` |
| `repo:fs-changed` | `Files/RepoWatcher` | `{ repo_path }` |
| `skills:progress` | `Workspaces/SkillInstaller` | `{ line }` |
| `api:stream-message` | WebSocket/Socket.IO stream, MQTT connection | `StreamMessage` (`08-api-client.md`) |
| `api:stream-status` | same | `StreamStatusEvent` |
| `update:progress` | `Update/UpdateService` | `{ downloaded, total, done }` — every 256 KiB plus a final `done` |

Listened to with no producer: `debug:paused`, `debug:resumed`, `debug:output`, `debug:terminated`
(deferred debugger). Synthesised by the shell: `codeflow:sidecar-status` `{ status, detail }`.

### 2.6 The Electron shell, exactly

**Window** (`createWindow`): `title: "CodeFlow"`, icon `assets/icon.png`, **1440×900, min 1024×640**,
`show: false` until `ready-to-show`. macOS: `titleBarStyle: "hidden"`,
`trafficLightPosition: {x: 20, y: 22}`. Windows/Linux: `frame: false`. `webPreferences`:
`contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`. **No window-state persistence.**

**Close is not quit** (BOOT-007): `close` → unless `quitting`, `preventDefault()` + `hideToBackground()`.
On macOS in fullscreen it waits for `leave-full-screen` then hides; otherwise hides immediately.
`showWindow()`: recreate if destroyed, `restore` if minimised, `show`, `focus`.

**Quitting** (BOOT-036): every deliberate exit goes through `requestQuit(reason)`, which logs
`[shell] quitting: <reason>` and sets `quitting`. `before-quit` with the flag still down logs a
warning with a stack (the quit came from outside: macOS, a signal, Electron). `SIGTERM`/`SIGINT`/`SIGHUP`
→ `requestQuit("a <sig> from outside the app")`. `uncaughtException`/`unhandledRejection` are logged
and the process keeps running (BOOT-035). `window-all-closed` quits only on non-darwin; `activate`
calls `showWindow`.

**Tray** (BOOT-012): icon `assets/tray.png` (if missing: no tray, silently). Tooltip `CodeFlow`. Menu
`Show CodeFlow` · separator · `Quit CodeFlow` (`requestQuit`). Click → `showWindow`.

**Menu** (BOOT-013/014/015): non-darwin → `Menu.setApplicationMenu(null)`. darwin:
- **CodeFlow**: about · separator · hide · hideOthers · unhide · separator · custom **`Quit CodeFlow`
  (`Command+Q`) → `requestQuit`** — never the predefined quit role, or ⌘Q would only hide.
- **Edit**: undo · redo · separator · cut · copy · paste · selectAll — without it ⌘C/⌘V/⌘X/⌘A never
  reach the webview.
- **Window**: minimize · zoom · close.

**Single instance**: `requestSingleInstanceLock()`; losing → `app.quit()`; `second-instance` → `showWindow`.

**Navigation**: `will-navigate` blocked unless the URL starts with `app://codeflow/` (or the dev URL);
`setWindowOpenHandler` → `openExternal(url)` + `{action: "deny"}`. **Context menu**
(`installContextMenu`): cut/copy/paste roles when applicable, separator, selectAll.

**`app://` protocol** (`app-protocol.ts`): scheme privileges `standard, secure, supportFetchAPI,
stream`; `/` → `index.html`; `isWithinRoot` via `path.relative`, else 404; streamed files; CSP added
to HTML responses only:

```
default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; worker-src 'self'; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'
```

**Permissions** (BOOT-029): both handlers grant only `clipboard-sanitized-write`; everything else
(including `clipboard-read`) is refused.

**Login-shell PATH** (`login-path.ts`, not in the spec — transcribed in §15.2.4): macOS GUI apps do not
inherit the shell `PATH`, so the shell captures it before spawning anything.

**Shell log** (BOOT-031): `{base}/logs/shell.log`, line `ISO-8601  LEVEL(padEnd 5)  message`, 2 MiB then
one `.1` rollover, synchronous, never throws, same redaction as `ErrorLog` (§15.2.2), mirrored to console.

**Bridge channels** (`registerBridge`):

| Channel | Behaviour |
|---|---|
| `codeflow:invoke` | `(method, params)` → `ipc.invoke` |
| `codeflow:sidecarStatus` | `{...ipc.state, logsDirectory}` |
| `codeflow:window` | `"minimize" \| "toggleMaximize" \| "close" \| "isMaximized"`; unknown → `false` |
| `codeflow:setTheme` | `nativeTheme.themeSource = theme ?? "system"` |
| `codeflow:openExternal` | http/https only, else throws; `shell.openExternal` |
| `codeflow:openLogs` | `mkdir -p` logs dir, `shell.openPath`; non-empty error string → throw |
| `codeflow:clipboardWrite` | rejects non-strings; `clipboard.writeText` (async in Electron 44) |
| `codeflow:dialog` | `(kind: "openFile"\|"openDirectory"\|"save", options)`; save → `filePath` or `null`; open → `filePaths[0]` or `null` |
| `codeflow:quit` | `(reason?)` → `requestQuit("the renderer asked (…)")` |
| `codeflow:event` (main → renderer) | `(name, payload)` for 13 forwarded names + `codeflow:sidecar-status`. **`update:progress` is not in the forward list.** |

**Preload API** (`window.codeflow`): `invoke<T>(method, params?)`, `on(event, handler) → unsubscribe`,
`platform()` (synchronous), `window.{minimize, toggleMaximize, close, isMaximized, setTheme}`,
`dialog.{openFile, openDirectory, save}(options?) → Promise<string|null>`, `openExternal(url)`,
`clipboardWrite(text)`, `openLogs()`, `sidecarStatus()`, `pathForFile(file)` (`webUtils.getPathForFile`,
Electron-only), `quit(reason)` (non-string → `undefined`, strings capped at 120 chars).

### 2.7 How the renderer couples to the host

- **Only two files read `window.codeflow`**: `lib/bridge/host.ts` and `lib/bridge/webview.ts`.
- `host.ts` (139 lines) defines the `Bridge` type above, `bridge()` (throws
  `"the CodeFlow shell bridge is unavailable — is this running outside Electron?"` per call),
  `unwrapInvokeError` (strips `/^Error invoking remote method '[^']*':\s*/` and then one `/^Error:\s+/`),
  `invoke()` (rethrows `new Error(unwrapInvokeError(msg), {cause})`), `listen()` (returns
  `Promise<UnlistenFn>`), and `host` with 9 members.
- Other bridge files: `dialog.ts` (`open` → `string|string[]|null`, `save` → `string|null`; callers
  test `typeof result === "string"`, so **cancel must be `null`**), `shell.ts` (`openUrl`, `platform`,
  `getCurrentWindow`), `updater.ts` (§2.9), `webview.ts` (Tauri-style drag events rebuilt from DOM events
  + `pathForFile`; only consumer `components/api/ImportModal.tsx`).
- `components/layout/WindowControls.tsx` calls `getCurrentWindow()` **at module scope** — the bridge
  must exist before `main.tsx` evaluates.
- **Wire naming** (a contract, `.claude/rules/dotnet.md` in the source repo): command names
  `snake_case`; **top-level params `camelCase`** (`{ repoPath, filePath, workspaceId }`); nested domain
  objects and **every returned payload and event `snake_case`**, except the CamelCase JSON contexts
  listed in §8.1.4. Optional params are sometimes `?? null` and sometimes `undefined` (key omitted) —
  the backend must treat *missing* and *null* the same.
- **Binary data**: `writeFileBytes(path, contents)` sends `contents` as a **JSON array of numbers**
  (`Array.from(Uint8Array)`), not base64.
- **Error sentinels** matched as substrings of `Error.message` (12): `CHECKOUT_CONFLICT: `,
  `QUOTA_EXCEEDED::`, `AUTH_EXPIRED::`, `RUN_CANCELLED::`, `RUN_TIMED_OUT::`, `CREDENTIAL_REFUSED: `,
  `SELF_APPROVAL: `, `STALE_REVIEW: `, `TICKET_NOT_LINKED: `, `TICKET_SYNC_FAILED: `,
  `NOTHING_TO_ANALYZE: `, `DB_CONNECTION_REFUSED: ` (trailing spaces are part of them; `XLANG-002/003/012–018`).
- **Types**: `types/domain.ts` (59 interfaces, snake_case), `types/api.ts` (57 interfaces; snake_case on
  the wire, camelCase only inside client-side JSON blobs such as `AuthConfig`). `T | null` is the norm;
  int64 values are plain `number`.
- **Tests never stub `window.codeflow`**: they `vi.mock` `lib/ipc/commands`, `lib/ipc/events`,
  `lib/ipc/apiCommands`. Only `lib/bridge/host.test.ts` (8 cases on `unwrapInvokeError`) touches the bridge.
- IPC wrappers are imported by 88 files (`commands`), 25 (`apiCommands`) and 12 (`events`).

### 2.8 Build and release pipeline

**`scripts/build-app.sh mac|win [electron-builder flags]`**: RID `osx-arm64` / `win-x64` (mac refuses
off Darwin) → `pnpm -C renderer install --frozen-lockfile && build` → same for `shell` →
`rm -rf shell/build` → `dotnet publish src/CodeFlow.App/CodeFlow.App.csproj -c Release -r $rid
--self-contained true -p:PublishSingleFile=false -o shell/build/core` → `cp -R renderer/dist
shell/build/renderer` → delete this platform's old installers → `electron-builder --config
electron-builder.yml --$target`.

**`scripts/build-dmg.sh`**: Darwin only (exit 65) → `build-app.sh mac` → expects
`dist-installers/CodeFlow-${version}-arm64.dmg` (exit 70 if missing) → `shasum -a 256` from inside that
directory → `.dmg.sha256` → prints the path.

**`scripts/publish-release.sh vX.Y.Z`**: tag must match `^v\d+\.\d+\.\d+` (64) and equal
`shell/package.json` with a clean tree (65) → `build-dmg.sh` → `git tag` + push → `gh release create
<tag> --title <tag> --generate-notes` if missing → `gh release upload <tag> dmg dmg.sha256 --clobber`.

**`scripts/release.sh [major|minor|patch|X.Y.Z] [--fast] [--dry-run]`**:
1. Guards: Darwin, `gh auth status`, on `main`, clean, `HEAD == origin/main`, tag not on local or remote.
2. CI check via `gh api …/commits/<sha>/check-runs` — all completed with success/skipped/neutral; none
   at all allowed only with `--fast`. `--dry-run` stops here.
3. Local suite (unless `--fast`): `dotnet build` + `dotnet test` (Release); shell install/test/`audit
   --audit-level moderate`; renderer install/typecheck/test/audit.
4. Bump (skipped when already there): `pnpm -C shell|renderer version X --no-git-tag-version
   --no-git-checks`; commit `chore(release): vX` (only the two `package.json`); push `main`.
5. `publish-release.sh vX`.
6. Find the `ci` run for the bump sha (20 tries × 3 s) → `gh run watch --exit-status`.
7. Verify assets: `CodeFlow-X-arm64.dmg` + `.sha256`, `CodeFlow-Setup-X-*.exe` + `.sha256`.

**`scripts/build-icons.sh`** (macOS: `qlmanage`, `sips`, `iconutil`, `pack-ico.py` writing a
PNG-compressed ICO: `ICONDIR <HHH`, `ICONDIRENTRY <BBBBHHII`, 256 stored as 0).

**`.github/workflows/ci.yml`** (`name: ci`; `push: main` + `workflow_dispatch`; never on pull requests;
`concurrency: ${{github.workflow}}-${{github.ref}}`, `cancel-in-progress: false`;
`permissions: contents: write`; every action pinned by SHA; **no tests, lint or audit**):

| Job | Runner | Timeout | Does |
|---|---|---|---|
| `gate` | ubuntu-latest | 5 min | reads `shell/package.json` version (`^\d+\.\d+\.\d+$`), requires renderer's to match; output `publish=true` when `gh release view v$version` fails — **a version bump is the act of releasing** |
| `draft` | ubuntu-latest | 5 min | if publish: `gh release create v$VERSION --draft --target $SHA --title v$VERSION --generate-notes` |
| `installers` | matrix macos-latest/`mac`, windows-latest/`win`; `fail-fast: false` | 45 min | checkout, setup-dotnet (global.json, cache on `packages.lock.json`), pnpm, node 24; `build-app.sh <target>` with `NODE_OPTIONS=--max-old-space-size=6144`; `sha256sum` every `*.dmg`/`*.exe`; `gh release upload … --clobber` |
| `publish` | ubuntu-latest | 10 min | requires assets `\.dmg$`, `\.dmg\.sha256$`, `-Setup-.*\.exe$`, `-Setup-.*\.exe\.sha256$`; `gh release edit --draft=false --latest` |

`dependabot.yml`: weekly — nuget `/`, npm `/renderer`, npm `/shell`, github-actions `/`.

**`shell/electron-builder.yml`**: `appId: com.codeflow.app`, `productName: CodeFlow`,
`copyright: Gastón Lara P.`, output `../dist-installers`; `extraResources` `build/core → core`,
`build/renderer → renderer`; fuses `runAsNode:false`, `enableNodeOptionsEnvironmentVariable:false`,
`enableNodeCliInspectArguments:false`, `grantFileProtocolExtraPrivileges:false`,
`resetAdHocDarwinSignature:true`. **macOS**: category `public.app-category.developer-tools`, targets
`dmg` + `zip`, **arm64 only**, `identity: null`, `hardenedRuntime: false` (unsigned, not notarised),
`dmg.writeUpdateInfo: false`. **Windows**: targets `nsis` + `portable`, **x64 only**;
`win.artifactName: CodeFlow-Setup-${version}-${arch}.${ext}`;
`portable.artifactName: CodeFlow-Portable-${version}-${arch}.${ext}`; `nsis`: `oneClick:false`,
`perMachine:false`, `allowToChangeInstallationDirectory:true`, `deleteAppDataOnUninstall:false`.
No `publish`, file associations, protocols, notarisation, or Linux targets.

### 2.9 The updater, end to end

- **Renderer** (`state/updateStore.ts`): hourly check (`CHECK_INTERVAL_MS = 1h`) + manual; statuses
  `idle | checking | uptodate | available | downloading | ready | error`; errors shown only for manual
  checks; a `check()` failing in a plain dev server is swallowed.
- **Bridge** (`lib/bridge/updater.ts`): `getVersion()` → `update_current_version`; `check()` →
  `update_check` returning snake_case `Availability { available, current_version, version, notes, date,
  asset_name, asset_url, asset_size, install_kind: "auto"|"manual", reason }`; not available with a `reason` → throws
  `UpdateCheckError(reason)`, reasons `no-credential | unauthorized | no-release | no-asset | unreachable`;
  download subscribes to `update:progress` **before** `invoke("update_download", {assetUrl, assetName})`
  and maps it to `Started/Progress/Finished`; `relaunch()` → `host.quit(...)` (quits, does not relaunch).
- **Core** (`Update/`):
  - Feed `GET https://api.github.com/repos/gastonlarap-a11y/code-flow/releases/latest`, headers
    `Authorization: Bearer <token>`, `Accept: application/vnd.github+json`,
    `X-GitHub-Api-Version: 2022-11-28`, `User-Agent: CodeFlow/<version>`.
  - Token: keychain `github-token:github.com`, else `gh auth token` (5 s timeout), else `no-credential`
    (still required although the repository is public).
  - 401/403 → `unauthorized`; other non-2xx or a draft → `no-release`.
  - `ReleaseVersion.IsNewer`: strip leading `v`, ignore `+build`, numeric segments (missing = 0),
    pre-release ranks below the final release.
  - `UpdateAssets.For`: Windows `.exe` preferring names containing `-Setup-` (else the first); others
    `.dmg`; never `.blockmap`. `InstallKind`: `auto` on Windows, `manual` elsewhere.
  - Download: re-fetch `/releases/latest`, find `<assetName>.sha256` (exact, case-insensitive), download
    it with `Accept: application/octet-stream`; digest file with one entry → use it without checking the
    name; several → match last path segment, strip GNU `*`. Download the asset to
    **`~/Downloads/<assetName>`** in 81 920-byte chunks, `update:progress` every 256 KiB + final
    `done`; SHA-256 compared in fixed time; **mismatch deletes the file**; no digest → refused (BOOT-021).
  - Hand-off: Windows starts the installer with `Process.Start(path) { UseShellExecute = true }`;
    macOS calls `FileOps.RevealInFileManager(path)`, which is `ShellOpen(path)` — i.e. `open <dmg>`:
    it **mounts** the image, it does not reveal it in Finder.
  - **Nothing quits the app during the hand-off.** The command returns, `updateStore` switches to
    `ready` and waits for the user's *Restart*, and `relaunch()` only quits. So on Windows the NSIS
    installer of the next version starts **while `CodeFlow.exe` and `codeflow-core.exe` are still
    running** — the 3.0.0 installer has to deal with that (§10.3, verified in
    `docs/phase0-findings.md` W-2/W-3).

### 2.10 Tests

- `tests/CodeFlow.Tests` — xUnit v3 (`xunit.v3.mtp-off` 4.0.0), **135 files, 1 101 `[Fact]` + 124
  `[Theory]`**. Files per folder: Activity 3 · Ai 17 · ApiClient 9 · Dbml 5 · Diagnostics 2 · Files 9 ·
  Git 17 · Ipc 5 · Platform 2 · Providers 16 · Review 10 · Security 3 · Storage 1 · Terminal 3 ·
  TestVectors 2 · Tickets 13 · Update 4 · Workspaces 12.
- Hand-written fakes, no mocking framework; test names are sentences with underscores.
- `SerialKeychain` collection (real OS keychain, no parallelism): CredentialStore, UpdateDownload,
  ProviderIpc, ReviewPosting*, ReviewFromLink, ReviewRun; temp secrets via `TempGitHubToken`/`TempAdoPat`.
- `SerialTemporaryFiles` collection: UnifiedPatch, TerminalSession, RepoWatcher.
- `TestVectors/FixtureCatalog`: walks up from the test binary to `docs/business-rules/test-vectors`,
  loads `*.vectors.json` (object or array of `{ $schema, sourceFile, kind: vector|scenario,
  setup.seedSql, cases[{ id, name, input, expected, notes }] }`), **comments allowed** (only
  `search.vectors.json` uses them).
- Real sockets: IPC tests (unix socket / named pipe), loopback `HttpListener` for HttpSend/StreamCommands.
  Real `git`: Stash, Identity. Processes: BinaryDiscovery.
- Live Azure DevOps tests skip unless `CODEFLOW_E2E_ADO_ORG` / `CODEFLOW_E2E_ADO_PROJECT` are set.
- Shell: 39 `node --test` cases (app-protocol, ipc-client, login-path, permissions, shell-log).
- Renderer: Vitest, `environment: "node"`, no jsdom; `scripts/` tests guard i18n parity (1 669 keys per
  locale), density CSS, theme contrast (AA), UI conventions.

### 2.11 Drift found while analysing (fix nothing silently; carry the knowledge)

| Finding | Consequence for the port |
|---|---|
| `.github/workflows/release.yml` is cited by scripts, a skill and BOOT-021 but **does not exist**; `ci.yml` is the only workflow. | Port `ci.yml`; correct the citations in §13. |
| `installer/hooks.nsh` (the keep/wipe uninstall prompt, a second hardcoding of `C:\CodeFlow`) **does not exist**; electron-builder sets `deleteAppDataOnUninstall: false`. There is no wipe prompt today. | Do not "port" a prompt that is not there. |
| Electron never forwards `update:progress`, so the download bar stays at 0 % until the call returns. | Fixed by construction in Wails (events reach the renderer). Record as a `DIVERGENCE` (§13). |
| `window.codeflow.window.setTheme` and `isMaximized` are exposed, never called. | Do not port them. |
| `CHANGELOG.md` stops at v1.9.1 while the app is at 2.7.1. | Start a fresh changelog at 3.0.0 or drop it (§14). |
| AGENTS.md says CI restores with `--locked-mode`; nothing does. | Irrelevant after the port (Go uses `go.sum` + `-mod=readonly`). |
| `02-bootstrap-platform.md` still carries Tauri 1.7.2 details (tray id `main-tray`, Services menu item, `titleBarStyle: "Overlay"`, `latest.json` + minisign, `security.csp null`). Code differs (§2.6). | §2.6 is the ground truth for the port; update doc 02 in Phase 1. |
| `01-ipc-surface.md` lists `ai:output` as `{ runId, … }`; the renderer reads `run_id`. | Emit `run_id`. |
| `UpdateCredential` still requires a token although releases are public. | Preserve (behaviour); candidate change listed in §14. |

---

## 3. Target architecture (to-be)

### 3.1 Process model

```
┌──────────────────────────── CodeFlow (Go 1.27, one process) ────────────────────────────┐
│ main.go — composition only                                                              │
│ Wails v3 application                                                                    │
│  ├─ WebviewWindow "main" ── frontend/dist embedded (//go:embed)                         │
│  ├─ Service bridge.Service ── Invoke(ctx, method, params) ──► bridge.Registry (235)     │
│  ├─ Event bus ── app.Event.Emit(name, payload) ──► renderer Events.On(name)             │
│  └─ backend/desktop: close→hide · tray · macOS menu · single instance · dialogs ·       │
│                      clipboard · open URL / logs · file drop · quit reasons             │
│ backend/<feature>: storage (1 connection) · git (CLI) · terminal (xpty) · ai · …       │
│ spawns: git · claude · codex · agy · opencode · npx · gh   (each in its own group)      │
└─────────────────────────────────────────────────────────────────────────────────────────┘
React renderer: frontend/src/lib/bridge/host.ts → generated binding Invoke(...) / Events.On(...)
```

No sidecar, no socket, no token, no framing. The renderer's calls reach Go through the Wails runtime
(an in-process message handler), and Go's events reach the renderer through the same channel.

### 3.2 Repository layout

The layout follows the operator's `template-wails-go` (the generator that emits Wails v3 projects):
`main.go` is the only orchestration point, domain code lives under `backend/<domain>`, the web app
under `frontend/`, build scaffolding under `build/`. One Go package per C# feature folder keeps the
source repo's rule "a feature must be findable in one place".

```
code-flow-go/
├── main.go                  # composition only (§3.9)
├── go.mod / go.sum          # go 1.27.0, toolchain go1.27.1, exact versions (§4)
├── Taskfile.yml             # root tasks; includes build/<os>/Taskfile.yml
├── build/
│   ├── config.yml           # wails3 build config: product name, identifier, version, icons
│   ├── appicon.png          # from docs/verbatim/assets/icon.png
│   ├── darwin/              # Info.plist(.tmpl), icons.icns, Taskfile.yml (generated by wails3)
│   └── windows/             # icon.ico, info.json, wails.exe.manifest, nsis/project.nsi, Taskfile.yml
├── backend/
│   ├── app/                 # start-up stages, StartupState, smoke test
│   ├── bridge/              # Registry, Service.Invoke, Params, jsonwire, Emitter interface
│   ├── desktop/             # window options, close→hide, tray, menu, dialogs, clipboard, open URL/logs, drop, quit
│   ├── platform/            # AppPaths, reset marker, login-shell PATH, TransientNetwork, retry transport
│   ├── diagnostics/         # ErrorLog, StartupLog, ShellLog, Redact, smoke probes
│   ├── storage/             # Open, Schema, Migrations, Clock, SeededPromptHistory
│   ├── security/            # CredentialStore (+ keychain_darwin.go, credman_windows.go, unsupported.go), SecretScan
│   ├── workspaces/          # workspaces, projects, settings, prompts, agents, MCPs, contexts, skills
│   ├── activity/            # activity_log, job_history, conversation titles
│   ├── git/                 # CLI runner, status, diff, branches, stash, merge, identity, checkpoints, network
│   ├── files/               # FileOps, path guards, repo walk, search/replace, watcher
│   ├── terminal/            # registry, shell resolver
│   ├── ai/                  # routing, run registry, runner, signals, discovery, engines/, prompts/ (embedded)
│   ├── providers/           # github/, azure/, prlink, known hosts, repo detection
│   ├── review/              # run, memory (reconcile), posting, store
│   ├── tickets/             # sync, mirror, html→markdown, review, verdict, comment
│   ├── apiclient/           # http send, decoding, digest, sigv4, websocket, socketio, mqtt, stores
│   ├── dbml/                # introspect/ (postgres, sqlserver, mysql, sqlite), layouts, connections, assistant
│   ├── update/              # check, assets, download + verify, hand-off
│   └── shared/              # proc (spawn, groups, tree kill), safego, sentinel, testvectors, httpfake
├── frontend/                # the renderer (copied in Phase 0); no generated bindings needed (§7.3)
├── docs/                    # the specification (already here)
├── scripts/                 # release.sh, publish-release.sh (ported), build-icons.sh
└── .github/workflows/ci.yml
```

**Dependency direction** (same as the source repo): features → `storage` / `bridge` interfaces /
`shared`; `bridge` knows no feature. Each feature exposes
`func Register(r *bridge.Registry, deps Deps)` — the Go equivalent of `Add…Commands(…)`. **Features
never import Wails**: they receive a `bridge.Emitter` interface and small `desktop` interfaces
(dialogs, open URL), so every feature is testable without a window.

### 3.3 The one pattern that holds the port together: a command registry behind one bound method

**Pattern**: Command (a name → handler registry) exposed through a single Wails service method.

**Why not the Wails-idiomatic "one service per domain, one bound method per command"**: the renderer
already owns 246 typed wrappers over `invoke(name, params)` imported by 88 + 25 + 12 files, the test
vectors and the spec are keyed by command name, and error strings with sentinel prefixes are part of
the contract. Per-method bindings would change every call site's shape (positional arguments instead of
a camelCase object), regenerate TypeScript that competes with `types/domain.ts`, and make a
byte-identical wire contract hard to prove. One `Invoke(method, params)` keeps **the renderer's 246
wrappers untouched** and lets a single contract test (§9.4) prove coverage.

```go
// backend/bridge/registry.go
package bridge

// Handler runs one command. The returned value is marshalled with its own struct tags (§3.4);
// a nil value reaches the renderer as null, exactly like the C# "void" commands did.
type Handler func(ctx context.Context, p Params) (any, error)

type Registry struct {
	handlers map[string]Handler
	sealed   bool
}

func NewRegistry() *Registry { return &Registry{handlers: make(map[string]Handler)} }

// Add panics on a duplicate or a late registration: both are programming errors caught at start-up.
func (r *Registry) Add(name string, h Handler) {
	if r.sealed {
		panic("bridge: registry is sealed; cannot add " + name)
	}
	if _, dup := r.handlers[name]; dup {
		panic("bridge: duplicate command " + name)
	}
	r.handlers[name] = h
}

func (r *Registry) Seal() { r.sealed = true }
```

```go
// backend/bridge/service.go
type Service struct {
	registry *Registry
	record   func(method string, err error) // diagnostics.ErrorLog.Record in main.go; nil in tests
}

// Invoke is the only method bound to the frontend.
func (s *Service) Invoke(ctx context.Context, method string, params json.RawMessage) (out json.RawMessage, err error) {
	defer func() {
		if p := recover(); p != nil { // a handler panic must not take the app down (§3.8)
			err = fmt.Errorf("internal error in %s: %v", method, p)
			s.recordFailure(method, err)
		}
	}()
	h, ok := s.registry.handlers[method]
	if !ok {
		return nil, fmt.Errorf("unknown command '%s'", method)
	}
	result, err := h(ctx, NewParams(params))
	if err != nil {
		s.recordFailure(method, err)
		return nil, err // the message crosses unchanged — sentinel prefixes live at its start
	}
	return jsonwire.Marshal(result)
}
```

**Params** mirror the C# `Arg / OptionalArg / Number` helpers. In the C# code these helpers live in
each feature's `*Commands.cs` (not in `IpcServer`), and a few call sites pass a literal name (for
example `AiCommands` reports `'runId'`); the text is always `missing required parameter '<name>'` —
verified against the installed 2.7.1 core (`docs/phase0-findings.md`, M-3). Missing and `null` are
the same thing (the renderer sends both):

```go
// Package-level generics because Go methods cannot take type parameters.
func Arg[T any](p Params, name string) (T, error)          // missing/null → fmt.Errorf("missing required parameter '%s'", name)
func OptionalArg[T any](p Params, name string) (*T, error)  // missing/null → nil, nil
```

### 3.4 JSON wire rules (each one is a silent-failure trap)

| Rule | Why | How |
|---|---|---|
| Every returned struct field has an explicit `json:"snake_case"` tag | Returned payloads and events are snake_case; a wrong name compiles and renders as `undefined` | Struct tags only; no `omitempty` on fields the renderer reads as `T \| null` |
| These C# JSON contexts were **camelCase** and stay so: Ipc envelope (gone), `SecretCommands`, `StreamRegistry`, Azure REST DTOs, `AzureWorkItem`, `TerminalRegistry`, `SkillInstaller` | Wire contract as shipped | Tag those structs in camelCase; §9.4 golden files pin them |
| Nullable → pointer (`*string`, `*int64`), marshalled as `null` | C# Git context emits explicit `null` | Never `omitempty` on them |
| **Slices are never `nil` in a response** | Go marshals a nil slice as `null`; the renderer calls `.map` on it and crashes | Build with `make([]T, 0, n)`; a reflection test (`jsonwire.AssertNoNilSlices`) runs over every golden response |
| `writeFileBytes.contents` arrives as a JSON **array of numbers** | Go's `[]byte` expects base64 | `type ByteArray []byte` with `UnmarshalJSON` accepting `[0..255,…]` only |
| int64 as plain JSON numbers | Renderer types them as `number` | Default `encoding/json` |
| Timestamps are the strings storage returns | Stored TEXT, sorted as strings (§5.2) | Never re-format through `time.Time` on the way out |
| `SetEscapeHTML(false)` in `jsonwire.Marshal` | Go escapes `<`, `>`, `&` as `\u003c…`; harmless for `JSON.parse` but ugly in stored JSON and exported files | One encoder helper used everywhere |
| Spanish field names in review findings (`tipo`, `categoria`, `archivo`, `lineas`, `confianza`, `estado`, `introducido_en_iter`, `resuelto_en_iter`, `motivo_descarte`) | Stored in `review_runs.findings` by 2.x and read back by 3.0 | Copy names from `07-review-pipeline.md`, never translate |
| **`Invoke` never returns an untyped `nil`** | Wails' HTTP transport writes `{}` for a `nil` result (`transport_http.go`, `json(nil)`), so a void command would resolve to `{}` instead of `null` — **measured** (M-1: `UntypedNil` → `"{}"`, `RawMessage("null")` → `null`) | `jsonwire.Marshal(nil)` returns `json.RawMessage("null")`; a bridge test asserts it |
| Request bodies are capped at **64 MiB** | The runtime sends bodies above 1 MiB in chunks and rejects an assembled body over 64 MiB with `Invalid runtime call: assembled body too large` — **measured** (M-1: a 70 MiB request was rejected; 5 MiB took 79 ms). Same ceiling as the 64 MiB frame of the transport being removed | Nothing to do for parity; `write_file_bytes` of a large image as a number array (~3–4 bytes of JSON per byte) is the case that could reach it |

### 3.5 Errors

- **Sentinels live in one package**, `backend/shared/sentinel`, as constants copied from §2.7 and
  `13-cross-language-contracts.md`, each with a test asserting its exact bytes.
- **In-process callers branch on types, never on strings** (as C# did): `*azure.Error{Status,
  Unauthorized}`, `*github.Error{SelfApproval}`, `*dbml.ConnectionError`, `*ai.RunFailedError`…,
  checked with `errors.As` / Go 1.26+ `errors.AsType[*T](err)`.
- **Sentinels are applied at the command boundary**, not at the throw site (`XLANG-012`): the handler
  inspects the typed error and returns `errors.New(sentinel.CredentialRefused + msg)`.
- **Never wrap a sentinel-bearing error** with `fmt.Errorf("…: %w")` before it leaves the handler:
  `NOTHING_TO_ANALYZE: ` and `TICKET_NOT_LINKED: ` are matched with `startsWith` (`XLANG-015/017`).
  One test per sentinel asserts `strings.HasPrefix(err.Error(), sentinel)` through `Service.Invoke`.
- **What the renderer receives** — verified in the source *and* measured. Source path in
  `v3.0.0-beta.23`: `bindings.go` builds a `CallError{Message: err.Error(), Kind: RuntimeError}` →
  `messageprocessor_call.go` wraps it (`Bound method returned an error`) → `transport_http.httpError`
  unwraps back to the `CallError` (`errs.wailsError` implements `Unwrap`) and answers HTTP 422 with its
  JSON → `@wailsio/runtime`'s `runtime.ts` throws `new RuntimeError(json.message)`. Measured on macOS 27
  (M-1): `errors.New("NOTHING_TO_ANALYZE: No hay cambios sin commitear para analizar")` arrived as a
  `RuntimeError` with exactly that `message`, `startsWith("NOTHING_TO_ANALYZE: ")` true; an unknown
  command arrived as `unknown command 'nope'`. The Wails wrapper text only appears in Wails' own log
  line (`ERR Binding call failed: Bound method returned an error: …`), never in `message`. Several
  errors would be joined with `errors.Join`; a panic would surface as `"<package.Type.Method>: panic: …"`
  — which is why `Invoke` recovers itself.
- `ErrorLog.Record(method, err)` runs inside `Invoke` for every failure, exactly where
  `IpcServer.DispatchAsync` did it. Line format stays `yyyy-MM-dd HH:mm:ss zzz  method  Type: message`;
  `Type` becomes the Go dynamic type without its package path (the log is diagnostics, not a contract).

### 3.6 Events

```go
// backend/bridge/emitter.go — what features see
type Emitter interface{ Emit(name string, payload any) }

// backend/desktop/emitter.go — the Wails adapter
func (e *WailsEmitter) Emit(name string, payload any) { e.app.Event.Emit(name, payload) }
```

With exactly one data argument, `app.Event.Emit` puts the payload in `CustomEvent.Data` (verified in
`event_manager.go`), so the renderer reads `event.data`. Events are broadcast; there is one window, and
payloads already carry their own id (`run_id`, `id`, `repo_path`). An event emitted before the renderer
subscribes is lost — same as today.

### 3.7 Concurrency and cancellation

- Each binding call runs concurrently — **measured** (M-1: two calls that each sleep 1 s finished
  together in 1 002 ms). Handlers must be safe for parallel use, as the C# ones were. Wails derives
  each call's context with `context.WithCancel(context.WithoutCancel(...))` and cancels it when the call
  returns (`messageprocessor_call.go`), which is what the next bullet depends on.
- **Storage**: one `*sql.DB` with `SetMaxOpenConns(1)` wrapped in `storage.DB` whose `Read(ctx, fn)`
  and `Write(ctx, fn)` serialise through a `sync.Mutex` — the `SemaphoreSlim(1,1)` equivalent
  (`STORE-001`).
- **Call context vs. lifetime context**: the `ctx` Wails passes is cancelled when the call ends. Work
  that must outlive the call — AI runs, WebSocket/Socket.IO/MQTT streams, terminals, watchers, git
  network operations — runs under an **app-lifetime context owned by its registry**, cancelled in
  `OnShutdown`. Passing the call context to a stream connector would close the stream the moment
  `api_ws_connect` returns.
- Cancellation stays per feature (`cancel_ai_run`, `api_cancel_http`, `close_terminal`,
  `api_stream_disconnect`); git network operations stay uncancellable (`AMBIGUOUS-GIT-b`), but are
  killed on quit.

### 3.8 What disappears, and how each start-up/shell rule is reinterpreted

| Rule | Today | In Go + Wails |
|---|---|---|
| BOOT-018b token on stdin | shell → core handshake | **Retired** — no child process. |
| BOOT-034 IPC endpoint path | pipe/socket address | **Retired.** |
| BOOT-035 no emitter may end the app | error listeners on sockets and pipes; `uncaughtException` logged | **Stronger in Go**: an unrecovered panic in *any* goroutine terminates the process. Every goroutine is started through `shared/safego.Go(name, fn)`, which recovers, logs to `errors.log`/`shell.log` and never re-panics. Enforced by a lint rule (§10.1: `go ` statements outside `safego` fail review). |
| BOOT-036 every quit names who asked | `requestQuit(reason)` | `desktop.RequestQuit(reason)` writes `[shell] quitting: <reason>` to `shell.log`, sets the quitting flag, calls `app.Quit()`. A quit that did not come through it (Dock *Quit*, logout, a signal) reaches `Options.ShouldQuit` — macOS' `applicationShouldTerminate` calls it before cleanup (`application_darwin_delegate.m`) — which logs it with the flag still down and returns `true`. `app.Quit()` destroys the app directly and does **not** go through the window's `WindowClosing` hook, so the close-to-tray interception never blocks a real quit. |
| BOOT-037 core leads its own process group | the *core* is detached from Electron | **Inverted**: the app is now the parent of every CLI. Children are spawned in **their own process group** (`SysProcAttr{Setpgid: true}` on Unix, `CREATE_NEW_PROCESS_GROUP` on Windows) so a group-wide signal raised inside an AI CLI tree cannot reach the app, and are killed as a tree (`kill(-pgid)` / Job Object). §8.0. |
| BOOT-030 start-up failure recorded | `startup.log` per stage | Same file, same stage names (`reset-marker`, `directories`, `scratch-sweep`, `storage`). |
| BOOT-031 shell log | Electron main writes `shell.log` | `backend/diagnostics.ShellLog` writes the same file, same format and redaction. |
| BOOT-032 renderer told the core is down | socket state + banner | A failed start-up stage no longer prevents the window from opening: `app.StartupState` records it, `host.sidecarStatus()` answers `{status: "down", detail, logsDirectory}`, and every storage-backed command returns that failure. On success it answers `{status: "ready", logsDirectory}`. `SidecarBanner` keeps working unchanged. |
| BOOT-033 clipboard through the shell | `clipboard.writeText` | `app.Clipboard.SetText(text)` (returns `bool`; `false` → error). `openLogs` → `app.Env.OpenFileManager(logsDir, false)` after `MkdirAll`. |
| BOOT-029 one web permission | Electron permission handlers | `WebviewWindowOptions.Permissions` (`map[PermissionType]Permission`; kinds `PermissionMicrophone`, `PermissionCamera`, `PermissionGeolocation`, `PermissionNotifications`, `PermissionClipboardRead`) set to `PermissionDeny` for all five; clipboard *write* goes through Go (`Clipboard.SetText`, measured round-trip in M-1). In WKWebView a page `navigator.clipboard.writeText` without a user gesture is refused with `NotAllowedError` (measured), which is one more reason for W5. |

### 3.9 Composition in `main.go`

Sketch against the verified `v3.0.0-beta.23` API; field names re-checked in Phase 0, step 0.4.

```go
package main

//go:embed all:frontend/dist
var assets embed.FS

// Set by the build: -ldflags "-X main.version=3.0.0"
var version = "0.0.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--smoke-test" {
		os.Exit(app.RunSmokeTest())
	}

	paths := platform.DefaultPaths() // C:\CodeFlow or ~/CodeFlow (BOOT-003)
	shellLog := diagnostics.NewShellLog(paths.Logs())
	platform.ApplyLoginShellPath(shellLog) // macOS only, before anything spawns (§15.2.4)
	startup := app.RunStages(paths)        // reset-marker → directories → scratch-sweep → storage

	emitter := desktop.NewEmitter()
	registry := bridge.NewRegistry()
	deps := app.NewDeps(paths, startup, emitter) // db, http client, registries, credential store…
	// Same order as the C# composition (§2.2, step 5).
	platform.Register(registry, deps); workspaces.Register(registry, deps); workspaces.RegisterSkills(registry, deps)
	security.Register(registry, deps); terminal.Register(registry, deps); ai.Register(registry, deps)
	activity.Register(registry, deps); providers.Register(registry, deps); tickets.Register(registry, deps)
	review.Register(registry, deps); git.Register(registry, deps); files.Register(registry, deps)
	files.RegisterWatcher(registry, deps); dbml.Register(registry, deps); apiclient.Register(registry, deps)
	update.Register(registry, deps, version)
	registry.Seal()

	wapp := application.New(application.Options{
		Name:        "CodeFlow",
		Description: "Code review and API workbench",
		Services: []application.Service{
			application.NewService(bridge.NewService(registry, diagnostics.NewErrorLog(paths.Logs()).Record)),
			application.NewService(desktop.NewHostService(startup, paths)), // sidecarStatus, dialogs, clipboard, openExternal, openLogs, quit
		},
		Assets:                      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:                         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Windows:                     application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance:              &application.SingleInstanceOptions{UniqueID: "com.codeflow.app", OnSecondInstanceLaunch: desktop.ShowMainOnSecondInstance},
		DisableDefaultSignalHandler: true,                  // desktop installs its own, which names the signal (BOOT-036)
		ShouldQuit:                  desktop.LogExternalQuit, // Dock Quit, logout: logged when the quitting flag is still down
		OnShutdown:                  deps.Shutdown,         // stop AI runs, streams, terminals, watchers; close DB
	})
	emitter.Bind(wapp)
	desktop.Install(wapp, desktop.Options{ShellLog: shellLog}) // window, close→hide, tray, menu, reopen, drop

	if err := wapp.Run(); err != nil {
		shellLog.Record("error", "[shell] run failed: "+err.Error())
		os.Exit(1)
	}
}
```

**Compiled, not just written.** Every Wails identifier used in this sketch and in §6 was compiled and
`go vet`-ed against `v3.0.0-beta.23` for darwin/arm64 and windows/amd64 in the M-1 probe
(`docs/phase0-findings.md`), and the macOS build was run. Two details it settled:
`AssetFileServerFS` serves the embedded directory's `index.html` at `/` without an `fs.Sub` (the probe
embedded `all:assets` and `/` resolved to `assets/index.html`), and the one bound method is reachable
without generated bindings as `Call.ByName("<import path>.Service.Invoke", …)` — the FQN Wails builds
is `<package path>.<type>.<method>` (`bindings.go`), e.g.
`github.com/gastonlarap-a11y/code-flow/backend/bridge.Service.Invoke`.

---

## 4. Technology stack, verified 2026-09-16

Sources: `https://proxy.golang.org/<module>/@latest`, `gh release list` / `gh api repos/<o>/<r>`,
`https://registry.npmjs.org/<pkg>`, `https://go.dev/doc/devel/release`. **Pin exact versions; re-read
them at Phase 0.** A version marked "verify" could not be confirmed that day.

### 4.1 Toolchain

| Tool | Version | Notes |
|---|---|---|
| Go | **1.27.1** (2026-09-01) | `go.mod`: `go 1.27.0` + `toolchain go1.27.1`. The operator's machine has 1.26.4 via mise → `mise use -g go@1.27.1`. |
| Wails (Go module) | **`github.com/wailsapp/wails/v3 v3.0.0-beta.23`** (2026-09-16, pre-release) | Module requires `go 1.25.0`. Nightly betas — pin exactly. |
| `wails3` CLI | **v3.0.0-beta.23** | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.23`. The operator's installed `wails3` reports `v3.0.0-alpha2.108` — outdated; reinstall. **CLI and module must match** (the operator's own `wails` skill rule). |
| Task | bundled as `wails3 task` | Standalone go-task v3.53.1 optional. |
| `@wailsio/runtime` (npm) | **3.0.0-beta.23** | Exact pin in `frontend/package.json`. |
| Node / pnpm | 24 / **11.20.0** | Unchanged from the source repo (`packageManager` field). |
| React / Vite / TypeScript / Tailwind / zustand / Vitest | ^19.2.8 / ^8.2.0 / ~6.0.3 / ^4.3.3 / ^5.0.15 / 4.1.11 | Unchanged (copied `package.json`). |
| golangci-lint | **v2.13.2** (2026-08-27) | |
| gotestsum | **v1.13.0** | Optional, nicer local output. |
| govulncheck | **v1.8.0** | `golang.org/x/vuln/cmd/govulncheck`; replaces `dotnet list package --vulnerable`. Confirmed 2026-09-17. |

### 4.2 Go libraries

| Concern | Choice | Version | Why | Rejected (and why) |
|---|---|---|---|---|
| SQLite | `modernc.org/sqlite` | **v1.59.0** | Pure Go → no CGO on Windows builds; `database/sql`; WAL, pragmas via DSN. **Measured** (M-4): opened a copy of a real 2.7.1 `codeflow.db` with the §8.3 DSN — 23 tables and 10 indexes identical to the C# schema, `journal_mode=wal`, `foreign_keys=1`, `integrity_check=ok`, stored timestamps match the Clock format | `mattn/go-sqlite3` v1.14.52 (CGO, cross toolchain on Windows); `ncruces/go-sqlite3` v0.35.5 (fine alternative, WASM-based; keep as fallback if modernc misbehaves) |
| Git | **the `git` CLI** through `backend/git` runner | system git; floor fixed in Phase 0 (step 0.10) from the oldest version shipped by Git for Windows and Xcode that runs the §8.6 commands | Only option with full parity: stash, merge with conflicts, rename-aware status, reflog, ignore rules; already required for network ops and Windows terminal. **Measured** (M-2, git 2.54): 55/55 checks of §8.6 pass, including the 9 git scenario test vectors | `go-git/go-git` v5.19.2 (no stash, partial merge — parity impossible); `libgit2/git2go` v34.0.0 (last release 2022, CGO + libgit2 per platform) |
| Keychain (macOS) | `github.com/keybase/go-keychain` | **v0.0.1** (repo active 2026-09) | SecItem API with `kSecClassGenericPassword` + service + account — the exact item shape 2.x wrote. **Measured** (M-5): an attributes-only query with that shape matches the 5 real `com.codeflow.app` items a 2.7.1 install left (`ado-pat:` ×2, `github-token:` ×1, `codeflow-test:` ×2 from its test suite); a missing account returns an empty result, not an error | `zalando/go-keyring` v0.2.8: shells out to `/usr/bin/security add-generic-password -U -s … -a … -w …` (verified in its `keyring_darwin.go`) — different ACL owner. Fallback if keybase's API does not fit: ~150 lines of own cgo against Security.framework |
| Credential Manager (Windows) | `github.com/danieljoos/wincred` | **v1.2.3** | `CRED_TYPE_GENERIC`, explicit `TargetName`, `UserName`, `Persist`. **Measured** (W-7): items written by Go were read by CodeFlow's own `WindowsCredentialManager.cs` and vice versa, value (UTF-8 with `é`), `UserName` and `Persist=2` intact | `zalando/go-keyring`: target name is `service + ":" + username` (verified in `keyring_windows.go`) — 2.x stored `{service}.{account}` → every existing credential unreadable |
| PTY | `github.com/charmbracelet/x/xpty` | **v0.1.4** | One interface over `creack/pty` v1.1.24 (Unix) and `charmbracelet/x/conpty` v0.2.0 (Windows ConPTY). **Measured** (M-6 macOS, W-4 Windows/ConPTY with Git Bash `--login -i`): 100×30, resize to 100×24, interactive output; on both OSes the reader does **not** end when the shell exits — it ends only after `pty.Close()` (§8.8) | `creack/pty` alone (no ConPTY); `aymanbagabas/go-pty` v0.2.3 (viable fallback) |
| Recursive file watching | `github.com/syncthing/notify` | pseudo-version `v0.0.0-20250528144937-c7027d4f7465` | Recursive `dir/...` on FSEvents (macOS) and ReadDirectoryChangesW (Windows); maintained by Syncthing. **Measured** on 50 000 files — macOS (M-7): a create 4 directories deep reported in 11 ms, a 5 000-write burst coalesced to 4 110 events in 1.8 s; Windows (W-5): a deep write reported in < 1 ms, the burst gave 4 115 events in 2.8 s with no error; idle heap ~2 MB on both (the 200 ms/400 ms throttle only needs a dirty flag) | `fsnotify/fsnotify` v1.10.1 (not recursive; kqueue opens a descriptor per file on macOS — exhausts on big repos); `rjeczalik/notify` v0.9.3 (upstream, last release 2023) |
| HTTP | `net/http` (stdlib) | — | Per-request transport, manual redirects and decompression (§8.13) | — |
| Brotli | `github.com/andybalholm/brotli` | **v1.2.4** | `br` in `Accept-Encoding: gzip, br, deflate` (XLANG-008) | — |
| PKCS#12 client certs | `software.sslmate.com/src/go-pkcs12` | **v0.7.3** | Maintained; `golang.org/x/crypto/pkcs12` is frozen | — |
| WebSocket | `github.com/coder/websocket` | **v1.8.15** | context-aware, maintained | `gorilla/websocket` (older API) |
| Socket.IO | **hand-ported** `SocketIoFraming` over `coder/websocket` | — | 12 vectors in `socketio.vectors.json` pin the wire bytes; the C# version is hand-written too | `zishang520/socket.io` v3.0.5 (large; parity with vectors unproven); `maldikhan/go.socket.io` v0.1.1 (tiny project) |
| MQTT 5.0 | `github.com/eclipse/paho.golang` | **v0.23.0** | Official Eclipse client for v5 | — |
| MQTT 3.1.1 | `github.com/eclipse/paho.mqtt.golang` | **v1.5.1** | Official client for 3.1.1 | No single Go client covers both (MQTTnet did); both sit behind one `mqttConn` interface |
| PostgreSQL | `github.com/jackc/pgx/v5` | **v5.11.0** | De facto standard | — |
| SQL Server | `github.com/microsoft/go-mssqldb` | **v1.11.0** | Official; Windows integrated auth via blank import `github.com/microsoft/go-mssqldb/integratedauth/winsspi` | — |
| MySQL | `github.com/go-sql-driver/mysql` | **v1.10.1** | Standard | — |
| UUID | `github.com/google/uuid` | **v1.6.0** | `uuid.NewString()` = lowercase `D` format, same as .NET `Guid.ToString()` | — |
| OS APIs | `golang.org/x/sys` | **v0.48.0** | Job Objects, `CREATE_NO_WINDOW`, process groups | — |
| errgroup | `golang.org/x/sync` | **v0.23.0** | Fan-out with cancellation | — |
| Charset decoding | `golang.org/x/text` | **v0.42.0** | `ResponseDecoding` charset handling (§8.13). Confirmed 2026-09-17 | — |
| Test assertions | `github.com/stretchr/testify` | **v1.12.1** | `require`/`assert` only — **no mocks** (hand-written fakes stay the rule) | testify v2 (unreleased) |

### 4.3 Wails v3 features the port relies on (verified in the beta.23 source)

`pkg/application`: `WebviewWindowOptions{Name, Width, Height, MinWidth, MinHeight, Frameless, Hidden,
URL, EnableFileDrop, DefaultContextMenuDisabled, Permissions, Mac MacWindow{TitleBar, InvisibleTitleBarHeight,
WebviewPreferences…}, Windows WindowsWindow{…}}`; `MacTitleBarHiddenInset` / `MacTitleBarHidden`;
`WebviewWindow.RegisterHook(events.Common.WindowClosing, fn)` + `WindowEvent.Cancel()`;
`IsFullscreen()`/`UnFullscreen()`/`Hide()`/`Show()`/`Focus()`; `app.Window.NewWithOptions`;
`app.SystemTray.New()` → `SetIcon`/`SetTemplateIcon`/`SetMenu`/`OnClick`; `app.Menu.SetApplicationMenu`,
`application.NewMenu()`, `Menu.AddRole(application.AppMenu|EditMenu|WindowMenu)`, `Menu.Add(label).SetAccelerator("CmdOrCtrl+Q").OnClick(fn)`;
`SingleInstanceOptions{UniqueID, OnSecondInstanceLaunch}`;
`app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, fn)`; `app.Clipboard.SetText`;
`app.Dialog.OpenFile()` (`CanChooseFiles`, `CanChooseDirectories`, `AddFilter`, `SetTitle`,
`PromptForSingleSelection`) and `app.Dialog.SaveFile()` (`SetFilename`, `AddFilter`,
`PromptForSingleSelection`); `app.Browser.OpenURL`; `app.Env.OpenFileManager(path, selectFile)`;
`WindowEventContext.DroppedFiles()` with `events.Common.WindowFilesDropped`; CSS
`--wails-draggable: drag` and `--wails-non-client-region: caption|minimize|maximize|close`;
WebView2 browser accelerator keys disabled by the framework (`PutAreBrowserAcceleratorKeysEnabled(false)`).
`pkg/updater` exists (GitHub provider, sha256/ed25519 verification, binary/`.app` swap) — **not used
for 3.0** (§8.15, §14).

---

## 5. The drop-in contract

Everything in this section must be **byte-identical** to 2.7.1. Breaking any row strands a user's
data, credentials or update path. Each row names the test that proves it (§9).

### 5.1 Filesystem

| Item | Value | Spec |
|---|---|---|
| Base directory, Windows | **`C:\CodeFlow`** — literal, not `%LOCALAPPDATA%` | BOOT-003, `DIVERGENCE-BOOT-a` |
| Base directory, macOS | `~/CodeFlow`; if the home directory cannot be resolved, `./CodeFlow` relative to the working directory | BOOT-003 |
| Database | `{base}/codeflow.db` (+ `-wal`, `-shm`) | BOOT-004 |
| Logs | `{base}/logs/errors.log`, `startup.log`, `shell.log`; each rolls over once to `.1` at 2 MiB | BOOT-030/031, §15.2.2 |
| Clone root | `{base}/repos` | BOOT-004 |
| Workspace skills | `{base}/workspaces/{workspace_id}/skills` (created by whoever writes into it) | BOOT-004/005 |
| Workspace MCP config | `{base}/workspaces/{workspace_id}/mcp.json` | `09-workspace-scoped.md` |
| Ticket mirror | `{base}/tickets/` unless setting `tickets_root_dir` overrides | `14-work-items.md` |
| PR-link reviews | `{base}/pr-link-reviews/<slug>/PULL_REQUEST.md`, `changes.diff` | `07-review-pipeline.md` |
| Reset marker | `{base}/.reset-pending`, empty file; wipe happens at next launch, before anything opens | BOOT-001/006/017 |
| Start-up creates | exactly `{base}`, `{base}/logs`, `{base}/repos`, in that order | BOOT-005 |
| AI scratch | temp directory; orphans older than 1 h swept at start-up | `05-ai-engines.md` (`EngineScratch`) |
| Update downloads | `~/Downloads/<assetName>` | BOOT-021 |

### 5.2 SQLite

| Item | Value | Spec |
|---|---|---|
| Connection | **one** for the process life, serialised | STORE-001 |
| Pragmas at open | `journal_mode = WAL`, `foreign_keys = ON`, `synchronous = NORMAL` | `03-storage.md` Bootstrap |
| Schema | the `CREATE TABLE IF NOT EXISTS` batch transcribed byte-for-byte in `03-storage.md` §Schema and `15-dbml.md` (`dbml_layouts`, `db_connections`); **23 tables and 10 indexes** (7 plain + 3 unique) in the C# code **and in a real 2.7.1 database** (M-4) — fix the count in `03-storage.md` in Phase 2 | `03-storage.md`, `15-dbml.md` |
| Migrations | **no version table**; each step re-derives "already ran" from the live schema; call order in `03-storage.md` §Migrations (step 1 before the batch, prompt backfill after the review-standard fold, GitHub host after GitHub columns); legacy `api_*` move inside one transaction with `legacy_alter_table` and `INSERT OR IGNORE` (`BUG-STORE-a` closed); `RealignReviewRunWorkspaces` (`BUG-STORE-b` closed) | STORE-002…008 |
| Refresh of unedited seeded prompts | a stored `review_standard` / `ticket_review_standard` whose lowercase-hex SHA-256 (UTF-8, no BOM) matches a digest in §15.2.3 is replaced by the current default | `SeededPromptHistory` |
| Timestamp format | **`yyyy-MM-ddTHH:mm:ss.fffffff+00:00`** (UTC, seven fractional digits, never `Z`); compared as strings | `Storage/Clock.cs` — Go layout `2006-01-02T15:04:05.0000000+00:00` on `t.UTC()` |
| Deletes | hard deletes only; cascades per `03-storage.md` §Cascade map (and mirrored in the renderer, `XLANG-011`) | STORE-009 |
| API client auth | plaintext JSON in SQLite (preserved; `DIVERGENCE-STORE-a`) | STORE-023, SEC-014 |

**Proof.** Already measured (M-4): the Go driver opens a real 2.7.1 database with these pragmas and
reads a schema identical to the C# one. Still to prove in Phase 2: the Go **migrations** run twice on
such a copy leave `sqlite3 codeflow.db .schema` unchanged, and the `migrations` and `queries` vectors
with their `sql/` seeds pass.

### 5.3 Credentials

| Item | Value |
|---|---|
| Service | **`com.codeflow.app`** (`SEC-001`) |
| Keys | `ado-pat:{org}` · `github-token:{host}` · `ai-api-key:{provider}` · `db-password:{connectionId}` (`SEC-002`) |
| macOS item | generic password (`kSecClassGenericPassword`), `kSecAttrService` = service, `kSecAttrAccount` = key, value = UTF-8 bytes. Set = `SecItemUpdate` first, `SecItemAdd` on `errSecItemNotFound`. Get uses `kSecReturnData` + `kSecMatchLimitOne`; `errSecItemNotFound (-25300)` → no value. |
| macOS refusal mapping | `errSecAuthFailed (-25293)`, `errSecInteractionNotAllowed (-25308)`, `errSecUserCanceled (-128)` → error starting with `CREDENTIAL_REFUSED: ` plus a sentence naming the way out (`XLANG-012`) |
| Windows item | `CRED_TYPE_GENERIC (1)`, **`TargetName = "{service}.{account}"`**, `UserName = account`, `Persist = CRED_PERSIST_LOCAL_MACHINE (2)`, blob = UTF-8; `ERROR_NOT_FOUND (1168)` → no value; delete of a missing item succeeds |
| Other OS | throw on first use: `no credential store is available on <platform>. CodeFlow targets Windows and macOS; it will not fall back to storing secrets in plaintext.` (`DIVERGENCE-SEC-c`) |
| Surface | set/has/delete only — **no command returns a secret** (`DIVERGENCE-SEC-d`) |
| Reset | `reset_app_data` never touches the keychain (`DIVERGENCE-BOOT-b`) |
| Invariant | **no credential reaches an AI agent process** — not in argv, env or stdin (`SEC-007`) |

**Item shape: verified.** An attributes-only query (class + service + account, no data, so no prompt)
matches every item a real 2.7.1 install wrote (M-5), and the same code shape covers set/update/delete
(§8.4).

**Expected one-time friction on macOS — reading the secret.** Items created through `SecItemAdd` in
the login (file-based) keychain carry an access-control partition list. For code with a Team ID the
partition is `teamid:<TEAM>`; for ad-hoc-signed code it falls back to `cdhash:<hash>`, and a cdhash
changes with every build (sources: Apple Developer Forums on SecItem ACLs; the partition-ID behaviour
documented by `claude-usage-swift` PR #31 and `CodexBar` issue #340). The 2.7.1 items therefore trust
the cdhash of `codeflow-core`, and the Go binary does not match it, so its **first data read shows
macOS' keychain prompt asking for the login keychain password** with an *Always Allow* option (or, if
the user cancels, `errSecUserCanceled`, which the existing `CREDENTIAL_REFUSED: ` path turns into
"reconnect this account"). Both outcomes are acceptable; a crash or a silent empty value is not. The
same prompt already happens today after every 2.x update (ad-hoc signature rewritten per build), and it
keeps happening per 3.x update until builds carry a stable signing identity (§14 D4). The prompt itself
was not triggered in this analysis on purpose (it needs the user's password); Phase 8's upgrade drill
records it.

### 5.4 Identity and installation

| Item | Value |
|---|---|
| App identifier / bundle id | `com.codeflow.app` |
| Product name | `CodeFlow` (window title, tray tooltip, menu `Quit CodeFlow`) |
| macOS | `CodeFlow.app`, **arm64 only** (parity), unsigned + ad-hoc, installed by dragging from the `.dmg` over the old app in `/Applications` |
| Windows | per-user NSIS install in **`%LOCALAPPDATA%\Programs\CodeFlow`** — the directory electron-builder 26.16.1 chose for 2.7.1 (`oneClick: false` → `getWindowsInstallationDirName` returns the product name) **and** the one Wails' template uses with `WAILS_INSTALL_SCOPE=user`. 2.7.1's uninstall entry is `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\e452a328-6f16-5dfd-9ae4-7f7f7761c215` (GUID = UUIDv5 of `com.codeflow.app` in electron-builder's namespace `50e065bc-3134-11e6-9bab-38c9862bdaf3`, `NsisTarget.ts`), its `InstallLocation` is under `HKCU\Software\e452a328-…`, its uninstaller is `Uninstall CodeFlow.exe`, its `DisplayName` is **`CodeFlow <version>`** (e.g. `CodeFlow 2.7.1` — never match on `DisplayName`), shortcuts in Start Menu and on the Desktop. **The Go installer must close CodeFlow and remove 2.7.1 before copying files** — done and measured on a Windows runner with 2.7.1 running (W-1…W-3, §10.3) |
| WebView2 profile | Wails' default user-data folder is `%APPDATA%\CodeFlow.exe` (`WindowsOptions.WebviewUserDataPath` empty → `%APPDATA%\[BinaryName.exe]`), distinct from Electron's `%APPDATA%\CodeFlow`; the Wails uninstaller removes it (`RMDir /r "$AppData\${PRODUCT_EXECUTABLE}"`) |
| Version | `3.0.0`, same string in `build/config.yml`, `frontend/package.json` and `-X main.version` |
| Tag | `v3.0.0` |
| Leftovers from Electron | its Chromium profile (`%APPDATA%\CodeFlow`, `~/Library/Application Support/CodeFlow`) holds **no user data** (everything lives under `{base}`); cleaning it is a decision (§14) |

### 5.5 Release artefacts and the update feed

The **2.7.x updater** decides whether users can reach 3.0.0. It reads
`https://api.github.com/repos/gastonlarap-a11y/code-flow/releases/latest` (with a token), takes the
`.dmg` on macOS or the `.exe` whose name contains `-Setup-` on Windows, downloads `<asset>.sha256`
beside it and refuses anything without a matching digest. Therefore:

| Artefact | Exact name | Digest file |
|---|---|---|
| macOS disk image | `CodeFlow-3.0.0-arm64.dmg` | `CodeFlow-3.0.0-arm64.dmg.sha256` |
| Windows installer | `CodeFlow-Setup-3.0.0-x64.exe` | `CodeFlow-Setup-3.0.0-x64.exe.sha256` |
| Windows portable | `CodeFlow-Portable-3.0.0-x64.exe` | `CodeFlow-Portable-3.0.0-x64.exe.sha256` |

- Digest file content: the output of `shasum -a 256 <name>` / `sha256sum <name>` run **from the
  artefact's directory** (`<64 hex>  <name>`), one entry per file. **No spaces in artefact names**
  (GitHub rewrites them to dots — the v1.7.5 incident, BOOT-021).
- **The 3.0.0 release must be published on `gastonlarap-a11y/code-flow`**, or 2.7.x users never see
  it. §14 D2 records how (recommended: cut over the Go code into that repository).
- On Windows 2.7.x starts the installer automatically **while it keeps running** (it only quits when
  the user clicks *Restart*); on macOS it opens the `.dmg` (`open <path>` mounts it) and the user drags
  the app to `/Applications`, where Finder asks to replace the running copy. §2.9, §10.3.

### 5.6 The wire contract

| Item | Value |
|---|---|
| Command names | the 235 registered names, byte-identical; the renderer's 246 (§2.4) are the proof set |
| Unknown command | error message `unknown command '<name>'` (measured against the 2.7.1 core, M-3) |
| Missing parameter | error message `missing required parameter '<name>'` (measured, M-3: `missing required parameter 'repoPath'`) |
| Params | camelCase top-level keys; nested objects snake_case; missing ≡ `null` |
| Results / events | snake_case, except the camelCase contexts in §3.4; void → `null`; empty lists `[]` |
| Events | the 10 names and payloads in §2.5 (`update:progress` now actually delivered) |
| Sentinels | the 12 in §2.7, trailing spaces included |

### 5.7 Other `VERBATIM` content the port must carry

| Content | Where it is now |
|---|---|
| 17 prompts | `docs/verbatim/prompts/*.txt` → `backend/ai/prompts/` (`//go:embed`); resource names today `CodeFlow.Ai.Prompts.<NAME>.txt` |
| Review finding format and its five regexes; severity table | `XLANG-001` |
| Ticket verdict block | `XLANG-016` |
| AI task keys `chat commit analyze review pr_description fix conflict inline ticket_review dbml`; settings keys `ai_provider_{task}`, `ai_provider`, `{provider}_{task}_model`, `{provider}_model`, `{provider}_binary_path`, `{provider}_allowed_tools` | `XLANG-004/005`, `05-ai-engines.md` |
| Default binaries (`claude`, `agy` for provider `gemini`, `codex`, `opencode`, `http://localhost:11434`, `https://api.openai.com/v1`) and the `claude` commit model `claude-haiku-4-5-20251001` | `XLANG-005/006` |
| `Accept-Encoding: gzip, br, deflate`; extension → MIME table | `XLANG-008/009` |
| 15 secret-scan rules, placeholder needles, mask algorithm | `10-security.md` |
| Checkpoint refs `refs/codeflow/checkpoints/…` (max 20) | `04-git.md` |
| PR fetch refspec `+refs/pull/{id}/head:refs/remotes/origin/codeflow-pr-{id}` | `07-review-pipeline.md` |
| GitHub self-approval sentence `Can not approve your own pull request` (case-insensitive) | `XLANG-013` |
| Azure api-versions `7.1`, `7.1-preview`, comments `7.1-preview.4`, iterations `7.1-preview.1`; GitHub `X-GitHub-Api-Version: 2022-11-28` | `06-providers.md`, `14-work-items.md` |

---

## 6. Electron shell → Wails v3 mapping

All of this lives in `backend/desktop` (Go) and `frontend/src/lib/bridge` (TypeScript). API names were
checked against `github.com/wailsapp/wails/v3@v3.0.0-beta.23`; re-check on every Wails upgrade.

### 6.1 Summary table

| Electron today (§2.6) | Wails v3 | Notes |
|---|---|---|
| `BrowserWindow` 1440×900, min 1024×640, `show:false` + `ready-to-show` | `app.Window.NewWithOptions(WebviewWindowOptions{Name:"main", Width:1440, Height:900, MinWidth:1024, MinHeight:640, Hidden:true})`; show on `events.Common.WindowRuntimeReady` | §6.2 |
| macOS `titleBarStyle:"hidden"`, traffic lights at 20/22 | `Mac: MacWindow{TitleBar: MacTitleBarHiddenInset}` | No traffic-light position option: compare screenshots in Phase 0 and adjust `MacControlsSpacer` (84 px) |
| Windows `frame:false` + React caption buttons | `Frameless: true` on Windows; buttons call `@wailsio/runtime` `Window.Minimise()/ToggleMaximise()/Close()` | Optional: `--wails-non-client-region: minimize/maximize/close` for Snap Layouts |
| `-webkit-app-region: drag` on `[data-drag-region]` | CSS `--wails-draggable: drag` / `no-drag` | `frontend/src/index.css`. WKWebView does not support `-webkit-app-region`/`app-region` at all (measured, M-1); the Wails runtime reads the custom property |
| `close` → `preventDefault` + `hideToBackground` | `win.RegisterHook(events.Common.WindowClosing, fn)` + `e.Cancel()` | §6.3 |
| macOS fullscreen: wait for `leave-full-screen` then hide | `UnFullscreen()`, poll `IsFullscreen()` every 50 ms up to 40 times, then `application.InvokeSync(win.Hide)` | Same as BOOT-009 |
| `requestQuit(reason)` + `quitting` flag | `desktop.RequestQuit(reason)` → `shell.log` + `atomic.Bool` + `app.Quit()` | §6.4 |
| SIGTERM/SIGINT/SIGHUP → `requestQuit` | `Options.DisableDefaultSignalHandler: true` + own `signal.Notify` → `RequestQuit("a <sig> from outside the app")` | Wails' default handler would quit without naming a reason |
| Tray `Show CodeFlow` / `Quit CodeFlow`, click shows | `app.SystemTray.New()` → `SetIcon`, `SetTooltip("CodeFlow")`, `SetMenu`, `OnClick` | §6.5 |
| macOS menu (App with custom Quit ⌘Q, Edit, Window); none elsewhere | `app.Menu.SetApplicationMenu(menu)` on darwin only; roles `About, Hide, HideOthers, UnHide, Undo, Redo, Cut, Copy, Paste, SelectAll, Minimise, Zoom, CloseWindow`; custom `Quit CodeFlow` item | §6.6 — never use the `Quit` role |
| `requestSingleInstanceLock` + `second-instance` → show | `Options.SingleInstance{UniqueID:"com.codeflow.app", OnSecondInstanceLaunch}` | |
| `activate` (Dock click) → show | `app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, …)` | BOOT-011 |
| `app://codeflow` protocol + `isWithinRoot` | `//go:embed all:frontend/dist` + `application.AssetFileServerFS(assets)` | Traversal is the asset server's job; add a test requesting `/../go.mod` |
| CSP header on HTML | `<meta http-equiv="Content-Security-Policy">` injected at build time | §6.7 — `connect-src` must become `'self'` |
| `will-navigate` block + `setWindowOpenHandler` → `openExternal` | Renderer-side click capture for external `a[href]` + `window.open` shim → `host.openExternal` | §7.4 |
| `installContextMenu` (cut/copy/paste/selectAll, only when applicable) | Keep the webview's default menu (`DefaultContextMenuDisabled` left `false`): the Wails runtime's CSS rule `--default-contextmenu: auto` (the default, `contextmenu.ts`) shows the native menu only on editable elements or selected text — the same "only when applicable" behaviour | Check in Phase 1 that production builds show no *Reload*/*Inspect* entries; if they do, add `--default-contextmenu: hide` outside inputs |
| Permission handlers (only clipboard write) | `WebviewWindowOptions.Permissions` (deny all) | Clipboard write goes through Go |
| `codeflow:clipboardWrite` | `HostService.ClipboardWrite(text)` → `app.Clipboard.SetText` | returns `bool`; `false` → error |
| `codeflow:openExternal` (http/https only) | `HostService.OpenExternal(url)` → validate scheme → `app.Browser.OpenURL` | gate stays in Go |
| `codeflow:openLogs` | `HostService.OpenLogs()` → `MkdirAll` → `app.Env.OpenFileManager(logsDir, false)` | |
| `codeflow:dialog` open file / directory / save | `HostService.OpenFile/OpenDirectory/SaveFile(opts)` → `app.Dialog.OpenFile()` (`CanChooseFiles`, `CanChooseDirectories`, `AddFilter`, `SetTitle`, `SetDirectory`, `PromptForSingleSelection`) / `app.Dialog.SaveFile()` (`SetFilename`, `AddFilter`) | Return `*string`; **cancel → `nil` → JS `null`**; filter extensions `["png"]` → pattern `"*.png"` |
| `codeflow:quit(reason)` (preload caps 120 chars) | `HostService.Quit(reason string)` → cap at 120 runes → `RequestQuit` | |
| `codeflow:sidecarStatus` + `codeflow:sidecar-status` event | `HostService.SidecarStatus()` → `{status, detail?, logsDirectory}` from `app.StartupState` | BOOT-032 reinterpreted (§3.8); the event is never emitted |
| `webUtils.getPathForFile` for dropped files | `EnableFileDrop: true` + `data-file-drop-target` + `events.Common.WindowFilesDropped` → `e.Context().DroppedFiles()` → emit `codeflow:files-dropped {paths}` | §6.8 |
| `nativeTheme.themeSource` (`setTheme`) | not ported (never called) | |
| `login-path.ts` | `platform.ApplyLoginShellPath` at the top of `main` (macOS only) | §15.2.4 |
| sidecar `spawn(detached)` + SIGKILL after 3 s | gone; children of the app are grouped and tree-killed (§8.0) | |
| `CODEFLOW_DEV_SERVER=http://localhost:1420` | `wails3 dev -config ./build/config.yml`, Vite on **1420** (`VITE_PORT`), `strictPort` | Keep 1420 so the copied `vite.config.ts` needs no change |

### 6.2 Window

```go
func newMainWindow(app *application.App) *application.WebviewWindow {
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:                       "main",
		Title:                      "CodeFlow",
		Width:                      1440,
		Height:                     900,
		MinWidth:                   1024,
		MinHeight:                  640,
		Hidden:                     true,                      // shown once the runtime is ready (ready-to-show)
		Frameless:                  runtime.GOOS != "darwin",  // Windows draws its own caption buttons
		EnableFileDrop:             true,                      // DefaultContextMenuDisabled stays false (§6.1)
		URL:                        "/",
		Permissions: map[application.PermissionType]application.Permission{ // BOOT-029
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,      // native traffic lights over the webview
		},
	})
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { win.Show() })
	return win
}
```

### 6.3 Close hides; only a named quit exits

```go
var quitting atomic.Bool

func installCloseToBackground(win *application.WebviewWindow) {
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return // a RequestQuit is in progress: let the window close
		}
		e.Cancel()
		hideToBackground(win)
	})
}

// BOOT-009/010. Polling IsFullscreen is the exact "transition finished" signal; 40 × 50 ms caps it.
func hideToBackground(win *application.WebviewWindow) {
	if runtime.GOOS != "darwin" || !win.IsFullscreen() {
		win.Hide()
		return
	}
	win.UnFullscreen()
	safego.Go("hide-after-fullscreen", func() {
		for i := 0; i < 40 && win.IsFullscreen(); i++ {
			time.Sleep(50 * time.Millisecond)
		}
		application.InvokeSync(func() { win.Hide() })
	})
}

func showMain(win *application.WebviewWindow) {
	win.Show()
	win.UnMinimise()
	win.Focus()
}
```

### 6.4 Quitting with a reason (BOOT-008, BOOT-036)

```go
func RequestQuit(app *application.App, log *diagnostics.ShellLog, reason string) {
	log.Record("info", "[shell] quitting: "+reason)
	quitting.Store(true)
	app.Quit()
}
```

Callers, each with its own reason: tray *Quit CodeFlow*, macOS menu *Quit CodeFlow* (⌘Q), the renderer
(`host.quit(reason)` from Settings, `reset_app_data`, the updater), and the signal handler.
`Options.ShouldQuit` logs a warning when `quitting` is still `false` — on macOS a Dock *Quit* or a
logout reaches `applicationShouldTerminate`, which calls `ShouldQuit` before cleanup
(`application_darwin_delegate.m`) — and returns `true`.

### 6.5 Tray

```go
func installTray(app *application.App, win *application.WebviewWindow, quit func(string)) {
	menu := application.NewMenu()
	menu.Add("Show CodeFlow").OnClick(func(*application.Context) { showMain(win) })
	menu.AddSeparator()
	menu.Add("Quit CodeFlow").OnClick(func(*application.Context) { quit("the tray's Quit item") })

	tray := app.SystemTray.New()
	tray.SetIcon(trayPNG) // build/tray.png, embedded; 32×32, not a template image today
	tray.SetTooltip("CodeFlow")
	tray.SetMenu(menu)
	tray.OnClick(func() { showMain(win) })
}
```

Click semantics, read from `systemtray.go` / `systemtray_darwin.go`: with `OnClick` set and no
`OnRightClick`, a left click runs our handler and a right click opens the menu (Wails' "smart
defaults" only fill the handlers that are missing). That matches BOOT-012 (left click shows the window,
the menu is not opened by a left click). Do **not** use `AttachWindow`: it toggles and repositions the
window next to the tray icon.

### 6.6 macOS menu (darwin only; Windows gets none)

```go
func installMacMenu(app *application.App, quit func(string)) {
	menu := application.NewMenu()

	appMenu := menu.AddSubmenu("CodeFlow")
	appMenu.AddRole(application.About)
	appMenu.AddSeparator()
	appMenu.AddRole(application.Hide)
	appMenu.AddRole(application.HideOthers)
	appMenu.AddRole(application.UnHide)
	appMenu.AddSeparator()
	// Custom item, never application.Quit: the role would go through the close interception and
	// ⌘Q would only hide the window (BOOT-014).
	appMenu.Add("Quit CodeFlow").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) {
		quit("the macOS menu's Quit item")
	})

	edit := menu.AddSubmenu("Edit") // load-bearing: without it ⌘C/⌘V/⌘X/⌘A never reach WKWebView (BOOT-013)
	edit.AddRole(application.Undo)
	edit.AddRole(application.Redo)
	edit.AddSeparator()
	edit.AddRole(application.Cut)
	edit.AddRole(application.Copy)
	edit.AddRole(application.Paste)
	edit.AddRole(application.SelectAll)

	window := menu.AddSubmenu("Window")
	window.AddRole(application.Minimise)
	window.AddRole(application.Zoom)
	window.AddRole(application.CloseWindow)

	app.Menu.SetApplicationMenu(menu)
}
```

### 6.7 Content Security Policy

Keep today's policy with **one required change** — `connect-src 'self'` — because the Wails runtime
talks to Go through requests to its own origin (`http://wails.localhost` on Windows, the `wails://`
scheme on macOS). Inject it only into production builds (a small Vite `transformIndexHtml` plugin),
because Vite's dev server needs its HMR websocket:

```
default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; worker-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'
```

`'unsafe-eval'` stays for the API client's Postman-style scripts (`lib/api/sandbox.ts` uses
`new Function`). **Measured on macOS** (M-1): with exactly this policy in a `<meta>` tag on
`wails://localhost/`, every runtime call succeeded, a module worker loaded from `wails://localhost/`
under `worker-src 'self'`, and the page recorded **zero** `securitypolicyviolation` events. **Same on
Windows** (W-6): origin `http://wails.localhost`, all calls through, worker loaded, zero violations.

### 6.8 Dropped files (BOOT-022)

```go
win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
	emitter.Emit("codeflow:files-dropped", map[string]any{"paths": e.Context().DroppedFiles()})
})
```

Wails only reports drops onto an element carrying `data-file-drop-target` (it adds the class
`file-drop-target-active` while hovering). `ImportModal.tsx` accepts a drop **anywhere while it is
open**, so put the attribute on the modal's full-screen backdrop, not on an inner zone.

---

## 7. Renderer adaptation

Copy once, then change **only** what the host swap forces. Every other file stays byte-identical so
the UX contract (`docs/UX-REDESIGN.md`) and the 66 renderer tests keep meaning what they meant.

### 7.1 Copy

§11 Phase 0, step 0.3 (the only time the original repository is read):

```sh
rsync -a --exclude node_modules --exclude dist \
  ~/Documents/Git/code-flow/renderer/ ~/Documents/Git/code-flow-go/frontend/
pnpm -C frontend install --frozen-lockfile
pnpm -C frontend add -E @wailsio/runtime@3.0.0-beta.23
```

### 7.2 Files that change

| File | Change |
|---|---|
| `src/lib/bridge/host.ts` | Rebuilt on Wails (§7.3), calling the bound methods with `Call.ByName` — **no generated bindings are needed** for the bridge, which also avoids the TypeScript type the generator would give `json.RawMessage`. Same exported names and types, so no importer changes. Replace the Electron wording of `bridge()`'s error (`"…is this running outside Electron?"`). |
| `src/lib/bridge/host.test.ts` | Replace the Electron-prefix cases with: a Wails `RuntimeError` message passes through unchanged (sentinels at position 0, trailing spaces kept); a non-`Error` rejection is stringified. |
| `src/lib/bridge/webview.ts` | Drop `pathForFile`; enter/over/leave stay DOM-driven; `drop` comes from `codeflow:files-dropped`. |
| `src/lib/bridge/shell.ts` | `getCurrentWindow()` returns the runtime `Window` wrapper; `platform()` from §7.3. |
| `src/lib/bridge/dialog.ts` | Calls `HostService` bindings; keeps `null` on cancel. |
| `src/lib/bridge/updater.ts` | No change (progress events now arrive). |
| `src/components/api/ImportModal.tsx` | `data-file-drop-target` on the backdrop. |
| `src/index.css` | `-webkit-app-region` → `--wails-draggable`. |
| `index.html` / `vite.config.ts` | Production-only CSP meta plugin (§6.7). |
| Comments that describe Electron/Chromium (no behaviour) | `index.css` (lines ~38, ~219, ~491), `state/sidecarStore.ts`, `components/common/RowActions.tsx`, `components/common/Tooltip.tsx`, `components/common/Toast.tsx`, `components/layout/WindowControls.tsx` (still says `titleBarStyle: Overlay`), `lib/platform.ts`, `lib/lazyRetry.ts` (mentions `app://codeflow/`), `lib/bridge/dialog.ts`, `lib/bridge/webview.ts`, `lib/bridge/shell.ts` — reword so nobody debugs the wrong host. The user-visible `sidecar.downTitle` ("CodeFlow's core is not running") still fits BOOT-032 as reinterpreted. |
| WebKit fixes | §7.4 |

`window.codeflow` disappears; nothing outside `host.ts`/`webview.ts` referenced it (§2.7).

### 7.3 `host.ts` on Wails (sketch)

```ts
import { Call, Events, Window as CurrentWindow } from "@wailsio/runtime";

/** Wails names a bound method `<Go package path>.<type>.<method>` (bindings.go); one constant per service. */
const BRIDGE = "github.com/gastonlarap-a11y/code-flow/backend/bridge.Service";
const HOST = "github.com/gastonlarap-a11y/code-flow/backend/desktop.HostService";

export type Platform = "macos" | "windows" | "linux" | "unknown";

/** Synchronous and deterministic per webview: WKWebView says "Macintosh", WebView2 "Windows NT". */
function detectPlatform(): Platform {
  const ua = navigator.userAgent;
  if (/Macintosh|Mac OS X/.test(ua)) return "macos";
  if (/Windows NT/.test(ua)) return "windows";
  if (/Linux/.test(ua)) return "linux";
  return "unknown";
}

/** A Wails RuntimeError's message is the Go error's text, verbatim — sentinels included. */
export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export async function invoke<T>(method: string, params?: Record<string, unknown>): Promise<T> {
  try {
    // Cast: the bridge is untyped by design; each command wrapper in lib/ipc owns its result type.
    return (await Call.ByName(`${BRIDGE}.Invoke`, method, params ?? {})) as T;
  } catch (error) {
    throw new Error(errorMessage(error), { cause: error });
  }
}

export function listen<T>(event: string, handler: (e: { payload: T }) => void): Promise<() => void> {
  const off = Events.On(event, (ev) => handler({ payload: ev.data as T }));
  return Promise.resolve(off);
}
```

`available()` becomes "the Wails runtime is present" (false under plain `vite dev` in a browser, which
keeps the existing silent fallbacks working). The two service constants depend on the module path
chosen in §14 D2; a Go test registers the services and asserts that both FQNs resolve, and `host.ts`
remains the only file that knows them (the operator's `wails` skill rule: wrap the backend in one
module). All of this was exercised in M-1: `Call.ByName("main.Bridge.Invoke", …)` round-tripped
params, `null`, events and errors exactly as described.

### 7.4 WebKit / WebView2 compatibility work

| # | Issue | Where | Fix | Verify |
|---|---|---|---|---|
| W1 | **CSS anchor positioning** shipped in Safari 26.0; `position-try-fallbacks` flip options were extended in 26.2 (WebKit release notes for Safari 26/26.1/26.2). Older WebKit draws every tooltip and row menu at the viewport's top-left | `components/common/Tooltip.tsx`, `components/common/RowActions.tsx`, `lib/ui/anchorName.ts` | **Measured on Safari 27** (M-1): `anchor-name`, `position-area`, `position-try-fallbacks` supported and a `popover` anchored `position-area: bottom` was placed right under its anchor. So no fix is needed on current macOS; for older WebKit either set the minimum to Safari ≥ 26.2 (§14 D3) or feature-detect `CSS.supports("anchor-name: --a")` and position with `@floating-ui/dom` (verify version when adopting) | screenshot on the oldest supported macOS |
| W2 | **Popover API** needs Safari 17+; `light-dark()` 17.5+; `color-mix` 16.2+; `@container` 16+ | same files; `index.css` (28 × `light-dark()`) | All supported on Safari 27 (measured, M-1); covered by the minimum of §14 D3 (Safari ≥ 26.2) | |
| W3 | `-webkit-app-region` is Chromium-only | `index.css:214-232`, `CommandHeader.tsx` | `--wails-draggable: drag` / `no-drag` | drag the header on both OSes |
| W4 | `user-select: none` unprefixed | `index.css:168` | **Measured** (M-1): WKWebView reports `CSS.supports("user-select: none") === false` — only the prefixed property works. The built CSS must carry `-webkit-user-select` (Tailwind 4/Lightning CSS adds it for Safari targets; confirm in `dist`, otherwise write both) | inspect `dist`, try to select UI chrome text |
| W5 | Direct `navigator.clipboard.writeText` | `settings/ProvidersSection.tsx:191`, `settings/ProjectsSettings.tsx:88`, `api/EnvironmentModal.tsx:227`, `api/ResponsePanel.tsx:115,512`, `api/CodeSnippetPanel.tsx:172`, `api/stream/shared.tsx:276` | **Measured** (M-1): without a user gesture WKWebView rejects it with `NotAllowedError`, while `Clipboard.SetText` from Go succeeded and round-tripped. Route all of them through `useCopy` (→ `HostService.ClipboardWrite`) | click each copy button |
| W6 | Image copy after `await canvasToPngBlob()` loses user activation in Safari | `editor/CodeSnapModal.tsx:115` | `new ClipboardItem({"image/png": canvasToPngBlobPromise})` — pass the `Promise<Blob>`, do not await first | CodeSnap copy on macOS |
| W7 | `navigator.clipboard.readText()` on mount shows WebKit's paste callout | `layout/OpenPrLinkModal.tsx:109` | Remove the automatic read (it was already a no-op under Electron's denied permission) — record in §13 | open the modal |
| W8 | Trackpad pinch may arrive as `gesture*` events in WebKit instead of ctrl+wheel | `dbml/DbmlCanvas.tsx:183-195` | Not measurable by script (a pinch needs a real trackpad; M-1 only saw that `ongesturestart` is not a `window` property). Add `gesturestart/gesturechange/gestureend` listeners on the canvas element mapping `scale` to the same zoom, keep the wheel handler; both are harmless where unused | pinch on the canvas by hand |
| W9 | `target="_blank"` links do nothing in WKWebView | `settings/SkillsSettings.tsx:125`, `layout/OpenPrLinkModal.tsx:48`, `ai/PrReviewPanel.tsx:208`, `lib/markdown.ts` (DOMPurify keeps `target`) | One capture-phase click listener installed in `main.tsx`: external `http(s)` anchors → `host.openExternal`, `preventDefault`; shim `window.open` likewise | click each link |
| W10 | Secure-context APIs: `crypto.randomUUID()` (13 call sites in 6 files: `state/aiRunStore.ts`, `state/chatStore.ts`, `lib/api/exporters.ts` ×3, `lib/api/importers.ts` ×4, `lib/api/variables.ts` (already guarded), `components/api/GrpcPanel.tsx` ×3), `crypto.subtle` (`lib/api/auth.ts` signing, `lib/api/sandbox.ts` digest/HMAC), `navigator.clipboard` | — | **Measured on macOS 27** (M-1): `wails://localhost` **is** a secure context — `isSecureContext` true, `randomUUID` and `subtle.digest` worked, no private API needed. This is WebKit's intended behaviour: its own API test `URLSchemeHandler.isSecureContext` (`Tools/TestWebKitAPI/Tests/WebKit/WKWebView/WKURLSchemeHandler-1.mm`) asserts that pages served by a `WKURLSchemeHandler` are secure. **Windows** (W-6): `http://wails.localhost` is a secure context too and the same APIs worked. Keep a one-line `crypto.randomUUID` guard in `lib/uuid.ts` only if the minimum macOS chosen in §14 D3 predates that behaviour | M-1 / W-6 |
| W11 | Monaco module workers from the custom scheme | `lib/monacoSetup.ts` | **Measured** (M-1): a `type: "module"` worker created with `new URL("./worker.js", import.meta.url)` loaded from `wails://localhost/` under `worker-src 'self'` — the mechanism Vite's `?worker` imports use. Confirm Monaco itself in Phase 1 | open the editor, check TS diagnostics |
| W12 | Shortcut clashes: WebView2 Ctrl+P/F/0 (Wails disables browser accelerator keys), macOS ⌘W bound by the Window menu's `CloseWindow` role | `lib/keys.ts`, `lib/useGlobalShortcuts.ts` | ⌘W parity: Electron's `close` role bound it too — keep; confirm Ctrl+P/F/0 reach the app on Windows | shortcuts list |
| W13 | Browser-default context menu | — | Keep the default with the runtime's `--default-contextmenu: auto` (native menu only on editable elements or selected text, §6.1); hide it elsewhere only if a production build shows *Reload*/*Inspect* | right-click an input and empty chrome |
| W14 | The `verify` skill drives Chromium over CDP (`--remote-debugging-port=9222`) | `.claude/skills/verify` (source repo) | Windows: `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222`; macOS: Safari Web Inspector, or build with `-tags mcp` to use Wails' built-in MCP automation server (port 9099) | Phase 1 |

---

## 8. Backend porting guide, feature by feature

How to read each subsection: **Spec** is what to satisfy; **Source size** tells you the effort; **Go**
is where it goes; **How** lists the decisions and traps; **Tests** names the C# classes to port (full
method lists in `docs/verbatim/test-inventory.md`) and the vectors that feed them. C# → Go idioms are
in §15.3.

### 8.0 Cross-cutting building blocks (write these first, in Phase 1)

**`backend/shared/proc` — every child process goes through it.**

- **Process group** (BOOT-037, inverted): Unix `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`;
  Windows `CreationFlags: CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW` and `HideWindow: true`.
  **Without `CREATE_NO_WINDOW` every `git`/`claude` call flashes a console window** in a GUI app.
- **Kill as a tree** (the C# `Kill(entireProcessTree: true)`): Unix `syscall.Kill(-pgid, SIGKILL)`;
  Windows: assign the process to a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` right after
  `Start()` (`golang.org/x/sys/windows`), close the job to kill; fall back to `taskkill /T /F /PID <pid>`.
- **Environment**: inherit, replace `PATH` with the augmented list (§8.9, `BinaryDiscovery`), add nothing
  else — **never a credential** (SEC-007). The C# code sets **no** other variable on any child, including
  the `git` network commands (verified), so keep network commands (`clone`/`fetch`/`pull`/`push`) on the
  inherited environment: their stderr is shown to the user in the user's language and a credential
  helper may legitimately prompt. Only the **local, parsed** git commands that replace libgit2 calls get
  `LC_ALL=C` (their messages were libgit2's English before, and the parser matches English text, e.g.
  `would be overwritten`, `CONFLICT`) and `GIT_OPTIONAL_LOCKS=0` for read-only ones.
- **Windows shims**: `.cmd`/`.bat` targets (npm-installed CLIs, `npx`, `code`) run through
  `cmd /C` exactly where C# did (`npx`, `code`); be aware that argv text reaching `cmd.exe` is re-parsed
  (§12, R9).
- **Output**: read stdout/stderr concurrently (never `CombinedOutput` for streaming commands); lossy
  UTF-8 decoding per invalid byte (see `terminal` below).

**`backend/shared/safego`** — `Go(name string, fn func())` with `recover`, logging, no re-panic. The
only way the codebase starts a goroutine (§3.8, BOOT-035).

**HTTP client** (`backend/platform/httpclient.go`) — one shared `*http.Client` for providers, tickets
and updates, like the C# singleton: `Timeout: 5 * time.Minute`; transport with
`IdleConnTimeout`/connection reuse (the 15-minute pooled lifetime existed to pick up DNS changes — use
`Transport.IdleConnTimeout = 90s` and `MaxConnsPerHost` defaults); wrapped by `TransientRetryTransport`:
**retry once, immediately, only for requests with no body, only when the error matches
`TransientNetwork`** (§15.2.1). The API client does **not** use it (API-001: one client per send).

**Regular expressions: .NET → Go RE2.** The source has 36 regex declarations. No lookaround or
backreference appears in them; named groups `(?<val>…)` are accepted by Go 1.22+. Watch for:
`\s`, `\w`, `\d`, `\b` are **ASCII-only** in RE2 but Unicode in .NET — where a spec rule relies on
Unicode classes (review findings with accented words), write explicit classes (`[\p{L}\p{N}_]`,
`[\s\x{00A0}]`). `RegexOptions.IgnoreCase` → `(?i)`; `CultureInvariant`/`NonBacktracking` → nothing
(RE2 is linear). `matchTimeoutMilliseconds` → nothing (linear time). **User-supplied search patterns**
(`search_repo`, `replace_in_repo`) that use lookaround now fail to compile → return the compile error;
record the divergence (§13).

**Strings and bytes.** C# strings are UTF-16; Go strings are bytes. Every "cap at N chars" rule
(2 000-char AI lines, 400-char search lines, 120-char quit reasons) counts **Unicode code points or
UTF-16 units as the spec says** — use `[]rune` / `utf8.RuneCountInString`, never `len()`.
`string.Contains(x, StringComparison.OrdinalIgnoreCase)` → `strings.Contains(strings.ToLower(a),
strings.ToLower(b))`. Lossy UTF-8 (`Encoding.UTF8.GetString`) → decode with `utf8.DecodeRune` and emit
U+FFFD per invalid byte.

**Paths.** `filepath` everywhere; comparisons for containment via `filepath.Rel` after `filepath.Clean`
(lexical, BUG-FILE-a's fix) and `filepath.EvalSymlinks` only for paths that exist (FILE-001). On Windows
compare case-insensitively.

### 8.1 Bridge, application start-up and smoke test

- **Spec**: `01-ipc-surface.md`, `02-bootstrap-platform.md` (BOOT-001…006, 016, 017, 030, 032).
- **Source size**: `Ipc/` 855 LOC (mostly deleted), `Program.cs` 266.
- **Go**: `backend/bridge` (§3.3–3.6), `backend/app` (`RunStages`, `StartupState`, `RunSmokeTest`).
- **How**:
  - `RunStages` runs `reset-marker` → `directories` → `scratch-sweep` → `storage` through
    `stage(name, fn)`, which records failures to `startup.log` (with the full error chain) and stops;
    unlike C# the process keeps running so the window can show the failure (§3.8).
  - `reset_app_data` writes `.reset-pending` (empty) and returns; the renderer then calls
    `host.quit("…")` (today's split, BOOT-017). The keychain is never touched.
  - `--smoke-test`: SQLite round trip in a temp DB; `git --version` + `git rev-parse` in a temp repo
    (replaces the LibGit2Sharp probe); PTY probe (§8.8 parameters); exit `0`/`1`. CI runs it on both OSes.
- **Tests**: `Ipc/` classes are replaced by `bridge` tests — registry duplicate/seal panics, unknown
  command text, missing parameter text, `null` for void, `[]` for empty lists, panic recovery, every
  sentinel preserved at position 0; `TestVectors/FixtureCatalogTests` (5) → `shared/testvectors`.

### 8.2 Platform and diagnostics

- **Spec**: `02-bootstrap-platform.md` (Paths, BOOT-003/004/005, 030, 031, 033).
- **Source size**: `Platform/` 299, `Diagnostics/` 607.
- **Go**: `backend/platform` (`AppPaths`, `ApplyLoginShellPath`, `TransientNetwork`,
  `TransientRetryTransport`), `backend/diagnostics` (`ErrorLog`, `StartupLog`, `ShellLog`, `Redact`).
- **How**:
  - `AppPaths.Base()`: `runtime.GOOS == "windows"` → `C:\CodeFlow`; else `os.UserHomeDir()` + `CodeFlow`,
    falling back to `./CodeFlow`.
  - The three logs share one writer: lock, `MkdirAll`, roll to `.1` when size > 2 MiB (overwrite),
    append line + newline, swallow I/O errors. Directory is a constructor parameter so tests never write
    into the user's real `~/CodeFlow/logs` (the 45-line `contoso` incident).
  - `Redact`: four regexes transcribed in §15.2.2 — RE2-compatible as written.
  - `ApplyLoginShellPath`: §15.2.4, 2 s timeout, never fatal.
- **Tests**: `TransientNetworkTests` (5), `TransientRetryHandlerTests` (5), `ErrorLogTests` (4),
  `StartupLogTests` (3); port the 10 `shell-log.test.ts` and 11 `login-path.test.ts` node cases as Go
  table tests (marker parsing, noisy profiles, `=` inside PATH, merge order, duplicates, empty entries).

### 8.3 Storage

- **Spec**: `03-storage.md` (entire), `15-dbml.md` (its two tables), `09-workspace-scoped.md` (prompt
  defaults), `test-vectors/migrations.vectors.json`, `queries.vectors.json`, `sql/*.sql`.
- **Source size**: 1 262 LOC.
- **Go**: `backend/storage` — `Open(path)`, `Schema` (the DDL as one Go raw string copied from the spec),
  `Migrations.Run(ctx, db)`, `Clock`, `SeededPromptHistory`; stores live with their feature packages.
- **How**:
  - Driver `modernc.org/sqlite`, DSN
    `file:<path>?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)`;
    `db.SetMaxOpenConns(1)`, `SetMaxIdleConns(1)`, `SetConnMaxLifetime(0)` — one physical connection, so
    per-connection pragmas stay in force (`foreign_keys` is per connection; `AMBIGUOUS-WS-a` depends on it).
  - `storage.DB{ mu sync.Mutex; db *sql.DB }` with `Read`/`Write(ctx, func(*sql.Tx or *sql.Conn) error)`.
  - Migrations are code, not files: a slice of named steps in the spec's call order, each re-deriving
    "already ran" with `table_exists` (`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`) and
    `has_column` (`PRAGMA table_info(<t>)`). The legacy `api_*` move keeps `PRAGMA foreign_keys = OFF` +
    `legacy_alter_table = ON` and runs in **one transaction** with `INSERT OR IGNORE`
    (`BUG-STORE-a` closed). Note: `PRAGMA foreign_keys` cannot change inside a transaction — issue it
    before `BEGIN` and after `COMMIT`, on the same single connection.
  - `Clock.Now()` → `time.Now().UTC().Format("2006-01-02T15:04:05.0000000+00:00")`.
  - `NULL` binding: pass `nil` for `*T == nil` (the C# `DBNull` helper).
  - Booleans are stored as the spec's DDL declares (INTEGER 0/1); scan into `bool` explicitly.
  - Reconcile the table count: spec says 22 (`03-storage.md`), code has 23 (`dbml_layouts`,
    `db_connections` live in `15-dbml.md`). Fix the document, not the schema.
- **Tests**: `MigrationTests` (12) against the vectors and seeds; a **real 2.7.1 database copy**
  (§5.2 proof); `-race` on concurrent `Read`/`Write`.

### 8.4 Security: credential store and secret scanner

- **Spec**: `10-security.md` (entire), `XLANG-012`, `test-vectors/secret_scan.vectors.json`.
- **Source size**: 1 010 LOC (`CredentialStore` 157, `MacKeychain` 366, `WindowsCredentialManager` 149,
  `SecretScan` 247, `SecretCommands` 91).
- **Commands**: `set_/has_/delete_ado_pat`, `set_/has_/delete_github_token`, `set_/has_/delete_ai_api_key`,
  `scan_staged_secrets`.
- **Go**: `backend/security` — `CredentialStore{backend}` with `keychain_darwin.go`,
  `credman_windows.go`, `unsupported_other.go` (build tags by filename); `secretscan.go`.
- **How**:
  - Item shapes and error mapping are fixed by §5.3. With `keybase/go-keychain` (names checked against
    its source on 2026-09-16): build items with `keychain.NewItem()` + `SetSecClass(keychain.SecClassGenericPassword)`,
    `SetService`, `SetAccount`; read with `SetMatchLimit(keychain.MatchLimitOne)` + `SetReturnData(true)` +
    `keychain.QueryItem`; write with `keychain.UpdateItem(query, update)` and, on
    `keychain.ErrorItemNotFound`, `keychain.AddItem`; delete with `keychain.DeleteItem`. Map
    `ErrorItemNotFound` → none, `ErrorAuthFailed` / `ErrorInteractionNotAllowed` / `ErrorUserCanceled` →
    `CREDENTIAL_REFUSED: `. Do **not** set a label, an access group or `kSecUseDataProtectionKeychain`:
    the query must match exactly what 2.x stored (class + service + account).
  - With `danieljoos/wincred` (checked the same day): `wincred.NewGenericCredential("com.codeflow.app." + key)`
    (defaults `Persist` to `PersistLocalMachine`), set `UserName = key` and `CredentialBlob = []byte(secret)`,
    `Write()`; read with `wincred.GetGenericCredential(target)` where `errors.Is(err, wincred.ErrElementNotFound)`
    means "none"; `Delete()` treating not-found as success.
  - `has_*` returns existence (AI key: non-empty); nothing returns a secret.
  - The scanner reads the staged diff the way `Diff.Staged` did — HEAD tree (or nothing, before the
    first commit) against the index, **full-file context (`ContextLines = 1_000_000`) with rename
    detection** — i.e. `git diff --cached --no-color --no-ext-diff -M -U1000000`; scans only `+` lines, first matching rule wins, one hit per line, placeholders skipped,
    masked preview per SEC-012; **never fails on the scan itself** (SEC-013). The 15 rules are
    transcribed in `10-security.md` — change `(?<val>…)` only if a Go version rejects it.
- **Tests**: `CredentialStoreTests` (7, real keychain — run only with `CODEFLOW_TEST_KEYCHAIN=1`,
  serially, with unique throwaway keys), `SecretCommandsTests` (3), `SecretScanTests` (11 + vectors).

### 8.5 Workspaces, skills and activity

- **Spec**: `09-workspace-scoped.md`, `03-storage.md` (stores, STORE-012…016).
- **Source size**: `Workspaces/` 2 027 LOC, `Activity/` 527.
- **Commands**: 27 in `WorkspaceCommands` — workspace/project CRUD, `default_clone_dir`, settings, workspace prompts (+ `default_workspace_prompt`), agents, review
  contexts, MCPs; 10 in `SkillCommands`; 7 in `ActivityCommands`. The review-run history commands
  (`list_review_runs`, `get_review_run`, `mark_review_finding`, `delete_review_run`,
  `delete_review_runs_for_pr`, `purge_workspace_review_runs`, `export_review_runs`) are registered in
  `ReviewCommands` but only need the review store, so they are ported here too (§11 Phase 2).
- **Go**: `backend/workspaces` (stores + `mcpconfig.go`, `skills.go`, `skillsync.go`,
  `skillinstaller.go`), `backend/activity`.
- **How**:
  - `move_project_to_workspace` updates `review_runs.workspace_id` in the same transaction
    (`BUG-STORE-b` closed).
  - Blank prompt = default (STORE-012): saving a blank string is the reset.
  - Skills: install runs `npx --yes skills add <repo> --skill <name>` (Windows `cmd /C npx …`) in the
    workspace skills directory, streaming both pipes as `skills:progress {line}`; guard "already exists"
    **before** npx (`BUG-WS-b` closed); remove deletes the folder first and propagates failure, then the
    row (`BUG-WS-a` closed). `SkillSync` copies enabled skills into `<project>/.claude/skills`.
  - `McpConfig` writes `{base}/workspaces/<id>/mcp.json` in the shape the Claude engine passes to
    `--mcp-config` (`05-ai-engines.md`).
  - `list_chat_conversations` groups by the app's `session_id`, never the engine's resume token (STORE-014).
- **Tests**: `McpConfigTests` (5), `ProjectStoreTests` (5), `SettingsTests` (11), `SkillCommandsTests` (9),
  `SkillFilesTests` (10), `SkillStoreTests` (7), `SkillSyncTests` (8), `UpsertTests` (8),
  `WorkspaceCommandsTests` (1), `WorkspaceIpcTests` (7 → registry contract), `WorkspaceStoreTests` (11),
  `ActivityCommandsTests` (2), `ActivityLogStoreTests` (10), `JobHistoryStoreTests` (5).

### 8.6 Git

- **Spec**: `04-git.md` (GIT-001…036), `XLANG-002`, vectors `git_branch`, `git_checkpoint`, `git_diff`,
  `git_stash`.
- **Source size**: 3 785 LOC; 47 commands in `GitCommands` (checkpoint commands included).
- **Go**: `backend/git` — `runner.go` (one function that runs `git -C <repo> -c core.quotepath=off
  -c color.ui=never …` through `shared/proc`, returns stdout, stderr, exit code), then one file per C#
  class: `status.go`, `diff.go`, `unifieddiff.go`, `branches.go`, `commitgraph.go`, `stash.go`,
  `merge.go`, `remotes.go`, `identity.go`, `checkpoints.go`, `network.go`, `repowalk.go`,
  `changecontext.go`, `promptdiff.go`.
- **The decision**: LibGit2Sharp has no Go equivalent with parity (§4.2), so every operation becomes a
  `git` invocation. Parity is proven by the vectors and the 165 Git tests against real temporary
  repositories, not by reading libgit2 semantics.
- **Already measured** (M-2, `docs/phase0-findings.md`, git 2.54): every command of this table was run
  against real temporary repositories with isolated `HOME`/`GIT_CONFIG_GLOBAL` — **55 of 55 checks
  pass**, including the 9 scenarios of `git_stash`, `git_branch`, `git_diff` and `git_checkpoint`
  vectors implemented with these exact commands. The table below is the corrected version.
- **Minimum git**: check `git version` once at start-up and fail git commands with a clear message below
  the floor. The floor is chosen in Phase 0 (step 0.10) as the oldest git that passes the M-2 script on
  both OSes; nothing in this table needs the recent `%(ahead-behind:…)` atom (see `list_branches`).

| Operation (spec rule) | libgit2 today | `git` command in Go | Traps |
|---|---|---|---|
| `is_git_repo` | `Repository.IsValid` | `git rev-parse --is-inside-work-tree` | exit code, not stderr text |
| `get_status` (GIT-001, 011) | `RetrieveStatus` + renames | `git status --porcelain=v2 -z --untracked-files=all --find-renames --branch` | Bucket priority conflicted → staged → untracked → unstaged; a path staged *and* modified is reported **once, as staged** (git prints it as one `1 MM` record — measured); renames are `2 R. … R100 <new>` followed by the old path; untracked files inside new directories are listed individually; `current_branch` null + `is_detached` from `# branch.head (detached)` (measured) |
| `list_commits` (GIT-020) | walk TOPOLOGICAL\|TIME | `git log --topo-order --date-order -z --format=%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s [--branches --remotes \| HEAD] -n <limit>`; refs from `git for-each-ref --format=%(objectname) %(*objectname) %(refname:short)` | `all_refs` walks branches + remotes, **not tags**; annotated tags peeled; `short_id` = first 7 chars; `limit 0` → `[]` |
| `list_unpushed_commits` (GIT-021) | walk hiding upstream | `git log HEAD --not --remotes …` / `@{upstream}..HEAD` per the rule | read GIT-021 in full (behaviour changed from 1.7.2) |
| `list_branches` (GIT-007) | branches + `graph_ahead_behind` | `git for-each-ref --format='%(refname)%00%(objectname)%00%(HEAD)%00%(upstream:short)%00%(upstream:track,nobracket)' refs/heads refs/remotes` — parse `ahead N`, `behind M`, `ahead N, behind M`, empty (in sync) or `gone` | **`%(ahead-behind:%(upstream))` is invalid** — atoms cannot nest (`fatal: failed to find '%(upstream'`, measured); `%(upstream:track,nobracket)` gave `ahead 1` and `git rev-list --left-right --count <b>...<upstream>` gave `1 0` (both measured). Remote branches report `0/0` and no upstream; a `gone` or unresolvable upstream silently `0/0` |
| `create_branch` / `delete_branch` (GIT-008/009) | refs | `git branch <name> [<start>]`; `git branch -D <name>` / `git branch -dr <remote/name>` | delete is a bare ref delete — `-D`, no merged check |
| `checkout_local_branch` (GIT-004) | safe checkout + set_head | `git checkout <name>` | "would be overwritten" (exit 1) → `CHECKOUT_CONFLICT: ` + message; other failures unprefixed |
| `checkout_detached` (GIT-005) | peel + set_head_detached | `git checkout --detach <rev>` | same conflict mapping |
| `checkout_remote_tracking` (GIT-006) | create/reuse + set_upstream | if `refs/heads/<short>` exists → `git checkout <short>` (upstream untouched, `AMBIGUOUS-GIT-a`); else `git checkout -b <short> --track <remote/short>` | error text for a name without `/`: `expected a name like 'origin/feature-x'` |
| `list_stashes` (GIT-015) | `stash_foreach` | `git stash list -z --format=%gd%x1f%gs%x1f%H` | index 0 = newest |
| `stash_save` | DEFAULT (+INCLUDE_UNTRACKED) | `git stash push [-u] -m <message or "WIP">` | stashes index + tree together |
| `stash_apply` / `stash_pop` | apply options → outcome string | `git stash apply stash@{i}` / `git stash pop stash@{i}` (with `LC_ALL=C`) | Outcomes `applied \| conflicts \| not_found \| uncommitted_changes \| unknown` are **results, not errors**. Measured classification: exit 0 → `applied`; exit 1 + `CONFLICT` → `conflicts` (index marked, **no `MERGE_HEAD`** — GIT-019); `log for 'stash' only has N entries` (exit 128) **or** `is not a valid reference` (**exit 1**, not 128 — corrected 2026-09-17 while porting: an index in a repository with no stash at all takes the second path, and requiring 128 sent the commonest "nothing is stashed" case to `unknown`) → `not_found`; exit 1 + `would be overwritten by merge` → `uncommitted_changes`; anything else → `unknown`. `pop` keeps the entry on conflict (measured) |
| `stash_drop` | `stash_drop` | `git stash drop stash@{i}` | |
| `rename_stash` (GIT-014) | drop + reappend reflog | `oid=$(git rev-parse stash@{i})`; `git stash drop stash@{i}`; `git stash store -m <new> <oid>` | renamed entry becomes `stash@{0}` — preserved reordering |
| `get_working_diff` / `get_staged_diff` / `get_commit_diff` (GIT-010) | Compare with Renames, full context | `git diff` / `git diff --cached` / `git diff <oid>^!` (root: `git diff --root <oid>` or empty tree `4b825dc642cb6eb9a060e54bf8d69288fbee4904`) with `--no-color --no-ext-diff -M -U1000000` (the C# `FullFile()` options are `ContextLines = 1_000_000` + renames); working diff also includes untracked content (`git diff --no-index /dev/null <file>` per untracked file — exit code **1** means "differences", not failure — or `git add -N` in a temp index) | Measured: `-U1000000` on a 50-line file gives one hunk `@@ -1,50 +1,50 @@`; binary → `Binary files … differ`, no hunks; a staged `git mv` shows `R` with both paths; `diff <oid>^! -- new old` prints `rename from/to`; root commit via `diff-tree --root`. Line numbers from `@@ -a,b +c,d @@` |
| `list_commit_files` / `get_commit_file_diff` (GIT-035) | tree compare | `git diff-tree -r -z -M --name-status --root <oid>`; `git diff <oid>^! -M -- <path> [<old_path>]` | no content for the list |
| `stage_file` / `stage_all` (GIT-013) | `add_path` / `remove_path` / `add_all` | `git add -- <path>` (missing on disk → `git rm --cached -- <path>`); `git add -A` | |
| `unstage_file` / `unstage_all` | reset to HEAD | `git reset -q -- <path>`; `git read-tree HEAD` (or `git reset -q`) | no HEAD yet → error, as today |
| `discard_file_changes` | force checkout from index | `git checkout -- <path>` | restores from **index**, not HEAD |
| `discard_all_changes` (GIT-012) | status walk + checkout_index + delete untracked | from porcelain v2: tracked worktree changes → `git checkout -- <paths…>`; untracked → delete files and walk up removing empty dirs | skip conflicted paths; staged-only changes untouched; first delete error aborts with `<path>: <error>` |
| `commit` (GIT-028) | ObjectDatabase.CreateCommit (one signature for author **and** committer) | `git commit --no-verify -m <msg>` with `--author "<name> <email>"` **and** `GIT_COMMITTER_NAME`/`GIT_COMMITTER_EMAIL` set to the same identity, resolved as `GitCommands.ResolveAuthor` does: explicit args → the workspace's `git_name`/`git_email` (found through `projects.local_path`) → repository config | libgit2 never runs hooks — `--no-verify` keeps "hooks never fire" (measured: a failing `pre-commit` hook blocked plain `git commit` but not `--no-verify`, and author = committer); keep executable-bit handling on non-Windows |
| `reset_to_commit` (GIT-002) | reset soft/mixed/hard | `git reset --soft\|--mixed\|--hard <oid>` | any unknown mode → mixed |
| `list_remotes` / `set_remote_url` | Network.Remotes | `git remote -v`; `git remote set-url <name> <url>` + `git remote set-url --push <name> <url>` | |
| `get_git_identity` / `set_git_identity` | global config | `git config --global --get user.name/email`; `git config --global user.name <v>` | missing key → `null`, not error |
| `merge_branch` (GIT-016) | merge_analysis + merge | `git merge-base <head> <theirs>` decides `up_to_date` / `fast_forward`; the fast-forward is `git reset --hard <theirs>`, **not** `git merge --ff-only` (corrected 2026-09-17 while porting: `--ff-only` refuses when the working tree holds changes the fast-forward would overwrite, which turns the outcome into an error; libgit2's `Reset(Hard)` overwrote them, and GIT-016 spells it "moves the ref and force-checks-out HEAD"); else `git merge --no-edit --no-verify -m "Merge branch '<name>'" <theirs OID>` | outcomes `up_to_date \| fast_forward \| merged \| conflicts`; local branch wins over same-named remote, so the resolved OID is passed rather than the name; the message is written out because merging by OID would otherwise read `Merge commit '<sha>'`; failed **without** unmerged entries (local changes in the way, unrelated histories) is an error, as libgit2 raised |
| `is_merging` (GIT-019) | `state == Merge` | `git rev-parse -q --verify MERGE_HEAD` | **not** "has conflicts" |
| `list_conflicts` / `resolve_conflict_side` / `mark_conflict_resolved` (GIT-017) | index stages | `git ls-files -u -z` (one line per **stage**, so dedupe to one row per file); `git checkout --ours\|--theirs -- <path>` → `git add -- <path>`; `git add -- <path>` | side must be `ours`/`theirs`; the side is written by `git checkout` rather than by reading `:2:`/`:3:` and writing the bytes here, so content lands byte for byte with no line-ending handling of our own; stage presence is checked first, for `that side has no content for this file (it was added/deleted)`; `ConflictVersions` from `:1:`/`:2:`/`:3:` through the **raw** runner (the line-decoding one strips `\r` and appends a final newline), lossy UTF-8, empty when absent |
| `complete_merge` / `abort_merge` (GIT-018) | two-parent commit / forced reset | refuse while `git ls-files -u` is non-empty (`There are still unresolved conflicts`), then `git commit --no-verify -m <msg>` — git builds the two-parent commit from `MERGE_HEAD` and clears the merge state itself; abort is `git reset --hard HEAD`, **not** `git merge --abort` (corrected 2026-09-17: `--abort` fails outside a merge, and GIT-018 requires abort to work there — the renderer's `is_merging` gate is what keeps it safe) | Measured: `git reset --hard HEAD` removes `MERGE_HEAD`, `MERGE_MSG` **and** `MERGE_MODE`, so libgit2's separate cleanup step has no equivalent here |
| Checkpoints (GIT-022…025) | in-memory index + commit under `refs/codeflow/checkpoints/` | temp index file: `GIT_INDEX_FILE=<tmp> git read-tree HEAD` (or empty), `git add -A`, `git write-tree`, `git commit-tree <tree> [-p HEAD] -m <kind>`, `git update-ref refs/codeflow/checkpoints/<id> <commit>`; changed paths = `GIT_INDEX_FILE=<tmp> git read-tree <ref>` + `git add -A` + `git diff --cached --no-renames --name-only <ref>` (sorted); prune to 20 newest (by committer time) on create; restore file-by-file with `git show <ref>:<path>` / delete paths absent from the snapshot; `remove_if_unchanged` deletes the ref when nothing changed | Measured: the real index is untouched (the temp index lives outside the worktree, so git even takes its lock there) and the three `git_checkpoint` vectors pass. The fallback identity must be **forced**: `git commit-tree` does not fail without a configured one, it invents it from the OS account — measured 2026-09-17, it wrote the machine user's real full name — so both `user.name` and `user.email` are read first and `CodeFlow <codeflow@local>` set through `GIT_AUTHOR_*`/`GIT_COMMITTER_*` when either is missing. `git update-ref -d` on a ref that is not there exits **0**, which is the forgiving behaviour GIT-025 needs. One temp index per `list` call, shared by all twenty diffs, or the whole repository is hashed twenty times. Internal compare **without** rename detection; the working-tree-only comparison is `DIVERGENCE-GIT-d` |
| Ignore rules (`RepoWalk`) | `Ignore.IsPathIgnored` | `git ls-files -z --cached --others --exclude-standard` for walks; `git check-ignore -z --stdin` for batches | git lists cached and untracked paths in two groups — **sort** before use (measured); `MaxFiles = 20 000` (`Files/RepoWalk.cs`) applies to the walk |
| Network (GIT-034) | already `git` CLI | unchanged commands: `clone <url> <dest>`, `fetch <remote\|origin> [refspecs…]`, `pull --no-edit`, `push`, and `push -u origin <branch name>` (refused first with `cannot push -u from a detached HEAD` when detached **or unborn**); stream both pipes line by line as `git:progress {op,line}`, then `git:done {op,success,message}` | **no cancellation, no timeout** (`AMBIGUOUS-GIT-b`); inherited environment (§8.0); failure `git <op> failed: <detail>` where detail = stderr, else stdout, else `git <op> exited with exit status: <code>` — redact before logging. The progress pump needs its **own splitter**: git redraws one line in place with a bare `\r` and no newline until a phase ends, so `bufio.ScanLines` delivers a whole download as one line at the very end. `proc.ProgressLines` follows .NET's `StreamReader.ReadLine` (`\n`, `\r\n`, lone `\r`), while `proc.Lines` keeps its parser-safe behaviour. Measured while porting: since git 2.27 a **divergent pull refuses outright** unless `pull.rebase`/`pull.ff` is configured, so GIT-037's editor hazard only exists on repositories whose owner chose merge |
| `UnifiedPatch` (Azure diffs, `06-providers.md`) | temporary bare repo | `git init --bare <tmp>`, `git hash-object -w --stdin`, `git diff <blobA> <blobB>` | delete the temp repo in all paths |

- **Tests**: 165 tests across `BranchContributionTests` (7), `BranchesTests` (14), `ChangeContextTests` (11),
  `CheckpointsTests` (11), `CommitGraphTests` (8), `DiffTests` (22), `GitCommandsTests` (10),
  `GitNetworkTests` (8), `IdentityTests` (1), `MergeTests` (16), `PromptDiffTests` (23), `RemotesTests` (3),
  `RepoStatusTests` (8), `StashTests` (10), `UnifiedDiffTests` (13). Helpers `TempRepo`/`GitFixtures` →
  a Go `testrepo` helper building repositories with real `git` in `t.TempDir()` (set `HOME` and
  `GIT_CONFIG_GLOBAL` to a temp file so the developer's config never leaks in).

### 8.7 Files: operations, search/replace, watcher

- **Spec**: `11-files-search-terminal.md` (FILE-001…018), `10-security.md` (`scan_staged_secrets`),
  vectors `fsops`, `search`.
- **Source size**: 1 794 LOC; 13 file + 3 watcher commands.
- **Go**: `backend/files` — `fileops.go`, `pathguards.go`, `repowalk.go` (shared with git), `search.go`,
  `globset.go`, `watcher.go`.
- **How**:
  - Strict UTF-8 on read/write (reject invalid, as C# did); `create_file` never truncates (FILE-004);
    `move_path` refuses self-containment and collisions (FILE-003); `list_dir` directories first, then
    case-insensitive name (FILE-002); a listing never guesses an entry's type (FILE-017).
  - `write_file_bytes` is the only op not scoped to a repo (FILE-005) and takes `ByteArray` (§3.4).
  - `open_in_vscode`: Windows `cmd /C code <path>`, else `code` found on PATH (FILE-006);
    `open_in_default_app` / `reveal_in_file_manager`: `app.Browser.OpenFile(path)` /
    `app.Env.OpenFileManager(path, true)` via a `desktop` interface (or `open`/`explorer.exe /select,`).
  - Search: caps 20 000 files (`MaxFiles`, which lives in `Files/RepoWalk.cs` and bounds every walk),
    1 MiB per file, 400 chars per line, 20 hits per file (`Files/Search.cs`, FILE-008); matcher
    composition escape/regex → whole word → case-insensitive (FILE-009); include/exclude are independent
    stages, exclude wins (FILE-010); port `GlobSet` (glob → regex) rather than swapping in a glob library,
    because its translation is what the tests pin (`GlobSetTests`, 13).
  - Replace plans every edit, checkpoints (§8.6), then writes (FILE-011).
  - **Watcher**: `syncthing/notify` watching `<repo>/...` recursively into a buffered channel; a
    **200 ms ticker** drains a dirty flag and emits `repo:fs-changed {repo_path}` at most once per
    **400 ms** with leading edge plus catch-up (FILE-012); ignore git bookkeeping noise (`*.lock`,
    `FETCH_HEAD`, `COMMIT_EDITMSG` — FILE-013) **plus the `.git` directory and the watched root
    themselves** (`DIVERGENCE-FILE-e`, measured 2026-09-17: FSEvents emits a directory-level event
    for both alongside each file event, so the three names alone leave the feedback loop intact).
    A dropped event is survivable here and needs no error path — the payload carries no information
    beyond "something changed", so one of a burst arriving is enough. One watcher per repo path;
    `stop_watching` closes it; all stopped on shutdown.
  - **Regex**: Go's `regexp` is RE2, so a user's lookahead/lookbehind/backreference is rejected with
    `invalid regular expression: …` where 2.x accepted it (`DIVERGENCE-FILE-f`). In exchange no
    query can hang a search over a large repository.
- **Tests**: `AnchorPatternTests` (4), `FileCommandsTests` (7), `FileOpsTests` (17), `GlobSetTests` (13),
  `RepoWatcherTests` (7, serial, real filesystem), `SearchTests` (12), `TextHandlingTests` (9),
  `WatcherCommandsTests` (6).

### 8.8 Terminal

- **Spec**: `11-files-search-terminal.md` (Terminal section, FILE-014…016), `AMBIGUOUS-FILE-c`.
- **Source size**: 419 LOC; `open_terminal`, `write_terminal`, `resize_terminal`, `close_terminal`.
- **Go**: `backend/terminal` — `registry.go`, `shellresolver.go`.
- **How**:
  - `xpty.NewPty(100, 30)` (width = cols, height = rows), `pty.Start(cmd)` with the resolved shell and
    `TERM=xterm-256color`, `pty.Resize(cols, rows)`; wait for the child with `xpty.WaitProcess(ctx, cmd)`
    (on Windows the process is started by ConPTY, so plain `cmd.Wait` cannot be used). API compiled
    and run in M-6 (macOS) and W-4 (Windows).
  - Shell: macOS `$SHELL` or `/bin/bash`, no arguments. Windows **Git Bash only** (FILE-014):
    `git --exec-path` (the C# code waits 5 s *after* a blocking `ReadToEnd`, so its "timeout" does not
    bound a hung git — use a real context deadline in Go) then check `bin\bash.exe` under that directory
    and its parents — **6 directories in total** (the exec path itself plus 5 ancestors); fall back to
    `C:\Program Files\Git\bin\bash.exe` and `C:\Program Files (x86)\Git\bin\bash.exe`; args
    `--login -i`; otherwise error `Git Bash not found — install Git for Windows (https://git-scm.com/download/win)`.
  - Reader: **4 096-byte reads**, each chunk decoded independently (a character split across chunks
    becomes U+FFFD — preserved, `AMBIGUOUS-FILE-c`), pushed into a channel of capacity **64** that
    **blocks** when full (back-pressure, nothing dropped), a second goroutine emits
    `terminal:output {id, data}`; `terminal:exit {id}` only after the output drained — exit is detected by
    the reader ending, never by the child's status (FILE-015).
  - **How the reader ends — measured, not assumed.** With `xpty` the read loop does **not** return when the
    shell exits: on macOS (M-6) the process exited cleanly but the reader stayed blocked until
    `pty.Close()`, then returned `EOF`; on Windows (W-4, ConPTY + Git Bash) the same — it returned
    `The handle is invalid.` only after `Close()`. So the session keeps
    FILE-015's observable order with: goroutine A waits for the process → closes the PTY; goroutine B's
    read loop then ends → drains the channel → emits `terminal:exit`. `close_terminal` kills the process
    tree and follows the same path. Treat the read error that follows `Close()` (`EOF`, or "handle is
    invalid" on Windows) as the normal end, not as a failure to report.
  - **Input written before the shell is ready can lose its first character** (W-4: a command typed ~1.5 s
    after starting Git Bash through ConPTY arrived as `cho …`; ConPTY and bash exchange terminal queries
    at start-up). In the app input comes from the user after the prompt appears, so nothing to change —
    but a Go test that types into a fresh PTY must wait for the prompt first.
  - ConPTY sends mode sequences such as `ESC[?9001h`/`ESC[?1004h` and bracketed paste `ESC[?2004h`;
    forward them to xterm.js untouched (it understands them) and check the terminal view in Phase 3.
  - `resize_terminal` params `id`, `cols`, `rows` bound by name (FILE-016).
  - **The PTY owns the spawn**, so `proc.Cmd.Start` never runs and its Windows Job Object is never
    attached — `proc.Cmd.AdoptStarted()` after `pty.Start` is what keeps `close_terminal` killing
    the tree rather than orphaning whatever the shell was running. The child's context is
    `context.WithoutCancel`: a terminal outlives the call that opened it, and the context Wails
    hands a command dies the moment it returns.
- **Tests**: `ShellResolverTests` (5), `TerminalCommandsTests` (5), `TerminalSessionTests` (7, serial, real PTY).

### 8.9 AI engines and the run lifecycle

- **Spec**: `05-ai-engines.md` (AI-001…056, the per-engine matrix, Run lifecycle, Prompt constants),
  `XLANG-001/003/004/005/006/007/015`, vectors `ai`, `claude`, `codex`, `gemini`, `opencode`, `openai`,
  prompts in `docs/verbatim/prompts/`.
- **Source size**: 5 482 LOC + 17 prompts; 13 commands (+ `review_changes`, `dbml_assist` use it).
- **Go**: `backend/ai` — `routing.go`, `runregistry.go`, `runner.go`, `signals.go` (quota/auth),
  `discovery.go` (binaries, models, versions), `scratch.go`, `operations.go`, `turn.go`, `prompts/`
  (`//go:embed *.txt`, `prompts.go` exposing one constant per file), `engines/{claude,codex,gemini,opencode,openai,ollama}.go`.
- **How**:
  - `EngineFor(provider)`: unknown → `claude`; known providers `claude, codex, gemini, opencode, ollama,
    local, openai` (AI-001).
  - **Engine argv** (copy exactly from `05-ai-engines.md`; summary):
    `claude -p <prompt> [--append-system-prompt …] [--model …] --output-format stream-json --verbose
    --setting-sources user [--tools X --allowedTools X] [--permission-mode acceptEdits] [--mcp-config
    <path> --strict-mcp-config] [--resume <id>]`, data on stdin;
    `codex exec [resume <id>] <fixed pointer text> --skip-git-repo-check [--model …]
    --sandbox workspace-write|read-only -c approval_policy="never" [--cd <cwd>]`, instructions + data on stdin;
    `agy -p <brief>` (over 12 000 chars → temp file + `--add-dir`) `[--model …] [--dangerously-skip-permissions]
    [--continue]`, no stdin;
    `opencode run … --format json [--model …] [--auto] [--dir …] [--session …] [--file <tmp>]`, no stdin;
    Ollama `POST /api/chat` (`stream:false`), models `GET /api/tags`;
    OpenAI-compatible `POST /chat/completions` with the keychain key as bearer (never in a process).
  - **Binary discovery** (AI-005…007): manual `{provider}_binary_path` wins; else known install dirs, then
    inherited PATH (§15.2.5); Windows tries `.exe`, `.cmd`, `.bat` per directory, exe first; always an
    absolute path; the child's `PATH` = augmented list.
  - **Run registry** (AI-009…013): register a cancel func under `run_id` **before** spawning; feed stdin
    concurrently; read both pipes in 8 192-char chunks — **8 192 is also the forced-emit threshold** for
    text that has no `\n` yet (`MaxPendingChars`) — split on `\n` only (with the `\r` progress escape of
    AI-010), strip ANSI and `TrimEnd`, drop blank lines, cap lines at 2 000 Unicode scalars **with a `…`
    suffix**, emit `ai:output {run_id, stream, line}`; **silence timeout 10 minutes, reset on every read**
    (`CancelAfter`); cancel kills the process tree (§8.0), waits ≤ 2 s for readers, and fails with the bare
    marker `RUN_CANCELLED::` (nothing after it) or `RUN_TIMED_OUT::<whole minutes>` — e.g.
    `RUN_TIMED_OUT::10`, and nothing after the marker when the deadline is under a minute.
  - Runner: retry **once** only for read-only runs whose failure matches `TransientNetwork`; incomplete
    stdin delivery fails the run; temp payloads deleted in `defer` on every path (`BUG-AI-a` closed);
    start-up sweep of scratch files older than 1 h.
  - Signals: `QUOTA_EXCEEDED::` (11-phrase dictionary over the whole output — `BUG-AI-b` **preserved**),
    `AUTH_EXPIRED::` only on failure paths, after the quota test (AI-056); never double-prefix.
  - Routing cascade (XLANG-005): `ai_provider_{task}` (blank = unset) → `ai_provider` → `claude`; model
    `{provider}_{task}_model` → for `commit` only the engine's commit model when non-empty → `{provider}_model`.
  - Prompts: load with `//go:embed`; a test compares each embedded file's SHA-256 with the file in
    `docs/verbatim/prompts` until that directory is deleted, then with a checked-in digest list.
- **Tests**: `AgentStreamingTests` (17), `AiCommandsTests` (14), `AiIpcTests` (3 → contract), `AiOperationsTests`
  (25), `AiRoutingTests` (17, includes `The_ten_task_keys_are_verbatim`), `AiTextFooterTests` (5),
  `BinaryDiscoveryTests` (11), `ChatTurnTests` (14), `ClaudeCodeTests` (10 + vectors), `EngineScratchTests` (4),
  `EngineVectorTests` (11 + vectors), `NetworkRetryTests` (3), `PromptsTests` (4), `ReviewOperationTests` (11),
  `StdinDeliveryTests` (4). Helpers `ScriptedEngine`/`Recorder` → a fake executable built by the test
  (`go build` of a tiny program in `testdata/`) that prints scripted output.

### 8.10 VCS providers

- **Spec**: `06-providers.md` (PROV-*), `XLANG-012/013/014`, vectors `pr_link`, `ado`.
- **Source size**: 5 704 LOC (largest folder); 19 commands.
- **Go**: `backend/providers` (`prlink.go`, `repodetection.go`, `knownhosts.go`, `linkedrepo.go`,
  `pullrequesthosts.go`, `statustext.go`, `workitemlink.go`), `providers/github`, `providers/azure`.
- **How**:
  - `IPullRequestHost` stays an interface: two real implementations (GitHub, Azure) — the source repo's
    own rule for interfaces.
  - GitHub REST `https://api.github.com` or `https://<host>/api/v3`; GraphQL `…/graphql` or
    `/api/graphql`; `Authorization: Bearer`, `X-GitHub-Api-Version: 2022-11-28`. 422 whose `errors[]`
    contains `Can not approve your own pull request` (case-insensitive) → `SelfApproval`.
  - Azure `https://dev.azure.com/{org}/…`, api-version `7.1` / `7.1-preview` (comments `7.1-preview.4`,
    iterations `7.1-preview.1`); auth `Basic base64(":" + PAT)`; 401/403 → `Unauthorized`.
  - Preserve `BUG-PROV-a` (repo id encoded in 3 functions, raw in 9) and `BUG-PROV-b` (only `%20`
    decoded): port the call sites one-to-one.
  - Link and posting paths were verified live on 2026-08-01 (`90-ambiguities.md`) — keep request bodies
    identical; `set_pr_thread_status` is still `UNVERIFIED`.
- **Tests**: `AzureClientTests` (45), `AzureDiffTests` (18), `AzurePostingTests` (10),
  `AzureWorkItemClientTests` (21), `GitHubClientTests` (29), `GitHubPostingTests` (25), `LinkedRepoTests` (12),
  `PrDescriptionDraftTests` (8), `PrLinkTests` (2 + vectors), `ProviderIpcTests` (26 → handler tests),
  `RepoDetectionTests` (14), `UnifiedPatchTests` (5), `WorkItemLinkTests` (6). `FakeHttpHandler` →
  `httptest.Server` or a `RoundTripper` fake.

### 8.11 PR review pipeline

- **Spec**: `07-review-pipeline.md` (the flagship feature), `XLANG-001/014`, `90-ambiguities.md` (live run).
- **Source size**: 2 435 LOC; 11 commands registered in `ReviewCommands`. `01-ipc-surface.md` lists 22
  under that file: several PR commands are actually registered in `ProviderCommands` (for example
  `resolve_pr_link`, `list_pr_comment_threads`, `repo_web_url`) — the contract test settles where each
  one lives.
- **Go**: `backend/review` — `reviewrun.go`, `memory.go` (parse, reconcile, render, renumber),
  `posting.go`, `store.go`.
- **How**:
  - PR checkout for review: `git fetch origin +refs/pull/{id}/head:refs/remotes/origin/codeflow-pr-{id}`
    (GitHub) or the Azure equivalent from the spec; link-only reviews write `PULL_REQUEST.md` and
    `changes.diff` under `{base}/pr-link-reviews/<slug>/`.
  - `ReviewMemory` parses findings with the five `XLANG-001` regexes (severity from the **word**, emoji
    fallback), lifts `## 👍 Lo que está bien` / `## 🗒️ Notas` **before** slicing findings, reconciles
    one-to-one (`BUG-REVIEW-b` closed), renumbers headers only when the pairing holds
    (`DIVERGENCE-REVIEW-a`), marks shallower re-reviews `fuera_de_alcance` (`AMBIGUOUS-REVIEW-b` decision).
  - Posting on GitHub compares the run's analysed head SHA with the current head and refuses with
    `STALE_REVIEW: ` (`XLANG-014`); Azure's half of `BUG-REVIEW-a` stays open.
- **Tests**: `ReviewCommandsTests` (10), `ReviewFromLinkTests` (6), `ReviewMemoryParseTests` (15),
  `ReviewMemoryReconcileTests` (21), `ReviewMemoryRenderTests` (9), `ReviewMemoryRenumberTests` (6),
  `ReviewPostingFromLinkTests` (4), `ReviewPostingTests` (13), `ReviewRunStoreTests` (11), `ReviewRunTests` (10).
  Add a cross-language test: the same markdown fixtures run through Go's parser and
  `frontend/src/lib/parseAnalysis.ts` (Vitest) must produce the same findings.

### 8.12 Work items (tickets)

- **Spec**: `14-work-items.md`, `XLANG-016/017`.
- **Source size**: 3 145 LOC; 17 commands (all read-only except `comment_ticket`).
- **Go**: `backend/tickets` — `sync.go`, `mirror.go`, `html.go` (HTML → Markdown), `review.go`,
  `verdict.go`, `comment.go`, `paths.go`, `store.go`, `accounts.go`, `criteria.go`.
- **How**: Azure Boards only (Jira only recognised in branch names); mirror Markdown + attachments under
  `{base}/tickets/` or `tickets_root_dir`; `TICKET_NOT_LINKED: ` / `TICKET_SYNC_FAILED: ` prefixes with
  the documented cache fallback; verdict parser tolerant (unreadable → `no verificable`).
  For HTML → Markdown, port `TicketHtml` by hand (16 tests pin its output) instead of adopting a library.
- **Tests**: `AzureBoardsEndToEndTests` (8) and `AzureCommentEndToEndTests` (1) skip unless
  `CODEFLOW_E2E_ADO_ORG`/`CODEFLOW_E2E_ADO_PROJECT` are set (use `t.Skip` with the reason);
  `TicketAccountsTests` (12), `TicketBranchRefTests` (4), `TicketCommandsTests` (2), `TicketCommentTests` (8),
  `TicketCriteriaReaderTests` (11), `TicketHtmlTests` (16), `TicketMirrorTests` (15), `TicketPathsTests` (11),
  `TicketReviewStoreTests` (5), `TicketStoreTests` (10), `TicketVerdictTests` (10).

### 8.13 API client (HTTP, GraphQL, WebSocket, Socket.IO, MQTT)

- **Spec**: `08-api-client.md` (API-001…045), `XLANG-008/009/010/011`, vectors `http`, `ws`, `socketio`,
  `mqtt` (`grpc` stays deferred).
- **Source size**: 4 322 LOC; 41 registered commands — `ApiCommands` 27 (storage), `ApiHttpCommands` 5
  (HTTP send/cancel and file helpers such as `api_read_file_base64`), `ApiStreamCommands` 9 — plus the
  2 deferred gRPC names.
- **Go**: `backend/apiclient` — `stores/` (tree, environments, history, cookies), `httpsend.go`,
  `decoding.go`, `digest.go`, `sigv4.go`, `registry.go` (in-flight HTTP cancel), `streams.go`,
  `websocket.go`, `socketio.go` (+ `framing.go`), `mqtt.go` (+ `mqtt311.go`, `mqtt5.go`), `tlspolicy.go`.
- **How — HTTP** (`api_send_http`, `api_send_http_tracked`, `api_cancel_http`):
  - **A new `http.Transport` per send** (API-001): `DisableCompression: true` (decompress yourself),
    `Proxy` from the request, `TLSClientConfig{InsecureSkipVerify: !verify_ssl, Certificates: [pkcs12]}`;
    `http.Client{CheckRedirect: func(…) error { return http.ErrUseLastResponse }}` and follow redirects
    by hand with the shared hop counter (default max 10) and the downgrade rules — `BUG-API-a` preserved.
  - Add `Accept-Encoding: gzip, br, deflate` only when the user did not set it (XLANG-008); decode gzip
    (`compress/gzip`), deflate (accept both zlib-wrapped — `compress/zlib` — and raw `compress/flate`
    streams; the `HttpDecodingTests` names in the inventory and `08-api-client.md` §Response handling
    pin the expected results), br (`andybalholm/brotli`).
  - Default timeout 30 000 ms; response cap 50 MiB **truncates, does not error** (API-020); binary
    detection → base64; charset transcoding (`golang.org/x/text`).
  - No cookie jar (`UseCookies=false`, API-024); `Set-Cookie` parsed with path default `/`
    (`BUG-API-c` preserved).
  - Digest (RFC 7616, MD5/SHA-256, `BUG-API-b` preserved) and AWS SigV4 (S3 single-encoded URI, others
    double) are hand-written today — port them against the vectors (SigV4 uses Amazon's published vectors).
- **How — streams** (`api_ws_connect`, `api_ws_send`, `api_socketio_connect`, `api_socketio_emit`,
  `api_mqtt_*`, `api_stream_disconnect`):
  - Connections live in a registry under the **app-lifetime context** (§3.7); **no automatic reconnect
    anywhere** (API-038, `DIVERGENCE-API-a`).
  - WebSocket via `coder/websocket` with the scheme normalisation, header merge and keep-alive rules
    (API-025…029).
  - Socket.IO/Engine.IO framing hand-ported (API-030…037): packet types 0–6, `EIO=` query, namespaces,
    acks parsed and logged but not correlated (`AMBIGUOUS-API-a`), v3 vs v4 heartbeat direction,
    websocket-only absolute handshake URL.
  - MQTT: `mqtt311.go` on `paho.mqtt.golang`, `mqtt5.go` on `paho.golang` (`autopaho`/`paho` packages),
    one internal interface; client id generation, QoS clamping, last-will only with a topic, keep-alive
    floor asymmetry, max packet size, `ws://` refused (API-040…045).
  - **TLS with verification off** (`StreamTlsPolicy`, `BUG-API-d` closed, API-027/039): Go's
    `InsecureSkipVerify` skips chain and hostname checks while crypto/tls still verifies the handshake
    signature with the peer's key (read in Go 1.26.4's `handshake_client.go`: only the
    `certs[0].Verify(opts)` chain check is gated by `InsecureSkipVerify`; `handshake_client_tls13.go`
    checks `CertificateVerify` unconditionally) — the same strength as the C# policy; add `VerifyConnection` requiring
    at least one peer certificate. Same policy object for WebSocket, Socket.IO and MQTT.
  - Events `api:stream-message` / `api:stream-status` with the payload types of `08-api-client.md`.
- **How — files**: `api_read_file_base64` (MIME table XLANG-009, lowercase extension, default
  `application/octet-stream`), `api_pick_file` (dialog with extension filters), `api_save_file`,
  `api_read_text_file`.
- **Tests**: `ApiCommandsTests` (7), `ApiStartupTests` (4), `ApiStoresTests` (13), `ApiTreeStoreTests` (16),
  `HttpDecodingTests` (19), `HttpSendTests` (20, loopback server → `httptest`), `SigV4Tests` (10 + vectors),
  `StreamCommandsTests` (13), `StreamFramingTests` (20 + vectors).

### 8.14 Schema designer (DBML)

- **Spec**: `15-dbml.md` (DBML-*), `XLANG-018`.
- **Source size**: 1 914 LOC; 10 commands.
- **Go**: `backend/dbml` — `documents.go`, `layouts.go`, `connections.go`, `assistant.go`,
  `snapshot.go`, `introspect/{postgres,sqlserver,mysql,sqlite}.go`.
- **How**:
  - Drivers per §4.2, no pooling (open, query, close); for the three **servers** `Catalogue.TimeoutSeconds
    = 30` is both the connect and the command timeout; default ports 5432 / 1433 / 3306.
  - SQL Server: `encrypt` per `UseTls`, `TrustServerCertificate = !UseTls`; **empty username →
    integrated authentication** (Windows SSPI via `integratedauth/winsspi`; on macOS it needs Kerberos —
    record what happens in Phase 7).
  - SQLite introspection checks `File.Exists` first, then opens the **user's** file read-only with no
    pooling and closes it immediately (C#: `Mode = ReadOnly`, `Pooling = false`, **no** timeout — the
    30 s catalogue timeout does not apply to SQLite). Go: `file:<path>?mode=ro&_pragma=query_only(1)`,
    `SetMaxOpenConns(1)`, `Close()` in `defer` — the 2.7.1 fix "stop holding the user's SQLite file open"
    must hold (Phase 7 checks the file can be renamed right after on Windows).
  - Passwords in the keychain under `db-password:{id}`; a refused or unreachable server → error starting
    with `DB_CONNECTION_REFUSED: ` followed by the driver's sentence **without the connection string**
    (DBML-024).
  - The DBML parser itself stays in the renderer (`@dbml/core`).
- **Tests**: `DbmlAssistantTests` (13), `DbmlCommandsTests` (17), `DbmlSnapshotBuilderTests` (11),
  `ServerIntrospectorTests` (4, need real servers — gate with env vars; Postgres/MySQL/SQL Server via
  containers locally), `SqliteIntrospectorTests` (13).

### 8.15 Update

- **Spec**: `02-bootstrap-platform.md` BOOT-021; §2.9 of this file.
- **Source size**: 826 LOC; `update_current_version`, `update_check`, `update_download`.
- **Go**: `backend/update` — `check.go`, `releaseversion.go`, `assets.go`, `digest.go`, `download.go`,
  `handoff.go`, `commands.go`. `digest.go` is split out from `download.go` because reading a
  published `.sha256` is a pure text rule with its own suite (`UpdateDigestTests`, 9 cases) and the
  v1.7.5 single-entry history behind it; `commands.go` holds `Deps`, the resolved `Service` and the
  three registrations, as every other feature package does.
- **How**: port §2.9 exactly (feed, token cascade incl. `gh auth token`, reasons, version compare, asset
  choice, digest rules including the single-entry rule, `~/Downloads`, 81 920-byte chunks, progress every
  256 KiB, delete on mismatch). Hand-off, same as 2.7.1: Windows **opens** the installer through the
  shell (`app.Browser.OpenFile(path)`, the equivalent of `UseShellExecute`), macOS **opens** the `.dmg`
  (`app.Browser.OpenFile(path)` — `open` mounts it); neither quits the app, the renderer's *Restart*
  does. Because 3.0's own installer will later be started the same way while 3.0 runs, the NSIS
  close-running-app logic of §10.3 covers 3.x → 3.y updates too.
  **Why not Wails' `pkg/updater`** for 3.0: it swaps a binary/`.app` from a zip/tar.gz and has its own
  events (`wails:updater:*`) and window — a different contract from the renderer's
  `updateStore`/`updater.ts` and from what 2.7.x expects to find on the release. Revisit after 3.0 (§14 D5).
- **Tests**: `ReleaseVersionTests` (8), `UpdateAssetTests` (9), `UpdateDigestTests` (9), `UpdateDownloadTests`
  (6, `httptest` server + keychain token).

---

## 9. Testing strategy

### 9.1 Principles

- **Every feature ships with its tests**; a phase is not done while its inventory entries are unticked.
- Port **behaviour**, not C# structure. The 1 232 method names in `docs/verbatim/test-inventory.md` are
  sentences describing behaviour — keep them as subtest names so parity is grep-able:

  ```go
  func TestCredentialStore(t *testing.T) {
  	t.Run("Stores_reads_and_deletes_a_secret", func(t *testing.T) { … })
  	t.Run("Deleting_something_that_is_not_there_succeeds", func(t *testing.T) { … })
  }
  ```

- **Hand-written fakes, no mocking framework** (source repo rule). `testify` for `require`/`assert` only.
- A skip always says why: `t.Skip("needs CODEFLOW_TEST_KEYCHAIN=1: touches the real OS keychain")`.
- Tests never write into the user's real `~/CodeFlow` or git config: directories and `HOME` /
  `GIT_CONFIG_GLOBAL` are injected per test.

### 9.2 Commands

| Purpose | Command |
|---|---|
| All Go tests | `go test ./...` |
| One package / one test | `go test ./backend/git -run 'TestStash/Rename'` |
| Race detector (before declaring a phase done) | `go test -race ./...` |
| No cache | `go test -count=1 ./...` |
| Vet + lint | `go vet ./...` · `golangci-lint run` |
| Vulnerabilities | `govulncheck ./...` |
| Renderer | `pnpm -C frontend typecheck` · `pnpm -C frontend lint` · `pnpm -C frontend test` |
| Smoke | `go run . --smoke-test` (and the packaged binary with `--smoke-test`) |

### 9.3 Test vectors (`backend/shared/testvectors`)

Port `FixtureCatalog`:

- Locate `docs/business-rules/test-vectors` by walking up from the package directory (`go test` runs with
  the package directory as working directory).
- A file holds one fixture object or an array of them: `{ $schema, sourceFile, kind: "vector"|"scenario",
  setup: { seedSql }, cases: [{ id, name, input, expected, notes }] }`; property names are matched
  case-insensitively (C# did) — decode through `map[string]json.RawMessage` with a lowercase lookup.
- JSON **with comments**: strip `//` and `/* */` outside strings before decoding (only
  `search.vectors.json` has them).
- API: `testvectors.Cases(t, "git_stash.vectors.json")` returning typed cases; `t.Run(c.ID+"_"+c.Name, …)`.
- Port the 5 `FixtureCatalogTests` (catalog integrity: unique ids, every file parses, seeds exist).
- `sourceFile` values still name C# files; leave them (they are documentation), do not "fix" the vectors.

### 9.4 Contract tests (the ones that catch silent breakage)

1. **Command coverage** (`backend/app/contract_test.go`): read `frontend/src/lib/ipc/commands.ts`,
   `frontend/src/lib/ipc/apiCommands.ts` and `frontend/src/lib/bridge/updater.ts`; extract names with
   `invoke(?:<[^>]*>)?\(\s*["']([a-z0-9_]+)["']`; require every name to be registered except the 11
   deferred ones (§2.4), and require no registered name the renderer does not call except an explicit
   allow-list. Expect 246 found / 235 registered.
2. **Wire shape**: for every response type, marshal a populated value and compare its key set with a
   golden file under `backend/<feature>/testdata/wire/<type>.json`; `jsonwire.AssertNoNilSlices` over
   every golden. Write the goldens from `frontend/src/types/*.ts` field names, never from the Go structs.
3. **Sentinels**: one test per prefix through `bridge.Service.Invoke` asserting `strings.HasPrefix`.
4. **Events**: each emitted payload marshals to the names in §2.5.
5. **VERBATIM**: prompt digests (§8.9), task keys (`The_ten_task_keys_are_verbatim`), keychain key formats
   (`Key_formats_are_reproduced_byte_for_byte`), Clock format, MIME table, `Accept-Encoding`.
6. **Cross-language parsers**: the same markdown fixtures through Go (`review.ParseFindings`,
   `tickets.ParseVerdict`) and Vitest (`parseAnalysis.ts`, `parseTicketVerdict.ts`) produce the same
   result (fixtures in `docs/business-rules/test-vectors/`-style JSON shared by both).

### 9.5 Tests that need the real environment

| Resource | Rule |
|---|---|
| `git` | Real `git` in `t.TempDir()`; `HOME`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_NOSYSTEM=1` set per test; configure `user.name/email` locally |
| OS keychain | Only with `CODEFLOW_TEST_KEYCHAIN=1`; never `t.Parallel()`; package-level mutex; unique throwaway keys cleaned in `t.Cleanup` |
| PTY, watcher, temp files | Serial (no `t.Parallel()`), same classes as the C# `SerialTemporaryFiles` collection |
| HTTP servers | `httptest.NewServer` / `NewTLSServer` on loopback — needs local `bind()`; a sandbox that blocks it will hang or fail (the source repo's AGENTS.md warns about exactly this) |
| Database servers | `ServerIntrospectorTests` only with `CODEFLOW_TEST_POSTGRES_URL` / `…_MYSQL_URL` / `…_SQLSERVER_URL` |
| Azure DevOps live | only with `CODEFLOW_E2E_ADO_ORG` and `CODEFLOW_E2E_ADO_PROJECT` |

### 9.6 Renderer tests

- The 66 `*.test.ts` + 4 `scripts/*.test.mjs` run unchanged (they mock `lib/ipc/*`, not the bridge).
- Rewrite `lib/bridge/host.test.ts` (§7.2); add tests for `detectPlatform` user-agent strings and for
  the external-link click handler.
- Rendered behaviour (WebKit fixes W1–W13) is verified on the real app per OS (§9.8, §11 Phase 1/9).

### 9.7 Differential parity oracle (recommended)

The installed **2.7.1 app still contains its core**:
`/Applications/CodeFlow.app/Contents/Resources/core/codeflow-core` (Windows:
`<install dir>\resources\core\codeflow-core.exe`). §2.3 documents its protocol completely, so a small
Go tool (`tools/parity`, not shipped) can:

1. create a temp directory and set `HOME` (macOS) to it so the old core's base directory is
   `<tmp>/CodeFlow` — **never run it against the real `~/CodeFlow`**;
2. start `codeflow-core --app-version 2.7.1`, write a UUID token + `\n` to stdin, wait for
   `codeflow-core ready <endpoint>`, open the `rpc` connection with the hello frame;
3. replay a scripted list of requests (read-mostly: `list_workspaces`, `create_workspace`, `get_status`,
   `list_branches`, `get_working_diff` on a fixture repo, `search_repo`, `scan_staged_secrets`,
   `default_*_template`, `api_load_tree`, …) against it and against `bridge.Service.Invoke` on a second
   temp base directory;
4. compare the JSON after normalising ids, timestamps and absolute paths.

Any difference is either a bug in the port or a behaviour to record in the spec — never silently
accepted.

**Feasibility proven** (M-3): a ~200-line Go client already drove the installed 2.7.1 core this way —
ready line, hello frame, `list_workspaces`, `create_workspace`, `get_status`, `list_branches`, a missing
parameter, an unknown command, `update_current_version`, `default_commit_template` — with the base
directory redirected to a temporary `HOME`.

**Built, and run** (Phase 9): `tools/parity`, `task parity`. 38 scripted requests, **zero unexplained
differences**. What it found, none of which reading would have:

| Finding | Outcome |
|---|---|
| `get_status` on a directory that is gone answered `fork/exec /usr/bin/git: no such file or directory` — `exec` attributes a failed `chdir` to the binary, so a moved project folder reported that git was not installed, and printed the whole command line into the toast | **Defect, fixed.** `backend/git/runner.go`, `startFailure`; pinned by `TestAMissingRepositoryDirectoryBlamesTheDirectoryAndNotGit` |
| An added file's `old_path` is `null` here and the file's own path in 2.7.1 — systematic across all four diff commands | `DIVERGENCE-GIT-e`. No consumer sees it; every renderer use is `new_path ?? old_path` |
| A path *inside* a repository resolves to that repository here; 2.7.1 refused it, although its own `is_git_repo` already walked up | `DIVERGENCE-GIT-f` + `AMBIGUOUS-GIT-c` — an open product decision, because neither behaviour is good |
| The unix socket path is capped at 104 bytes, and macOS's `$TMPDIR` spends 49 of them before the tool adds anything | A constraint of the transport being removed. The tool picks a short root and checks the budget rather than letting .NET raise about a parameter named `path` |
| A request with **no `params` member at all** makes 2.7.1 raise `Operation is not valid due to the current state of the object.` where 3.0 answers `missing required parameter 'repoPath'` | Not a difference: `lib/bridge/host.ts` sends `params ?? {}`, so the renderer cannot produce that shape. The oracle was corrected, not the code |

The two normalisation rules worth knowing before reading a report: **minted ids are matched by shape,
not by field name** — every one this application mints is a uuid, so a *commit's* id stays comparable
to the character — and `date`/`timestamp` are likewise left alone, because a commit's date is data
the two sides read from the same fixture repository. Only clocks read at insert time and measured
durations are erased.

### 9.8 Parity checklist per phase

A phase is done when: its inventory classes are ported (subtest names match), its vectors pass, the
contract tests cover its commands, `go test -race` is green, the renderer checks are green, and the
manual acceptance checklist of its spec documents (`10-security.md` roundtrip checklist,
`11-files-search-terminal.md` watcher/terminal checklist, …) passed **on macOS and Windows**.

C# baseline to match (tests per folder): Activity 17 · Ai 153 · ApiClient 122 · Dbml 58 ·
Diagnostics 7 · Files 75 · Git 165 · Ipc 17 (→ bridge contract) · Platform 10 · Providers 221 ·
Review 105 · Security 21 · Storage 12 · Terminal 17 · TestVectors 5 · Tickets 113 · Update 32 ·
Workspaces 82.

---

## 10. Build, packaging, CI/CD and release

### 10.1 Local developer loop

```sh
mise use -g go@1.27.1
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.23
wails3 doctor                               # must be clean before anything else
pnpm -C frontend install --frozen-lockfile
wails3 dev -config ./build/config.yml       # Go rebuild + Vite on 1420 + bindings regeneration
```

Lint configuration (`.golangci.yml`, golangci-lint v2): `errcheck`, `govet`, `staticcheck`,
`revive`, `gosec`, `bodyclose`, `noctx`, `contextcheck`, `errorlint`, `unparam`, `misspell`. Plus a
release-gate grep that fails on a bare goroutine outside `shared/safego`:
`grep -rnE '^\s*go (func|[a-zA-Z_.]+\()' backend --include='*.go' | grep -v '_test.go' | grep -v 'shared/safego'`.

### 10.2 Build configuration

- Generate once: `wails3 generate build-assets -dir build` (check `--help` for the name, identifier and
  version flags of the pinned CLI), then set product name `CodeFlow`, identifier `com.codeflow.app`,
  company/copyright `Gastón Lara P.`, description, version `3.0.0`.
- Icons: `build/appicon.png` ← `docs/verbatim/assets/icon.png`; `build/darwin/icons.icns` ←
  `icon.icns`; `build/windows/icon.ico` ← `icon.ico`; tray `backend/desktop/assets/tray.png` ←
  `tray.png` (embedded). Keep `scripts/build-icons.sh` + `pack-ico.py` only if the SVG masters change
  (their algorithm is in §2.8).
- Version is written in two places — `build/config.yml` and `frontend/package.json` — and injected with
  `-ldflags "-X main.version=<v>"` by the Taskfile. The CI gate requires all three to agree.
- `go.mod`: `module github.com/gastonlarap-a11y/<repo>` (decided in §14 D2), `go 1.27.0`,
  `toolchain go1.27.1`. Builds use `-mod=readonly` and `-trimpath`.
- `.gitignore`: `bin/`, `build/bin/`, `frontend/dist/` (keep a placeholder `index.html` so `go:embed`
  compiles), `frontend/node_modules/`, `frontend/bindings/`, `dist-installers/`, `.env*`, secrets
  patterns carried over from the source repo (`*.pem`, `*.p12`, `*.pfx`, `*.mobileprovision`, `secrets*`).

### 10.3 Packaging

**macOS (arm64 only)**

1. `wails3 task darwin:package` builds `bin/CodeFlow.app` (`GOARCH=arm64`, `MACOSX_DEPLOYMENT_TARGET`
   per §14 D3 with matching `CGO_CFLAGS/CGO_LDFLAGS`); the generated task already runs
   `codesign --force --deep --sign -` (ad-hoc; §14 D4 for a stable identity).
2. Disk image: `wails3 task darwin:package:dmg` (it runs `wails3 tool package --format dmg` with the
   background and icons in `build/darwin/`), or by hand: a staging folder with `CodeFlow.app` and an
   `Applications -> /Applications` symlink, then
   `hdiutil create -volname CodeFlow -srcfolder <staging> -ov -format UDZO CodeFlow-3.0.0-arm64.dmg`.
   Either way the published name is `CodeFlow-3.0.0-arm64.dmg`.
3. `shasum -a 256 CodeFlow-3.0.0-arm64.dmg > CodeFlow-3.0.0-arm64.dmg.sha256` (run inside the directory).

**Windows (x64)** — this recipe ran end to end on a Windows runner with 2.7.1 installed and running (W-3).

1. Generate once: `wails3 generate build-assets -dir build -name CodeFlow -binaryname CodeFlow
   -productname CodeFlow -productidentifier com.codeflow.app -productversion 3.0.0 -productcompany
   "Gastón Lara P."` (these flags were accepted by the beta.23 CLI). Edit `build/windows/nsis/project.nsi`
   — not the updatable `wails_tools.nsh` — to add, after `!include "wails_tools.nsh"`:
   `!include "codeflow_replace_electron.nsh"`, and in `Function .onInit` after
   `!insertmacro wails.checkArchitecture`: `Call cf.closeRunningCodeFlow` and
   `Call cf.uninstallElectronCodeFlow`.
2. `build/windows/nsis/codeflow_replace_electron.nsh` (as tested):

   ```nsis
   ; CodeFlow 3.0: remove a CodeFlow 2.x install (electron-builder, per-user) before installing over it.
   ; GUID = UUIDv5("com.codeflow.app", 50e065bc-3134-11e6-9bab-38c9862bdaf3), as electron-builder 26.16.1 derives it.
   !include "LogicLib.nsh"

   !define CF_ELECTRON_GUID "e452a328-6f16-5dfd-9ae4-7f7f7761c215"
   !define CF_ELECTRON_UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${CF_ELECTRON_GUID}"
   !define CF_ELECTRON_INSTALL_KEY "Software\${CF_ELECTRON_GUID}"

   !macro CF_PROCESS_RUNNING NAME OUTVAR
     nsExec::ExecToStack 'cmd /C tasklist /NH /FI "IMAGENAME eq ${NAME}" | find /I "${NAME}"'
     Pop ${OUTVAR}
     Pop $9
   !macroend

   Function cf.closeRunningCodeFlow
     !insertmacro CF_PROCESS_RUNNING "CodeFlow.exe" $0
     !insertmacro CF_PROCESS_RUNNING "codeflow-core.exe" $1
     ${If} $0 == 0
     ${OrIf} $1 == 0
       MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "CodeFlow is running and will be closed to continue." /SD IDOK IDOK cf_kill
       Abort
       cf_kill:
       nsExec::ExecToStack 'taskkill /F /T /IM CodeFlow.exe'
       Pop $2
       Pop $9
       nsExec::ExecToStack 'taskkill /F /T /IM codeflow-core.exe'
       Pop $2
       Pop $9
       Sleep 1500
     ${EndIf}
   FunctionEnd

   Function cf.uninstallElectronCodeFlow
     ReadRegStr $R0 HKCU "${CF_ELECTRON_UNINSTALL_KEY}" "UninstallString"
     ${If} $R0 == ""
       Return
     ${EndIf}
     ReadRegStr $R1 HKCU "${CF_ELECTRON_INSTALL_KEY}" "InstallLocation"
     ${If} $R1 == ""
       StrCpy $R1 "$LOCALAPPDATA\Programs\CodeFlow"
     ${EndIf}
     ${IfNot} ${FileExists} "$R1\Uninstall CodeFlow.exe"
       Return
     ${EndIf}
     InitPluginsDir
     CopyFiles /SILENT "$R1\Uninstall CodeFlow.exe" "$PLUGINSDIR\old-uninstaller.exe"
     StrCpy $R4 0
     cf_retry:
       IntOp $R4 $R4 + 1
       ExecWait '"$PLUGINSDIR\old-uninstaller.exe" /S /KEEP_APP_DATA /currentuser --updated _?=$R1' $R3
       ${If} $R3 != 0
       ${AndIf} $R4 < 5
         Sleep 1000
         Goto cf_retry
       ${EndIf}
     ${If} $R3 != 0
       MessageBox MB_OK|MB_ICONSTOP "CodeFlow 2.x could not be removed (exit $R3). Close it and run this installer again." /SD IDOK
       Abort
     ${EndIf}
     Delete "$R1\Uninstall CodeFlow.exe"
   FunctionEnd
   ```

   Why each part: the uninstall key is the GUID (never match `DisplayName`, which is `CodeFlow <version>`);
   the uninstaller is copied out of the directory it deletes and run with `_?=` (electron-builder's own
   `uninstallOldVersion` does the same); `/KEEP_APP_DATA --updated` keep electron-builder's app-data
   logic out of the way (user data lives in `C:\CodeFlow` anyway). Closing the processes first is belt
   and braces: W-2 showed the old uninstaller closes a running 2.7.1 by itself in silent mode, but the
   same function also closes a running **3.x** before a 3.x → 3.y update, because the updater starts the
   installer without quitting (§2.9). Wails' own template has no running-app check.
3. Package with the user scope: `wails3 task windows:package INSTALL_SCOPE=user`. Per the generated
   `build/windows/Taskfile.yml` it runs `wails3 generate webview2bootstrapper` and
   `makensis -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user -DARG_WAILS_AMD64_BINARY=<exe>
   project.nsi`; the probe ran those two commands directly (W-3), not the task wrapper, and used an ASCII
   company name — confirm the task and the accented company name (`Gastón Lara P.`, which becomes part of
   the uninstall key name) in Phase 1. The template's output is
   `bin\CodeFlow-amd64-installer.exe`; rename it to **`CodeFlow-Setup-3.0.0-x64.exe`** (2.7.x's updater
   selects on `-Setup-`).
4. Measured result (W-3): per-user install in `%LOCALAPPDATA%\Programs\CodeFlow` with only `CodeFlow.exe`
   and `uninstall.exe`; the 2.7.1 files, keys and processes gone; Start Menu and Desktop shortcuts point to
   the new exe; new uninstall key `HKCU\…\Uninstall\<company><product>`; `C:\CodeFlow` intact after
   installing **and** after uninstalling 3.0 (parity: there is no wipe prompt today). The WebView2 profile
   `%APPDATA%\CodeFlow.exe` is removed by the Wails uninstaller.
5. Portable: copy the built `CodeFlow.exe` to `CodeFlow-Portable-3.0.0-x64.exe` (self-contained; needs the
   WebView2 runtime).
6. `sha256sum CodeFlow-Setup-3.0.0-x64.exe > CodeFlow-Setup-3.0.0-x64.exe.sha256` and the same for the
   portable build, from inside the directory.

### 10.4 Supply chain

- Exact versions in `go.mod`; `go.sum` committed; `go mod verify` in the release gate; `govulncheck ./...`.
- `frontend/pnpm-lock.yaml` committed; `pnpm audit --audit-level moderate`; the `pnpm-workspace.yaml`
  overrides from the source renderer are carried over by the copy.
- `.github/dependabot.yml`: `gomod` at `/`, `npm` at `/frontend`, `github-actions` at `/`, weekly, 5 PRs each.
- Every GitHub Action pinned by commit SHA — resolve SHAs at implementation time with
  `gh api repos/<owner>/<action>/git/ref/tags/<tag>`, never from memory.

### 10.5 `.github/workflows/ci.yml` (same shape as today)

Triggers `push: main` + `workflow_dispatch`; `concurrency` per workflow/ref without cancelling;
`permissions: contents: write`; every job has `timeout-minutes`.

| Job | Runner | Timeout | Steps |
|---|---|---|---|
| `gate` | ubuntu-latest | 5 | read version from `build/config.yml` and `frontend/package.json`, require `^\d+\.\d+\.\d+$` and equality; `publish=true` when `gh release view v$VERSION` fails |
| `draft` | ubuntu-latest | 5 | if publish: `gh release create v$VERSION --draft --target $SHA --title v$VERSION --generate-notes` |
| `installers` | matrix `macos-latest`/`mac`, `windows-latest`/`win`; `fail-fast: false` | 45 | checkout; `actions/setup-go` (`go-version-file: go.mod`, cache on); `pnpm/action-setup` + `actions/setup-node` 24 (pnpm cache); `go install …/wails3@v3.0.0-beta.23`; `pnpm -C frontend install --frozen-lockfile`; `wails3 task <os>:package`; run the built binary with `--smoke-test`; produce the artefacts of §10.3 with the exact names of §5.5; `gh release upload v$VERSION <artefacts> --clobber` |
| `publish` | ubuntu-latest | 10 | require assets matching `\.dmg$`, `\.dmg\.sha256$`, `-Setup-.*\.exe$`, `-Setup-.*\.exe\.sha256$`; `gh release edit v$VERSION --draft=false --latest` |

Whether CI should also run the test suites is an open decision (§14 D1): today no CI runs a test, on
purpose, and `release.sh` is the gate.

### 10.6 `scripts/release.sh` (ported)

Same flags (`major|minor|patch|X.Y.Z`, `--fast`, `--dry-run`) and the same seven steps as §2.8, with
the local suite replaced by:

```sh
go mod verify
go vet ./...
golangci-lint run
go test -race -count=1 ./...
govulncheck ./...
pnpm -C frontend install --frozen-lockfile
pnpm -C frontend typecheck && pnpm -C frontend lint && pnpm -C frontend test
pnpm -C frontend audit --audit-level moderate
```

The bump edits `build/config.yml` and `frontend/package.json` (a tiny `go run ./tools/bumpversion <v>`
avoids a `yq` dependency) and commits `chore(release): vX` with **only the operator's identity** — no
co-author trailers or tool attribution. `publish-release.sh` keeps its checks (tag format, version match,
clean tree) but no longer builds the `.dmg` locally: CI builds both installers (the source repo already
works this way through `ci.yml`).

---

## 11. Phased plan, step by step

### 11.0 Conventions for every phase

- **Operator rules** (from the operator's global instructions): commits, pushes and pull requests happen
  **only on the operator's explicit order**; Conventional Commits (`feat|fix|refactor|test|chore(scope):`);
  **no co-author trailers or AI attribution** anywhere; before the first edit of a task touching 3+
  files, state the scope (files, out of scope, confidence) and wait; close every report with
  `Confianza: NN% — <what stayed unverified>`.
- Work in the order written. A phase starts only when the previous phase's exit criteria hold.
- Before declaring a phase done: `go vet ./...`, `golangci-lint run`, `go test -race -count=1 ./...`,
  `pnpm -C frontend typecheck && pnpm -C frontend lint && pnpm -C frontend test`, the phase's manual
  checklist **on macOS and on Windows**, and the spec updates listed for the phase (§13). Report real
  results, including failures.
- The command-coverage contract test (§9.4 #1) carries an explicit "not yet ported" list; each phase
  removes its commands from that list. The list reaching zero (except the 11 deferred) is the port's
  progress meter.
- Every AMBIGUOUS/BUG/DIVERGENCE marker touched by a phase is re-read in its owning document before the
  code is written.

### Phase 0 — Toolchain and spike (go/no-go gate)

**Goal**: prove the five risky points of §1.5 and fix the version pins, before any production code.

| Step | Do | Evidence to record in `docs/phase0-findings.md` |
|---|---|---|
| 0.1 | Re-verify every version in §4: `curl -s https://proxy.golang.org/<module>/@latest`, `gh release list -R wailsapp/wails -L 5`, `npm view @wailsio/runtime dist-tags`, `https://go.dev/doc/devel/release`. Update §4 if anything moved; decide exact pins. | table of pins with dates |
| 0.2 | Install on the Mac: `mise use -g go@<pin>`; `go install github.com/wailsapp/wails/v3/cmd/wails3@<pin>`; `wails3 version` must equal the module pin; `wails3 doctor` clean. On a Windows 11 machine or VM: Go, Node 24, pnpm 11.20.0, Git for Windows, the same `wails3`, `wails3 doctor` clean (it reports NSIS/WebView2 needs). | doctor output, both OSes |
| 0.3 | **Copy the renderer** into its final place (the one time the source repository is read): `rsync -a --exclude node_modules --exclude dist ~/Documents/Git/code-flow/renderer/ ~/Documents/Git/code-flow-go/frontend/`; `pnpm -C frontend install --frozen-lockfile`; `pnpm -C frontend typecheck && pnpm -C frontend test` green before any change. | green output |
| 0.4 | Create a throwaway spike app in `code-flow-go/spike/` (never committed; delete at 0.11): `wails3 init` with the React TypeScript template (list templates with `wails3 init -l`), point its Vite root to `../frontend`, and write `main.go` from §3.9/§6 with a **stub** `bridge.Service.Invoke` answering a handful of commands with canned JSON (`list_workspaces` → `[]`, `get_setting` → `null`, `update_current_version` → `"0.0.0-spike"`, everything else → `unknown command '<name>'`). Compile-check every Wails API name used in §3.9 and §6. | list of API names that differed |
| 0.5 | Minimal `host.ts` from §7.3 in `frontend/` on a spike branch of the working copy. | app window shows the real UI |
| 0.6 | **Webview checks on both OSes** — run the app and record each: W1 tooltips/row menus (and the oldest macOS/Safari available), W3 header drag, W5/W6 copy buttons and CodeSnap image copy, W8 pinch on the DBML canvas, W9 external links, W10 `window.isSecureContext` and `crypto.randomUUID()`, W11 Monaco TS diagnostics (workers), CSP meta with `connect-src 'self'` still lets calls through, the default context menu under `--default-contextmenu: auto` in a production build, file drop onto a `data-file-drop-target` backdrop, **error passthrough** (Go returns `errors.New("CHECKOUT_CONFLICT: x")` → JS `e.message === "CHECKOUT_CONFLICT: x"`), **event shape** (`ev.data`), **call concurrency** (two 2-second calls finish in ~2 s total), **payload size** (a 20 MB string result round-trips; time it), tray + close→hide + ⌘Q + Dock reopen + second instance + fullscreen close on macOS, frameless caption buttons on Windows, Ctrl+P/F/0 reach the page on Windows. | pass/fail per item + screenshots of W1, titlebar, traffic lights vs `MacControlsSpacer` |
| 0.7 | **Keychain continuity (macOS)**: on a Mac with 2.7.1 and a saved GitHub token, a spike command reads `com.codeflow.app` / `github-token:github.com` with `keybase/go-keychain` from the packaged spike `.app` (ad-hoc signed) and from a bare binary. Record the exact behaviour: prompt shown? which button? OSStatus? Then write/read/delete a throwaway key. **Windows**: with 2.7.1 and a saved token, read target `com.codeflow.app.github-token:github.com` via `wincred`. | statuses, prompt screenshots |
| 0.8 | **Installer replacement (Windows)**: on a VM with 2.7.1 installed from `CodeFlow-Setup-2.7.1-x64.exe`: `reg query HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall /s /f CodeFlow` → record key name, `DisplayName`, `UninstallString`, `InstallLocation`; build the spike NSIS installer with `WAILS_INSTALL_SCOPE "user"` and the uninstall-old logic of §10.3; install; check old files gone, shortcuts point at the new exe, `C:\CodeFlow` untouched, the new app starts. | registry values, before/after listing |
| 0.9 | **PTY and watcher**: xpty with Git Bash (`--login -i`) on Windows and `$SHELL` on macOS — echo marker, resize 100×24, 4 096-byte reads. `syncthing/notify` recursive watch on a large repository (≥ 100 000 files) on both OSes: event latency, CPU at idle, open descriptors (`lsof -p <pid> \| wc -l` / Resource Monitor). Measure the spike's binary size and idle memory vs the installed 2.7.1. | numbers |
| 0.10 | **git floor**: `git version` on both machines; confirm the porcelain v2, `%(ahead-behind:…)` and `stash store` usages of §8.6 work; choose the minimum version. | chosen floor |
| 0.11 | **Decide**: every §1.5 point passes or has a written workaround → proceed; otherwise stop and report to the operator. Delete `spike/`; revert the spike changes in `frontend/` (keep the pristine copy from 0.3). | go/no-go |

**Exit**: `docs/phase0-findings.md` complete; §4 and any API names in §3/§6 corrected in this file.

**Status on 2026-09-17** (probes of `docs/phase0-findings.md`, run before this plan was handed over):

| Step | Status |
|---|---|
| 0.1 | Done on 2026-09-16; **repeat** at the real start (Wails betas move almost daily) |
| 0.2 | Open — install the pinned toolchain on the operator's machines (the probes used Go 1.26.4 locally and 1.26.8 on the runner) |
| 0.3 | Open — copy the renderer |
| 0.4 | **API part done**: the §3.9/§6 surface compiled and ran for darwin/arm64 and windows/amd64 (M-1, W-6). Still to do: the spike wired to the real renderer |
| 0.5 | Open — `host.ts` of §7.3 against the real renderer (the `Call.ByName` mechanism itself is measured) |
| 0.6 | **Mechanisms done on both OSes** (secure context, CSS support, workers, CSP, errors, events, concurrency, payloads, clipboard). Still manual with the real UI: W1 on the oldest Safari allowed by D3, W3 drag, W6 CodeSnap image copy, W8 pinch, W9 links, tray/menu/close/⌘Q/Dock/second instance/fullscreen, caption buttons, context menu in a production build, Monaco |
| 0.7 | **Shape done** (M-5; W-7 on Windows). Still to record: the first *data* read of a 2.7.1 item by the packaged Go app (password prompt) |
| 0.8 | **Done** (W-1…W-3): exact keys and values, silent replacement of a running 2.7.1 |
| 0.9 | **Done** (M-6/M-7, W-4/W-5) with 50 000 files; binary size and idle memory of the full app remain for Phase 1 |
| 0.10 | **Commands done** on git 2.54 (macOS) — rerun the M-2 script on the oldest Git for Windows / Xcode git you intend to support and fix the floor |
| 0.11 | The five go/no-go points pass (§1.5) |

### Phase 1 — Repository skeleton, bridge and desktop shell

**Goal**: the real app opens on both OSes with the real UI, every host capability works, and every
command answers `unknown command` except `reset_app_data`.

1. **Repository**: `git init -b main` in `code-flow-go`; `.gitignore` per §10.2. (Commit only when the
   operator asks.)
2. **Agent configuration**: run the `setup-project` skill to create `README.md`, `AGENTS.md`, `CLAUDE.md`,
   `.claude/settings.json` (Go + React/TypeScript plugins only) and path-scoped rules
   (`.claude/rules/go.md` for `backend/**`, `frontend.md` for `frontend/**`). Carry these rules over from
   the source AGENTS.md, rewritten for Go: one package per feature; `main.go` composition only;
   `bridge` knows no feature; errors translated at the edge with sentinel prefixes; renderer reaches the
   backend only through `frontend/src/lib/bridge/host.ts`; interfaces need two implementations or a real
   test seam; **known bugs are preserved**; **XLANG literals are byte contracts**; **no credential reaches
   an AI agent process**; **the credential store fails loudly**; exact version pins; English everywhere
   except the `VERBATIM` Spanish literals; changed behaviour → owning spec document; one workflow with
   `gate → draft → installers → publish`; a version bump is the act of releasing; the release is a draft
   until all installers and digests are present; every job has `timeout-minutes`. Port the source skills
   as Go versions: `new-command` (handler + registration + renderer wrapper + contract list + spec row +
   tests), `db-migration` (a named, self-guarding step in `storage.Migrations`), `verify` (§7.4 W14),
   `release`.
3. **Go module and build**: `go mod init github.com/gastonlarap-a11y/<repo per §14 D2>`; set `go 1.27.0` and
   `toolchain go1.27.1`; `go get github.com/wailsapp/wails/v3@<pin>`; `wails3 generate build-assets -dir build`;
   set identity and version (§10.2); copy icons from `docs/verbatim/assets/`; root `Taskfile.yml` including
   `build/<os>/Taskfile.yml`, with `VITE_PORT=1420` and `-X main.version`; placeholder
   `frontend/dist/index.html` for `go:embed`.
4. **Cross-cutting packages** (§8.0) with tests: `shared/proc`, `shared/safego`, `shared/sentinel`,
   `shared/testvectors` (+ the 5 `FixtureCatalogTests`).
5. **`platform` and `diagnostics`** (§8.2) with tests, including the ported shell `login-path` and
   `shell-log` cases.
6. **`bridge`** (§3.3–3.6): `Registry`, `Service.Invoke`, `Arg`/`OptionalArg`, `jsonwire` (+
   `AssertNoNilSlices`, `ByteArray`), `Emitter`; tests for unknown command, missing parameter, void → `null`,
   panic recovery, sentinel passthrough; the contract test (§9.4 #1) with the full "not yet ported" list.
7. **`app`**: `RunStages` (reset-marker → directories → scratch-sweep → storage placeholder),
   `StartupState`, `--smoke-test` (SQLite probe can use a temp DB now; git and PTY probes as in §8.1).
8. **`desktop`** (§6): window, close→hide with the fullscreen wait, `RequestQuit` + signal handler, tray,
   macOS menu, single instance, Dock reopen, `HostService` (`SidecarStatus`, `OpenFile`, `OpenDirectory`,
   `SaveFile`, `OpenExternal`, `ClipboardWrite`, `OpenLogs`, `Quit`), file-drop event, default context
   menu kept (§6.1).
9. **`main.go`** per §3.9 with only the packages above; `platform.Register` provides `reset_app_data`.
10. **Renderer** (§7.2–7.4): new `host.ts`, `dialog.ts`, `shell.ts`, `webview.ts`; `host.test.ts`
    rewritten; `ImportModal` drop target; CSS drag region; production CSP plugin; external-link handler;
    W1 decision (§14 D3), W4–W9 fixes; the bridge is called with `Call.ByName` (§7.3), so the Taskfile's
    `generate:bindings` step can stay for other services but nothing in `lib/bridge` imports generated
    code; keep `frontend/bindings/` git-ignored.
11. **CI and scripts** (§10.4–10.6): `ci.yml`, `dependabot.yml`, `release.sh`, `publish-release.sh`,
    `tools/bumpversion`. Add a `workflow_dispatch` input that builds installers **without publishing**, so
    packaging is exercised from now on.
12. **Spec**: apply the §13 edits for `02-bootstrap-platform.md` (retired and reinterpreted BOOT rules) and
    the transport notes in `01-ipc-surface.md`.

**Manual checklist (both OSes)**: launch → real UI, no console flashes; close hides, tray *Show* restores,
tray *Quit* and ⌘Q quit with a reason in `shell.log`; second launch focuses the first; Dock click restores
(macOS); fullscreen then close lands on the desktop, not a black Space (macOS); caption buttons (Windows);
header drag; every dialog returns a path or `null`; copy buttons; *open logs*; external links open the
browser; `reset_app_data` wipes `{base}` on next launch and leaves the keychain alone; `--smoke-test`
exits 0 on the packaged binary.

**Exit**: all of the above, suites green, CI dispatch builds unsigned artefacts with the §5.5 names.

### Phase 2 — Storage, workspaces, settings, activity, credentials

**Goal**: the first vertical slice — the app manages workspaces and projects on a **real 2.7.1 database**
and uses the credentials 2.7.1 saved.

1. **`storage`** (§8.3): `Open`, schema, migrations in spec order, `Clock`, `SeededPromptHistory`; wire the
   `storage` start-up stage; `MigrationTests` + `migrations`/`queries` vectors + `sql/` seeds.
2. **Real-database proof** (§5.2): copy a 2.7.1 `codeflow.db` (with `-wal`/`-shm`) into a temp dir, open it
   twice with the Go layer, compare `sqlite3 <db> .schema` before/after; open it in the app (point `{base}`
   at a temp copy via a **test-only** build tag, never an environment variable in release builds).
3. **Prompts as files**: `git mv docs/verbatim/prompts/*.txt backend/ai/prompts/`; `//go:embed`; digest test
   (§8.9). The storage backfill and `default_workspace_prompt` need them now.
4. **`security`** credential store (§8.4) + `set_/has_/delete_*` commands (9) + `CredentialStoreTests`
   (env-gated) + `SecretCommandsTests`. (The scanner waits for Phase 3.)
5. **`workspaces`** (§8.5): stores and the 27 `WorkspaceCommands` (workspaces, projects,
   `default_clone_dir`, settings, workspace prompts, agents, review contexts, MCPs — `pickFolder`,
   `quitApp`, `openExternalUrl` and `saveTextFile` are renderer wrappers over host capabilities, not
   commands); plus, from
   `ReviewCommands`, the review-run history commands `list_review_runs`, `get_review_run`,
   `mark_review_finding`, `delete_review_run`, `delete_review_runs_for_pr`,
   `purge_workspace_review_runs`, `export_review_runs` — implement `backend/review/store.go` now with
   `ReviewRunStoreTests`.
6. **Skills** (10 commands) with the npx installer streaming `skills:progress`.
7. **`activity`** (7 commands).
8. Remove the ported names from the contract list; wire-shape goldens for every new response type.

**Manual checklist**: with a copy of a real `~/CodeFlow` (or `C:\CodeFlow`) — existing workspaces,
projects, colours, prompts, agents, MCPs, skills and review history appear unchanged; create/rename/delete
round-trips; settings persist across restarts; Settings shows saved GitHub/ADO/AI credentials as present
after at most the one-time macOS keychain prompt; saving and deleting a token works; `reset_app_data`
still leaves credentials.

**Exit**: inventory classes of Storage, Security (store + commands), Workspaces, Activity,
`ReviewRunStoreTests` ported and green; §13 edits for `03-storage.md`, `09-workspace-scoped.md`,
`10-security.md`.

### Phase 3 — Git, files, watcher, secret scanner, terminal

**Goal**: the Git and editor experience at parity.

1. `git` runner, version floor check, `testrepo` helper (§8.6, isolated `HOME`/`GIT_CONFIG_GLOBAL`).
2. Status, diffs (working/staged/commit/commit files/commit file), unified diff parser, change context,
   prompt diff — with `DiffTests`, `UnifiedDiffTests`, `RepoStatusTests`, `ChangeContextTests`,
   `PromptDiffTests`, `git_diff` vectors.
3. Branches, checkout variants with `CHECKOUT_CONFLICT: `, commit graph, unpushed commits — `BranchesTests`,
   `BranchContributionTests`, `CommitGraphTests`, `git_branch` vectors.
4. Stage/unstage/discard/commit/reset/remotes/identity — `GitCommandsTests`, `RemotesTests`, `IdentityTests`.
5. Stash (outcomes, rename reorder) — `StashTests`, `git_stash` vectors.
6. Merge and conflicts (outcomes, sides, complete, abort, `ConflictVersions`) — `MergeTests`.
7. Checkpoints (temp index, refs, prune to 20, restore) — `CheckpointsTests`, `git_checkpoint` vectors.
8. Network streaming (`git:progress`, `git:done`) — `GitNetworkTests`.
9. `files`: operations and path guards, search/replace with checkpoint, `GlobSet`, repo walk, watcher —
   all `Files/` classes, `fsops`/`search` vectors.
10. `scan_staged_secrets` — `SecretScanTests` + `secret_scan` vectors.
11. `terminal` — `ShellResolverTests`, `TerminalCommandsTests`, `TerminalSessionTests`.
12. Parity oracle (§9.7) over fixture repositories for status, branches, diffs, stash list, search.

**Manual checklist (both OSes)**: stage/unstage/discard/commit; undo last commit (`reset` mixed); branch
switch with local changes → carry / stash / cancel dialog (XLANG-002) including the "nothing was brought"
dialog; stash rename reorders to the top; merge with conflicts → resolve ours/theirs → complete; abort;
fetch/pull/push progress; AI checkpoint list/restore/delete (after Phase 4 creates them, re-check);
search with include/exclude, replace and undo; external edits refresh the Changes panel within ~0.5 s;
terminal in `$SHELL` (macOS) and Git Bash (Windows) with resize; secret found in a staged file blocks the
commit gate.

**Exit**: 165 Git + 75 Files + 11 SecretScan + 17 Terminal tests ported and green; §13 edits for `04-git.md`,
`11-files-search-terminal.md`.

### Phase 4 — AI engines and run lifecycle

**Goal**: every engine runs, streams, cancels and fails exactly as today.

1. Routing (XLANG-004/005) — `AiRoutingTests`.
2. Binary discovery, version and model discovery (§15.2.5) — `BinaryDiscoveryTests`.
3. Run registry: streaming, line rules, silence timeout, cancel/tree kill — `AgentStreamingTests`,
   `StdinDeliveryTests`.
4. Runner: retry, scratch files, sweep — `NetworkRetryTests`, `EngineScratchTests`.
5. Signals: quota (with `BUG-AI-b` preserved), auth.
6. Engines claude, codex, gemini/agy, opencode, openai, ollama — `ClaudeCodeTests`, `EngineVectorTests` +
   `ai`/`claude`/`codex`/`gemini`/`opencode`/`openai` vectors.
7. Operations and commands (13): `generate_commit_message`, `cancel_ai_run`, `list_ai_models`,
   `check_ai_provider`, `resolve_conflict_with_ai`, the five `default_*_template`, `resolve_finding_with_ai`,
   `send_chat_message`, `inline_edit_with_ai` — `AiCommandsTests`, `AiOperationsTests`, `ChatTurnTests`,
   `ReviewOperationTests`, `AiTextFooterTests`, `PromptsTests`.
8. **Credential invariant test**: a fake engine binary (built from `testdata/`) dumps its argv, env and
   stdin; the test stores real-looking secrets in a test credential store and asserts none appear.
9. **Process-group regression test** (BOOT-037 inverted): a fake engine that spawns a child and then
   signals its own process group; cancelling the run kills both; the test process survives.

**Manual checklist (both OSes, with the CLIs the operator uses)**: chat with each installed engine
(session resume, MCP config), commit message, inline edit, conflict resolution, fix finding; cancel mid-run
(the app stays open); a 10-minute silence produces the timed-out banner (use a fake engine with a short
test deadline); quota and expired-auth banners; Settings probes and model lists; Windows: `.cmd` shims
(`opencode`, `agy`) with briefs containing `&`, `%`, `^`, quotes.

**Exit**: 153 Ai tests ported and green; §13 edit for `05-ai-engines.md`.

### Phase 5 — Providers, PR review pipeline, work items

**Goal**: the flagship feature at parity, re-verified live.

1. `providers` (GitHub, Azure, PR links, repo detection, known hosts, linked repo, unified patch, work-item
   links) and the 19 provider commands — all `Providers/` classes, `pr_link`/`ado` vectors.
2. `review`: run (PR fetch refspecs), memory (parse/reconcile/render/renumber), posting (stale check),
   PR-link flows, act on PR, create PR, PR description, with the review-level strings and directives of AI-022 — all `Review/` classes + the
   Go ↔ TypeScript parser fixtures (§9.4 #6).
3. `tickets` (17 commands incl. `review_changes` and `comment_ticket`) — all `Tickets/` classes; end-to-end
   classes gated by `CODEFLOW_E2E_ADO_*`.

**Live verification** (repeat the 2026-08-01 run in `90-ambiguities.md`, through the Go app, on a throwaway
private GitHub repository and a throwaway Azure DevOps repository): create PR → review `completo` → publish
(anchored threads + summary) → publish again (replies inside threads) → push a fix → re-review reconciles →
publish resolved (GitHub threads resolved via GraphQL) → approve own PR (`SELF_APPROVAL: `) → close/abandon
→ link-path review and post; expire a PAT → `CREDENTIAL_REFUSED: ` UI; push between review and post on
GitHub → `STALE_REVIEW: `. If a second push is possible on Azure, close `set_pr_thread_status`'s
`UNVERIFIED` marker.

**Exit**: 221 Providers + 105 Review + 113 Tickets tests ported and green; live matrix recorded in
`90-ambiguities.md`; §13 edits for `06`, `07`, `14`.

### Phase 6 — API client

**Goal**: HTTP/GraphQL, WebSocket, Socket.IO and MQTT at parity (gRPC stays deferred).

1. Stores (27 commands: collections, folders, requests, move/reorder, environments, history, cookies) —
   `ApiTreeStoreTests`, `ApiStoresTests`, `ApiStartupTests`.
2. HTTP send/tracked/cancel (§8.13) — `HttpSendTests`, `HttpDecodingTests`, `SigV4Tests`, `http` vectors.
3. Streams: WebSocket, Socket.IO, MQTT 3.1.1/5, TLS policy, disconnect — `StreamCommandsTests`,
   `StreamFramingTests`, `ws`/`socketio`/`mqtt` vectors.
4. Files: `api_read_file_base64`, `api_pick_file`, `api_save_file`, `api_read_text_file` — `ApiCommandsTests`.

**Manual checklist (both OSes)**: requests against a local HTTP test server (redirects, gzip/br/deflate,
digest, SigV4, client certificate, proxy, verify-off on a self-signed server, 50 MiB truncation, cancel);
GraphQL; WebSocket echo; a Socket.IO **v4** and a **v3** server (namespaces, auth payload, emits); Mosquitto
with MQTT 3.1.1 and 5, TLS with verification off, last will; import/export of collections; environments and
Postman-style scripts (CSP `unsafe-eval`).

**Exit**: 122 ApiClient tests ported and green; §13 edit for `08-api-client.md`.

### Phase 7 — Schema designer (DBML)

**Goal**: documents, layouts, AI assist and database introspection at parity.

1. Documents, layouts, connections (keychain `db-password:{id}`), assistant (`dbml_assist` with the three
   DBML prompts), snapshot builder.
2. Introspectors: PostgreSQL, SQL Server (+ integrated auth), MySQL, SQLite (read-only, file released).
3. Tests: all `Dbml/` classes; `ServerIntrospectorTests` against containers (`postgres`, `mysql`,
   `mcr.microsoft.com/mssql/server`) gated by env vars.

**Manual checklist**: introspect each engine; wrong password / closed port → `DB_CONNECTION_REFUSED: ` with
the driver's sentence and no connection string anywhere (UI, `errors.log`); SQL Server integrated auth on
Windows; after introspecting a SQLite file, delete or rename it immediately (Windows must allow it).

**Exit**: 58 Dbml tests ported and green; §13 edit for `15-dbml.md`.

### Phase 8 — Updater, installers and the drop-in upgrade

**Goal**: 3.0.0 installs over 2.7.1 on both OSes and keeps everything; 3.0.x updates itself the same way.

1. `update` package and its 3 commands (§8.15) — `ReleaseVersionTests`, `UpdateAssetTests`,
   `UpdateDigestTests`, `UpdateDownloadTests`. Add a test that runs the ported asset selection over the
   exact artefact names CI produces (§5.5): the 2.7.x selection logic is the same code, so this proves
   2.7.x will pick the right files.
2. ~~Finalise packaging (§10.3) and CI (§10.5); dispatch a build-only run; download its artefacts.~~
   **Packaging and CI are written and verified as far as macOS can verify them.** Dispatching a run
   needs a push, which needs an explicit instruction, so that half is still open.

   - `wails3 generate build-assets` run at the pinned beta.23 with the flags §10.3 names.
   - `build/windows/nsis/codeflow_replace_electron.nsh` written from the W-3 recipe, and
     `project.nsi` wired per §10.3 — `!include` after `wails_tools.nsh`, `Call cf.closeRunningCodeFlow`
     and `Call cf.uninstallElectronCodeFlow` after `wails.checkArchitecture`. **`makensis` compiles it
     clean** (macOS has NSIS), which proves the script and both functions resolve; whether they do the
     right thing is the Windows drill.
   - `task package:mac`, `package:mac:dmg` and `package:win` added. They wrap the hand-written `build`
     rather than the generated `darwin:build`, and that is the point of their existing: the generated
     build task carries no `-X main.version`, so an `.app` made from one reports `0.0.0` and offers
     itself as an update forever.
   - `.github/workflows/release.yml` written per §10.5: gate → draft → installers (both OSes) →
     publish, publishing only once all four artefacts and their digests are present.
   - **`bin/CodeFlow-3.0.0-arm64.dmg` builds on this machine**, from a 67 MB ad-hoc-signed bundle
     identified as `com.codeflow.app`, with its `.sha256` written the way §10.3 specifies.

   **Three defects the packaging step found, all fixed:**

   | Found | Why it mattered |
   |---|---|
   | `generate build-assets` **overwrote** `build/appicon.png` and `build/config.yml` with Wails defaults | The icon became the Wails logo and the config lost the identity block that keeps an upgrade finding the user's data. Restored; the generator is not idempotent and must not be re-run blind |
   | The generated `Info.plist` declared `LSMinimumSystemVersion` **12.0.0** | §14 D3 decided **26.0**, and the renderer uses anchor positioning, the Popover API and `light-dark()` with **no fallback built** on that promise. Shipping 12.0 would let it install where the UI cannot lay out at all |
   | `frontend/package.json` still said **2.7.1** | §10.5's gate requires it to equal `build/config.yml`'s `3.0.0`. The release would have refused itself — correctly, and at the worst moment |

   Left over, and needing a hand: the generator also wrote `build/android/`, `build/ios/`,
   `build/linux/`, `build/docker/` and `build/windows/msix/`, none of which this project targets
   (arm64 macOS and x64 Windows only, §14 D8). Their Go broke the lint gate, so `build/` is now
   excluded from golangci-lint — right in itself, since everything there is regenerated — but the
   trees should still go:
   `rm -rf build/android build/ios build/linux build/docker build/windows/msix build/appicon.icon build/icon.icns build/icon.ico`
3. **Upgrade drills on clean machines** (record each in `docs/phase8-drills.md`):
   - macOS: install 2.7.1 from its `.dmg`; create workspaces, projects, API collections, a review run; save
     GitHub/ADO/AI credentials and a DB password; quit; drag the 3.0.0 `.app` over it; launch; everything is
     there; keychain prompt behaviour matches §5.3; tokens work (list PRs); tray, menu, update check.
   - Windows: install 2.7.1 (`CodeFlow-Setup-2.7.1-x64.exe`), same data; run `CodeFlow-Setup-3.0.0-x64.exe`;
     the Electron install is removed, shortcuts start 3.0.0, `C:\CodeFlow` and credentials intact; uninstall
     3.0.0 leaves `C:\CodeFlow`; portable build runs.
   - Fresh install on both OSes (no `{base}`): first launch creates `{base}`, `logs`, `repos` only.
   - `reset_app_data` on 3.0.0.
   - 3.0.0 → 3.0.1 through the Go updater against a throwaway repository built with a **test-only** feed
     override (build tag, absent from release builds).

**Exit**: all drills pass; nothing published yet.

### Phase 9 — Parity audit, documentation and cutover

**Steps 1, 2, 5 and 6 are done; step 4 is done as far as one machine can take it.** Everything that
remains — the manual checklists, cold start and idle memory, the upgrade drills, and the cutover
itself — needs a person at each of the two operating systems, and the cutover additionally needs an
explicit instruction. Two tools came out of this phase and both stay: `task parity` (the
differential oracle) and `task inventory` (the test audit). Neither is in `task check`: one needs
2.7.x installed, the other reports volume rather than pass or fail.

1. ~~`docs/verbatim/test-inventory.md` fully ticked, or each unported test recorded with its reason in the
   owning spec document.~~ **Audited**: `tools/inventory`. The ticking itself is not possible and the audit
   is what established that — §9.2 asked the port to keep the C# method names as subtest names so parity
   would be grep-able, and **the port did not**: only 16 of the 121 classes kept even the file name, and
   inside them the sentences were rewritten (`A_file_committed_then_edited_again_appears_once_with_its_
   cumulative_change` became `TestBranchContributionCountsATwiceTouchedFileOnce` — same file, same
   behaviour, two words in common). Any per-name score is a measure of English phrasing. What the audit
   reports instead is **volume per area** and **where each class went**, and the one number that means
   something: **1 232 C# behaviours against 1 847 Go ones**, 1.5×, with no area below 0.7×.

   Of the 121 classes, 114 have entries that resemble Go tests. The seven that do not were each read
   against the tree, and only one was a real gap:

   | Class | Outcome |
   |---|---|
   | `Ai/PromptsTests` (4) | **A real gap, now closed.** Nothing asserted that the embedded prompts ask for what the parsers read — see below. `backend/tickets/promptcontract_test.go`, `backend/review/promptcontract_test.go` |
   | `Ai/NetworkRetryTests` (3) | Covered, relocated: retry moved from the AI engines to the shared client, `backend/platform/platform_test.go` (9 tests) |
   | `Ai/StdinDeliveryTests` (4) | Covered: `backend/ai/runner_test.go`, `engine_cli_test.go` |
   | `Diagnostics/StartupLogTests` (3) | Covered: `backend/diagnostics/diagnostics_test.go`, the three `TestStartupLog*` |
   | `Git/IdentityTests` (1) | Covered, spread: `commands_test.go`, `merge_test.go`, `staging_test.go`, `checkpoints_test.go` |
   | `Ipc/NamedPipeIpcListenerTests` (2) | **Not ported, by design.** It tests the Windows named pipe of the transport this port removes (§2.3). There is nothing to port it to |
   | `Dbml/ServerIntrospectorTests` (4) | **Known gap**, already recorded: needs real PostgreSQL/MySQL/SQL Server. Only SQLite is exercised end to end (§8.14) |
   | `Tickets/AzureCommentEndToEndTests` (1) | **Known gap**: gated on `CODEFLOW_E2E_ADO_*` in C# too. Phase 5 slice 5g |

   **What the gap was.** `XLANG-001` calls the finding format a three-way contract and `XLANG-016` does
   the same for the verdict block, but both pin only the *parsers* — the prompt that causes the format to
   exist is a text file nothing read in a test. Rewording `## VEREDICTO DE COBERTURA` in
   `DEFAULT_TICKET_REVIEW_STANDARD.txt` left the whole suite green while every live ticket review
   silently lost its verdict. Five tests now join the halves: every literal the parsers match is asserted
   present in the prompt that asks for it, the prompt's own worked example is run through `ParseVerdict`,
   and the 58-line finding-format block the two standards share is asserted byte-identical — which it has
   to be, because `ReviewMemory` reconciles ticket and pull-request reviews with one parser.
2. ~~Parity oracle (§9.7) over the full scripted request list; zero unexplained differences.~~ **Done**:
   `tools/parity` / `task parity`, 38 requests, 0 unexplained differences, 7 explained (each naming the
   marker that records it). One defect fixed and two divergences raised — see the table in §9.7.
   Widening the script is cheap and worth doing as the manual checklists find areas worth pinning.
3. Manual acceptance checklists of every spec document, both OSes; the WebKit list W1–W14 re-checked on the
   oldest supported macOS.
4. Performance record (README): cold start, idle memory, installer size, `get_status` on a 100 000-file repo,
   2.7.1 vs 3.0.0. **Partly done** — the half this machine can answer is in the README, measured with
   `task parity -- -time`, which times the same request against both cores.

   The shape of the result is not the one this line assumed. Every command that reads the database or
   answers from memory is **7–29× faster**, because the transport was most of what it cost. Every
   command that touches git is **slower** — `get_commit_diff` 748 µs → 14.8 ms, `get_working_diff`
   8.05 ms → 22.7 ms — because the port replaced libgit2 with the `git` command line and each read is
   now a process spawn of about 5–7 ms before git does any work. The fixture is five files, so the git
   rows are measuring that fixed cost almost alone; on a large repository the work should dominate.
   It is a measured consequence of a deliberate, documented decision (`backend/git` package comment),
   not a defect, and recording it is the point of this step.

   One scaling note came out of it: `get_working_diff` spawns one `git diff --no-index` **per untracked
   file**, so the cost is linear in something the user controls.

   **Still outstanding**: cold start, idle memory and `get_status` on a 100 000-file repository, all of
   which need the window open on each OS — they belong with step 3's manual pass, not here.
5. Spec sweep (§13): C# and Electron paths replaced by Go paths (§0.4) across `docs/`; re-run the XLANG sweep
   (grep `mirror`, `in sync`, `must match` in `backend/` and `frontend/`); marker ledgers updated.
   **Done for the three documents §13 lists at phase 9**, and the sweep found more than paths:

   - `01-ipc-surface.md` — every `###` heading now names the **Go file that registers** those commands,
     derived from the registry rather than transcribed: each of the 246 names was found as a literal
     registration in exactly one non-test file under `backend/`, with **zero ambiguity**, totalling the
     235 the registry reports. Four regroupings are named (the API client split three ways, the review
     commands five, the workspace block two, checkpoints merged into git). The sweep also found that
     **five headings disagreed with their own row counts**, six rows are host surfaces rather than
     commands (now marked `HOST`: they live on `desktop.HostService`, and `contract_test.go` would fail
     if they were registered), and **four names had no row at all** — `repo_web_url` and the three
     updater commands, which are called from `bridge/updater.ts` and were never tabulated. All four are
     now in the tables.
   - `00-conventions.md` — the counts are measured over `backend/` (171 files, 37 887 lines, 235
     registered, 246 called, 23 tables), each keeping its 2.x figure beside it. Its `236 / 232` had
     contradicted `01-ipc-surface.md`'s `235 / 246` since the port began.
   - `13-cross-language-contracts.md` — every `XLANG-*` implementation line carries its Go path. The one
     that does not is `XLANG-007`, deliberately: the static model lists are the renderer's alone.
   - **The renderer moved and nothing had said so**: `renderer/src/…` became `frontend/src/…` in the
     port, and 16 references across the two documents still used the old root. Now corrected, and
     **every code path cited anywhere under `docs/business-rules/`, `MIGRATION-GO.md` and `AGENTS.md`
     resolves to a file that exists** — checked by resolving each one, which also caught
     `backend/bridge/contract_test.go` in §9.4 (it is `backend/app/contract_test.go`).

   Still stale, and left for the remaining phase-9 pass: the per-row `<sub>` provenance in
   `01-ipc-surface.md`'s tables (kept on purpose — the heading carries the Go file, the row carries
   where it came from) and the `Implementation` lines of the twelve *other* spec documents, most of
   which their own phase already updated.
6. ~~Delete `docs/verbatim/` leftovers that moved (prompts, assets); keep `test-inventory.md` until step 1 is
   complete, then delete it.~~ **Done, with two deliberate departures from what this line says.**

   The prompts had already moved to `backend/ai/prompts/`, leaving an empty directory. Of the six
   assets, only two had actually moved, and comparing digests is how that was established rather than
   assumed:

   | Asset | Fate |
   |---|---|
   | `icon.png` | Deleted — byte-identical to `build/appicon.png` |
   | `tray.png` | Deleted — byte-identical to `backend/desktop/assets/tray.png` |
   | `icon.icns`, `icon.ico` | **Moved to `build/`**, not deleted |
   | `icon.svg`, `tray.svg` | **Moved** to `build/` and `backend/desktop/assets/` |

   **The four were the only copies, and two of them are build inputs.** `icon.icns` and `icon.ico` are
   what macOS bundling and the NSIS installer need, and Phase 8 step 2 — the step that will reference
   them — has not been written yet; deleting them would have destroyed the only copy of an input before
   the thing that consumes it existed. The two SVGs are the vector masters the PNGs were generated from.
   "Leftovers that moved" is the right rule; these had not moved, so they were filed rather than dropped.
   (`backend/desktop/desktop.go` embeds `assets/tray.png` by name, not `assets/*`, so the SVG beside it
   does not enter the binary.)

   **`test-inventory.md` stays.** The instruction assumed step 1 would end with every entry ticked, after
   which the file is spent. Step 1 instead established that ticking is not possible — the port rewrote
   the names — and turned the inventory into the input of a re-runnable audit (`tools/inventory`, which
   reads it by default). Deleting it now would turn a fact anybody can re-check into a claim in a
   document, and would break `task inventory`. It is 76 KB.
7. **Cutover** per §14 D2 (recommended path): on the operator's order, open a pull request in
   `gastonlarap-a11y/code-flow` that replaces the tree with this repository's content (branch
   `feat/go-wails-port`), keeping the repository, its releases and its update feed; merge; bump to `3.0.0`
   (the CI gate publishes: draft → installers → publish).
8. Watch the first real 2.7.x → 3.0.0 upgrades on the operator's machines; keep 2.7.1 artefacts available
   for rollback (users can reinstall 2.7.1; its database is untouched by 3.0.0 migrations that are
   no-ops on an up-to-date schema — confirm in step 2 of Phase 2).

---

## 12. Risk register

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | **Wails v3 beta churn** breaks APIs between betas | High | Medium | Exact pin of module + CLI + `@wailsio/runtime`; upgrade only as a dedicated change with `wails3 doctor` and the Phase 1 manual checklist; features never import Wails (§3.2), so churn stays in `desktop`/`main.go` |
| R2 | **WKWebView rendering gaps** (anchor positioning, popover, gestures, clipboard, links) | Low on Safari ≥ 26.2 / Medium on older | High on macOS | Mechanisms measured working on Safari 27 (M-1); remaining W3–W9 fixes (§7.4); minimum Safari per §14 D3 with a start-up check |
| R3 | **Keychain ACL**: the Go binary cannot read 2.7.1 items silently | High (by design of ad-hoc signing) | Medium | Item shape measured (M-5); first data read shows the keychain password prompt → *Always Allow*, or `CREDENTIAL_REFUSED: ` "reconnect"; a stable signing identity (§14 D4) stops it recurring on every update |
| R4 | **Windows installer** leaves the Electron install behind or collides with its files | Low | High | **Measured** (W-3): the §10.3 `.onInit` replaced a running 2.7.1 cleanly; repeat in the Phase 8 drill with the real app |
| R5 | **git CLI parity** gaps (stash/merge outcomes, full-context diffs, rename detection, spawn cost on Windows) | Low–Medium | High | 55/55 CLI checks and the 9 git vectors pass (M-2); remaining: the 165 C# Git tests ported, the parity oracle on real repos, `get_status` timing on large repos on Windows |
| R6 | **Recursive watcher** misbehaves on big repos or network drives | Low | Medium | 50 000-file trees measured on both OSes (M-7, W-5); network drives untested; fallback: `fsnotify` per directory on Windows |
| R7 | **ConPTY via xpty** edge cases (resize, exit detection, Git Bash login shell) | Low | Medium | Measured (W-4): interactive Git Bash, resize, exit; reader ends only after `Close()` — encoded in §8.8; fallback `aymanbagabas/go-pty` |
| R8 | **MQTT with two libraries / hand-ported Socket.IO** diverge from the C# behaviour | Medium | Medium | Vectors (`mqtt`, `socketio`, `ws`); manual checks against v3/v4 Socket.IO servers and Mosquitto 3.1.1/5 |
| R9 | **Windows `.cmd` shims**: argv text (agy/opencode briefs) re-parsed by `cmd.exe` | Medium | Medium | Same exposure as 2.7.1 (not a regression); test briefs with metacharacters in Phase 4; prefer temp-file delivery where the engine supports it (opencode already uses `--file`) |
| R10 | **A goroutine panic kills the whole app** (unlike a .NET task exception) | Medium | High | `safego` everywhere + release-gate grep (§10.1); `Invoke` recovers per call |
| R11 | **JSON drift**: nil slices → `null`, wrong casing, `omitempty` hiding `null` | High | High (blank UI, silent) | §3.4 rules; wire-shape goldens and `AssertNoNilSlices` (§9.4 #2) |
| R12 | **Unsigned binaries**: Gatekeeper, SmartScreen, antivirus false positives on Go executables | Medium | Medium | Same unsigned status as today; document first-run steps in README; §14 D4 |
| R13 | **WebView2 runtime missing** (locked-down Windows 10) | Low | High | Bootstrapper embedded in NSIS; portable build documents the requirement |
| R14 | **2.7.x users never see 3.0.0** (updater needs a token; feed repository) | Medium | Medium | Publish on `gastonlarap-a11y/code-flow` (§14 D2); release notes explain manual download |
| R15 | **Large payloads** over the Wails bridge (multi-MB diffs, file reads, terminal floods) are slow | Low | Medium | Measured: 20 MiB responses in 137 ms (macOS) / 543 ms (Windows); 5 MiB requests 79 ms / 1 159 ms (chunked); requests capped at 64 MiB like today's frames. Batch `terminal:output` if a flood shows up; the renderer already virtualises long lists |
| R18 | **Updates start the installer while the app runs** (2.7.x → 3.0 and 3.x → 3.y on Windows; `.dmg` replace on macOS) | High (it is the normal flow) | Medium | §10.3 `cf.closeRunningCodeFlow` (measured with a running 2.7.1, W-3); on macOS Finder asks to replace the running app — document in release notes |
| R16 | **Main-thread requirements**: window/dialog calls from background goroutines | Medium | Medium | Wrap UI calls issued from goroutines in `application.InvokeSync`; covered by the Phase 1 checklist |
| R17 | **Accidental "fixes"** of preserved bugs or divergences during a rewrite | High | Medium | Re-read markers before each feature (§11.0); tests that pin `BUG-*` behaviour stay (e.g. `BUG-AI-b`, `BUG-PROV-a`, `BUG-API-a/b/c`) |

---

## 13. Keeping the specification true

The rule "changed behaviour → update the owning document in the same change" applies to the port itself.
Required edits, by phase:

| Document | Edit | Phase |
|---|---|---|
| `00-conventions.md` | "Source of truth for counts": implementation files and lines counted over `backend/` (`*.go`, excluding tests); registered commands from the Go registry | 9 |
| `01-ipc-surface.md` | Replace the transport description with Wails (`bridge.Service.Invoke`, events via `app.Event.Emit`); `ai:output` payload `run_id`; group commands by Go registration file; mark the 11 deferred names | 1, 9 |
| `02-bootstrap-platform.md` | Retire BOOT-018b and BOOT-034; rewrite BOOT-035 (goroutine panics / `safego`), BOOT-037 (children in their own group), BOOT-029/030/031/032/033 per §3.8; window lifecycle, tray, menu, CSP and paths per §2.6/§6 (removing the Tauri 1.7.2 leftovers of §2.11); remove `installer/hooks.nsh` and `release.yml` references; add a `DIVERGENCE-BOOT-*` (next free letter) for `update:progress` now reaching the renderer | 1, 8 |
| `03-storage.md` | Driver and connection model in Go; table count reconciled with `15-dbml.md` (23) | 2 |
| `04-git.md` | Implementation is the `git` CLI; every rule's **Implementation** path; the text after `CHECKOUT_CONFLICT: ` and other error messages are now git's wording (new `DIVERGENCE-GIT-*`); `--no-verify` keeps "hooks never fire"; how stash outcomes are classified | 3 |
| `05-ai-engines.md` | Process groups and tree kill in Go; `CREATE_NO_WINDOW`; prompts path `backend/ai/prompts/` | 4 |
| `06-providers.md`, `07-review-pipeline.md`, `14-work-items.md` | Implementation paths; live-run record for the Go app | 5 |
| `08-api-client.md` | Transport per send in Go, TLS policy implementation (§8.13), decompression | 6 |
| `10-security.md` | Backends (`keybase/go-keychain`, `wincred`); the one-time macOS keychain prompt after migrating from 2.7.1 (new `DIVERGENCE-SEC-*`) | 2 |
| `11-files-search-terminal.md` | User regex patterns are RE2 (lookaround now rejected — new `DIVERGENCE-FILE-*`); watcher backend (`syncthing/notify`) — answers `AMBIGUOUS-FILE-a`; PTY library | 3 |
| `13-cross-language-contracts.md` | Every `XLANG-*` implementation line: C# path → Go path; re-run the sweep (grep `mirror`, `in sync`, `must match` in `backend/` and `frontend/`) | 9 |
| `15-dbml.md` | Drivers; SQLite read-only open/close | 7 |
| `90-ambiguities.md`, `91-known-bugs.md` | New markers raised by the port; live-run results | each |
| `test-vectors/README.md` | Loader is `backend/shared/testvectors`; `sourceFile` values remain historical | 1 |
| `UX-REDESIGN.md` | CDP smoke passes → Wails verification (§7.4 W14); WebKit constraints (W1–W9); `OpenPrLinkModal` no longer reads the clipboard on mount (also BOOT-029's edge case) | 1 |

Marker ids: take the **next free letter** in the owning document's ledger; never reuse a closed id.

---

## 14. Open decisions for the operator

| # | Decision | Options | Recommendation |
|---|---|---|---|
| D1 | Should CI run the test suites? | (a) keep "no CI runs a test", `release.sh` is the gate; (b) add a `test` job (Go + renderer) on macOS and Windows before `installers` | **(b)**: Actions minutes are free for the public repository, and Windows-only breakage (the `BUG-BOOT-a` history, "not verified on Windows" in the credential code) is exactly what a Windows runner catches |
| D2 | Where does the Go code live and publish? | (a) develop here, then **cut over into `gastonlarap-a11y/code-flow`** as 3.0.0; (b) separate repository plus a last Electron 2.x release that repoints its updater; (c) separate repository uploading to `code-flow`'s releases with a PAT | **(a)**: keeps the update feed 2.7.x reads, the release history and a single source of truth; module path `github.com/gastonlarap-a11y/code-flow` |
| D3 | Minimum macOS / WebKit | **DECIDED 2026-09-17: macOS 26 (Tahoe).** Option (a) below was already stale when it was written — Apple stopped patching macOS 14 Sonoma on 2026-09-14, two days earlier, and its newest Safari is 26.6.1. Every macOS Apple still supports (15, 26, 27) ships Safari 27, so anchor positioning, the Popover API and `light-dark()` are native and **no `@floating-ui/dom` fallback is built**. `LSMinimumSystemVersion` and `MACOSX_DEPLOYMENT_TARGET` are 26.0; the start-up `CSS.supports("anchor-name: --a")` check stays as a net, because a Tahoe install that never updated Safari can be on 26.0 and `position-try-fallbacks` was extended in 26.2. Original options: (a) **macOS 14 with Safari ≥ 26.2** — Safari 26.x ships for macOS Sonoma and Sequoia (Apple's Safari 26.0–26.6 release notes), updates the system WebKit that WKWebView uses, and brings anchor positioning (26.0) with the flip fallbacks (26.2): no renderer fallback needed, plus a start-up check (`CSS.supports("anchor-name: --a")`) that shows "update Safari" instead of a broken layout; (b) macOS 14 + Safari ≥ 17.5 with the `@floating-ui/dom` W1 fallback; (c) Wails' default 12.0 (Popover/`light-dark()` break on old Safari) | **(a)** — cheapest and measured working (M-1 on Safari 27); `LSMinimumSystemVersion` 14.0 |
| D4 | Code signing | (a) stay ad-hoc; (b) Apple Developer ID + notarisation and a Windows Authenticode certificate; (c) **a free, stable self-signed code-signing identity** for macOS builds | (b) when affordable. Meanwhile (c): with ad-hoc signing the keychain partition is `cdhash:` and changes every build, so macOS asks for the keychain password after **every** update (true today); a stable signing identity makes one *Always Allow* persist across updates (the approach of `claude-usage-swift` PR #31). It does not help Gatekeeper |
| D5 | Wails' built-in updater (`pkg/updater`) after 3.0 | in-place `.app`/`.exe` swap with signature verification vs today's open-the-`.dmg`/run-the-installer | Revisit after 3.0 ships; it changes the renderer contract and the release artefacts |
| D6 | Clean Electron leftovers (`%APPDATA%\CodeFlow`, `~/Library/Application Support/CodeFlow`) | leave / delete on first 3.0 run / delete in the NSIS installer | Delete in the installer on Windows; leave on macOS (no installer) |
| D7 | Update checks require a GitHub token although releases are public | keep (parity) / allow anonymous checks | Keep for 3.0 (no behaviour change during a port); change afterwards as its own release note |
| D8 | macOS Intel / universal build | arm64 only (parity) / universal | arm64 only for 3.0 |
| D9 | `CHANGELOG.md` (stale since 1.9.1) | restart at 3.0.0 / drop and rely on generated release notes | Restart at 3.0.0 |
| D10 | Spec's own open questions: git network cancellation (`AMBIGUOUS-GIT-b`), keeping `C:\CodeFlow`, plaintext API-client auth in SQLite | — | Out of scope for the port (drop-in means keeping them); decide separately |

---

## 15. Appendices

### 15.1 Verified versions (2026-09-16)

| Item | Version | Published | Source |
|---|---|---|---|
| Go | 1.27.1 | 2026-09-01 | go.dev/doc/devel/release |
| Wails v3 module / CLI / npm runtime | v3.0.0-beta.23 | 2026-09-16 | `gh release list -R wailsapp/wails`, proxy.golang.org, registry.npmjs.org |
| Wails v2 (not used) | v2.16.0 | 2026-09-14 | proxy.golang.org |
| modernc.org/sqlite | v1.59.0 | 2026-09-15 | proxy.golang.org |
| mattn/go-sqlite3 (rejected) | v1.14.52 | 2026-09-05 | GitHub releases |
| ncruces/go-sqlite3 (fallback) | v0.35.5 | 2026-09-16 | GitHub releases |
| go-git v5 (rejected) / v6 | v5.19.2 / v6.0.0-alpha.5 | 2026-07-29 | proxy.golang.org, GitHub |
| git2go (rejected) | v34.0.0 | 2022-10-04 | proxy.golang.org |
| keybase/go-keychain | v0.0.1 (repo pushed 2026-09-09) | 2025-02-27 | proxy.golang.org, GitHub |
| danieljoos/wincred | v1.2.3 | 2025-10-02 | GitHub releases |
| zalando/go-keyring (rejected) | v0.2.8 | 2026-03-23 | GitHub releases + source |
| charmbracelet/x/xpty | v0.1.4 | 2026-07-30 | proxy.golang.org |
| aymanbagabas/go-pty (fallback) | v0.2.3 | 2026-05-17 | GitHub releases |
| creack/pty | v1.1.24 | 2024-10-31 | GitHub releases |
| syncthing/notify | v0.0.0-20250528144937-c7027d4f7465 | 2025-05-28 | proxy.golang.org |
| fsnotify/fsnotify (rejected) | v1.10.1 | 2026-05-04 | GitHub releases |
| coder/websocket | v1.8.15 | 2026-06-15 | GitHub releases |
| eclipse/paho.golang | v0.23.0 | 2025-09-06 | proxy.golang.org |
| eclipse/paho.mqtt.golang | v1.5.1 | 2025-09-16 | proxy.golang.org |
| jackc/pgx/v5 | v5.11.0 | 2026-09-07 | GitHub releases |
| microsoft/go-mssqldb | v1.11.0 | 2026-08-24 | GitHub releases |
| go-sql-driver/mysql | v1.10.1 | 2026-09-02 | GitHub releases |
| andybalholm/brotli | v1.2.4 | 2026-09-10 | proxy.golang.org |
| software.sslmate.com/src/go-pkcs12 | v0.7.3 | 2026-06-24 | proxy.golang.org |
| google/uuid | v1.6.0 | 2024-01-23 | proxy.golang.org |
| golang.org/x/sys | v0.48.0 | 2026-08-31 | proxy.golang.org |
| golang.org/x/sync | v0.23.0 | 2026-08-31 | proxy.golang.org |
| stretchr/testify | v1.12.1 | 2026-08-19 | GitHub releases |
| golangci-lint | v2.13.2 | 2026-08-27 | GitHub releases |
| gotestsum | v1.13.0 | — | GitHub releases |
| zishang520/socket.io (rejected) | v3.0.5 | 2026-09-16 | proxy.golang.org |
| creativeprojects/go-selfupdate (rejected) | v1.6.0 | 2026-07-08 | proxy.golang.org |

### 15.2 Literals that live only in code (transcribed)

#### 15.2.1 `TransientNetwork` — "the connection was never made"

Case-insensitive substring match against the error message **and every wrapped error** (in Go: walk
`errors.Unwrap`, including `errors.Join` branches). Timeouts are deliberately absent (the far side may
have acted).

```
no such host
nodename nor servname provided
name or service not known
temporary failure in name resolution
no address associated with hostname
connection refused
network is unreachable
network is down
no route to host
```

Used by `TransientRetryTransport` (one immediate retry, body-less requests only) and by the AI runner
(one retry of a read-only run).

#### 15.2.2 `ErrorLog` / `ShellLog` redaction and format

Applied in this order (patterns are RE2-compatible as written; `$1` → `${1}` in Go replacements):

| # | Pattern | Replacement |
|---|---|---|
| 1 | `(\w+)://[^/\s:@]+:[^/\s@]+@` | `$1://***:***@` |
| 2 | `(?i)\b(authorization\|x-api-key\|private-token\|api-key)\s*:\s*(?:bearer\s+\|token\s+\|basic\s+)?[^\s"',}\]]+` | `$1: ***` |
| 3 | `(?i)\bbearer\s+[A-Za-z0-9._\-]{8,}` | `Bearer ***` |
| 4 | `\b(gh[pousr]_[A-Za-z0-9]{16,}\|github_pat_[A-Za-z0-9_]{20,}\|xox[abposr]-[A-Za-z0-9-]{10,}\|sk-[A-Za-z0-9-]{20,})` | `***` |

(`\|` above is table escaping for `|`.)

- `errors.log` line: `yyyy-MM-dd HH:mm:ss zzz  <method>  <Type>: <redacted message>` (two spaces between
  fields; `zzz` = `+02:00` style offset; local time). Go layout: `2006-01-02 15:04:05 -07:00`.
- `shell.log` line: `<ISO-8601 UTC>  <LEVEL padded to 5>  <redacted message>`.
- `startup.log` line: `yyyy-MM-dd HH:mm:ss zzz  startup/<stage>  <Redact(full error chain)>` (local time
  with offset, two spaces between fields — `StartupLog.cs`); written to stderr and to
  `{base}/logs/startup.log`, falling back to `<temp dir>/startup.log` when the log directory cannot be
  written.
- All three: roll to `<file>.1` (overwrite) when the file exceeds 2 MiB (`2 * 1024 * 1024`), append,
  flush, never fail the caller.

#### 15.2.3 `SeededPromptHistory` digests

Lowercase hex SHA-256 over the UTF-8 bytes (no BOM) of a stored prompt row:

| Prompt kind | Former built-in digests |
|---|---|
| `review_standard` (`DEFAULT_PR_REVIEW_STANDARD.txt`) | `6b8bdda6da739ae4f60809830e7854a91278d0a32862a8e80385ac76d1f3d0c4` (v2.5.1) |
| `ticket_review_standard` (`DEFAULT_TICKET_REVIEW_STANDARD.txt`) | `a5cb429d5f7e034aec95e3e381164cc8198293f339b6b6c606653ebc6cc1756c` (v2.5.1) |

A stored row matching one of these is an unedited former default and is replaced by the current default;
anything else is the user's text and stays. **Before changing a seeded prompt, append its outgoing
digest here.** `test-vectors/prompts/review_standard.v2.5.1.txt` is the v2.5.1 text.

#### 15.2.4 Login-shell `PATH` (macOS only; skipped on Windows)

1. `shell := $SHELL`, or `/bin/sh` when unset.
2. Run `shell -ilc "echo __CODEFLOW_ENV__; env; echo __CODEFLOW_ENV__"` with stdin closed, **stderr
   discarded**, stdout captured as UTF-8. If it cannot be spawned → keep the inherited `PATH`.
3. Kill it with SIGKILL after **2 000 ms** → keep the inherited `PATH`.
4. `extractPath(output)`: find the first `__CODEFLOW_ENV__`, then the next one after it; no pair → none.
   Between them, split on `\n`; the first line starting with `PATH=` gives the value (trimmed); empty →
   none.
5. `mergePath(captured, inherited)`: split both on `:`; captured entries first, then inherited; drop empty
   entries and duplicates (first occurrence wins); join with `:`.
6. Set the process `PATH` to the merge before anything is spawned (every child inherits it).

Test cases to port: marker parsing with a noisy profile before/after the block, `=` inside the PATH value,
a missing or single marker, an empty PATH, merge order, duplicates, empty entries, an undefined inherited
PATH.

#### 15.2.5 AI binary discovery (from `05-ai-engines.md`, AI-005…007)

- **Install directories** — macOS: `~/.local/bin`, `~/.claude/local`, `~/.opencode/bin`, `~/.bun/bin`,
  `~/Library/pnpm`, `~/.npm-global/bin`, `/opt/homebrew/bin`, `/usr/local/bin`. Windows:
  `%USERPROFILE%\.local\bin`, `%USERPROFILE%\.claude\local`, `%USERPROFILE%\.opencode\bin`,
  `%APPDATA%\npm`, `%LOCALAPPDATA%\agy\bin`, `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin`.
- **Search order**: install directories, then every entry of the process `PATH` (which already includes
  §15.2.4's merge).
- The child's `PATH` is that joined list.
- **Windows resolution**: a name with a separator or an extension is used as-is; otherwise for each
  directory in order try `<name>.exe`, `<name>.cmd`, `<name>.bat`; first existing file wins. The probe
  (`find_on_path`) also tries the bare name.
- **Manual override** `{provider}_binary_path` (non-blank) is used exactly as stored.

#### 15.2.6 Terminal and smoke-test parameters

| Parameter | Value |
|---|---|
| PTY name / `TERM` | `xterm-256color` |
| Initial size (terminal) | 100 cols × 30 rows |
| Smoke probe | 120×30, resize to 100×24, command `echo marker; exit` (Windows `cmd.exe`), 15 s timeout, 5 s wait for exit |
| Read size | 4 096 bytes, each chunk decoded as lossy UTF-8 |
| Output channel | capacity 64, blocking when full |
| Windows shell | Git Bash only, `--login -i`; discovery via `git --exec-path` then `bin\bash.exe` under that directory and up to 5 parents (6 directories), then two fixed paths |
| macOS shell | `$SHELL` or `/bin/bash`, no arguments |

#### 15.2.7 HTTP parameters

| Client | Parameter | Value |
|---|---|---|
| Shared (providers, tickets, update) | overall timeout | 5 minutes |
| Shared | connection lifetime | 15 minutes (.NET pooled lifetime) |
| Shared | retry | once, immediately, body-less requests, `TransientNetwork` failures only |
| API client | client | new per send |
| API client | default timeout | 30 000 ms |
| API client | max redirects | 10 (shared hop counter) |
| API client | response cap | 50 MiB, truncates |
| API client | advertised encodings | `gzip, br, deflate` |
| API client | cookies | no jar |
| DB introspection | timeout | 30 s, no pooling |

#### 15.2.8 Updater constants

Repository `gastonlarap-a11y/code-flow`; endpoint `/repos/{owner}/{repo}/releases/latest`; headers
`Authorization: Bearer <token>`, `Accept: application/vnd.github+json` (asset downloads
`application/octet-stream`), `X-GitHub-Api-Version: 2022-11-28`, `User-Agent: CodeFlow/<version>`;
token cascade keychain `github-token:github.com` → `gh auth token` (5 s); reasons `no-credential`,
`unauthorized`, `no-release`, `no-asset`, `unreachable`; install kinds `auto` (Windows) / `manual`;
download directory `~/Downloads`; chunk 81 920 bytes; progress events at 0, every 256 KiB and a final
`done`; hand-off opens the installer (Windows) or the `.dmg` (macOS, which mounts it) and does not quit;
`update_current_version` default `0.0.0` (measured: the installed core answers `2.7.1` when started with
`--app-version 2.7.1`).

#### 15.2.9 AI run constants

Chunk 8 192 chars, also the forced-emit threshold without a newline; split on `\n` (with AI-010's `\r`
escape); ANSI stripped + `TrimEnd`; blank lines dropped; line cap 2 000 Unicode scalars plus `…`; silence
timeout 10 minutes, reset on every read; reader wait after kill 2 s; markers `RUN_CANCELLED::` (bare) and
`RUN_TIMED_OUT::<whole minutes>`; scratch
sweep age 1 h; agy inline-brief threshold 12 000 characters; `claude` commit model
`claude-haiku-4-5-20251001`; fix tools claude `Read, Edit, Write, Grep, Glob`, opencode
`read, edit, write, bash, grep, glob`; Codex models cache `$CODEX_HOME` or `~/.codex/models_cache.json`.

#### 15.2.10 Files constants

Watcher: 200 ms poll, 400 ms throttle, noise `*.lock`, `FETCH_HEAD`, `COMMIT_EDITMSG`. Search: 20 000 files,
1 MiB per file, 400 characters per line, 20 hits per file. Checkpoints: 20 kept, refs under
`refs/codeflow/checkpoints/`.

### 15.3 C# → Go idiom map

| C# in the source | Go in the port |
|---|---|
| `Task`/`ValueTask`, `async`/`await` | plain functions; concurrency with goroutines started through `safego`; `errgroup` for fan-out |
| `CancellationToken` (forwarded everywhere) | `context.Context` as the first parameter, forwarded everywhere |
| `System.Threading.Channels` | buffered `chan` (capacity = bounded channel size; blocking send = `FullMode.Wait`) |
| `IAsyncEnumerable<T>` | `iter.Seq[T]` for pull-style, a channel for push-style |
| `record` / `sealed` DTOs | structs with explicit JSON tags |
| `System.Text.Json` source-generated contexts (snake_case / camelCase) | struct tags; `jsonwire.Marshal` with `SetEscapeHTML(false)` |
| `JsonElement` params + `Arg`/`OptionalArg` | `json.RawMessage` + `bridge.Arg[T]` / `bridge.OptionalArg[T]` |
| Exceptions + domain exception types | returned `error`; typed errors (`*azure.Error`) checked with `errors.As` / `errors.AsType` |
| `catch (Exception)` at the edge | `Service.Invoke` (+ `recover`) |
| `SemaphoreSlim(1,1)` around SQLite | `sync.Mutex` in `storage.DB` |
| `Microsoft.Data.Sqlite` | `database/sql` + `modernc.org/sqlite` |
| `HttpClient` singleton + `DelegatingHandler` | shared `*http.Client` + wrapping `http.RoundTripper` |
| `Process` + `ProcessStartInfo` | `exec.CommandContext` through `shared/proc` |
| `Process.Kill(entireProcessTree: true)` | `kill(-pgid)` / Job Object (§8.0) |
| `FileSystemWatcher` | `syncthing/notify` |
| `Regex` / `[GeneratedRegex]` | `regexp.MustCompile` at package level (§8.0 for class differences) |
| `DateTimeOffset.UtcNow` formatted | `time.Now().UTC().Format(...)` with the layouts of §5.2 and §15.2.2 |
| `Guid.NewGuid().ToString()` | `uuid.NewString()` |
| `Convert.ToHexStringLower(SHA256.HashData(…))` | `hex.EncodeToString(sha256.Sum256(…)[:])` |
| `CryptographicOperations.FixedTimeEquals` | `crypto/subtle.ConstantTimeCompare` |
| Embedded resources (`EmbeddedResource`) | `//go:embed` |
| `[InternalsVisibleTo]` for tests | tests in the same package (`package git`, not `git_test`) where internals are needed |
| xUnit `[Fact]` / `[Theory]` + `[MemberData]` | `func TestX(t *testing.T)` + `t.Run(<C# method name>, …)`; vectors via `shared/testvectors` |
| `[Collection("serial-…")]` | no `t.Parallel()` + package-level mutex |
| NuGet central pinning + lock files | `go.mod` exact versions + `go.sum`, `-mod=readonly` |

### 15.4 Marker glossary (from `00-conventions.md`)

| Marker | Meaning for the port |
|---|---|
| `VERBATIM` | Copy byte-for-byte. Never translate, reformat or "clean up". |
| `BUG-*` (open) | Preserve, and keep the test that pins it. |
| `BUG-*` (closed) | The fix is the behaviour; port the fix. |
| `DIVERGENCE-*` | Looks wrong, is intentional; preserve. |
| `AMBIGUOUS-*` | Do not resolve while porting; port what the code does today. |
| `UNVERIFIED` / `VERIFIED-LIVE` | Port the same way; re-verify live where the phase says so. |
| `DEAD` | `debug_is_running`; nothing calls it; do not port. |

### 15.5 Command → phase map

Counted from `.Add("<name>"` in each C# registration file (unique names; the `.Add` calls in
`Ai/Engines/*` and `Workspaces/SkillInstaller.cs` are not commands):

| C# registration file (commands) | Phase |
|---|---|
| `Platform/AppCommands` (1: `reset_app_data`) | 1 |
| `Workspaces/WorkspaceCommands` (27), `Workspaces/SkillCommands` (10), `Activity/ActivityCommands` (7), `Security/SecretCommands` (9) — plus the 7 review-run history commands from `ReviewCommands` | 2 |
| `Git/GitCommands` (47, checkpoints included), `Files/FileCommands` (13), `Files/WatcherCommands` (3, incl. `scan_staged_secrets`), `Terminal/TerminalCommands` (4) | 3 |
| `Ai/AiCommands` (13) | 4 |
| `Providers/ProviderCommands` (19), `Review/ReviewCommands` (the remaining 4 of its 11), `Tickets/TicketCommands` (17) | 5 |
| `ApiClient/ApiCommands` (27), `ApiClient/ApiHttpCommands` (5), `ApiClient/ApiStreamCommands` (9) | 6 |
| `Dbml/DbmlCommands` (10) | 7 |
| `Update/UpdateCommands` (3) | 8 |
| `debug_*` (9), `api_grpc_call`, `api_grpc_describe` | deferred — answer `unknown command` |

Total 235 registered. `01-ipc-surface.md` groups some commands under other files; **the contract test
(§9.4 #1), built from the renderer's wrappers, is the authority** — if a count here disagrees with it,
trust the test.

