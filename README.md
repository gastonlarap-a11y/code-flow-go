# CodeFlow

A desktop code-review and API workbench: pull-request review driven by AI engines, a Git client, a
terminal, an HTTP/WebSocket/MQTT client and a database schema designer, in one window.

This repository is **CodeFlow 3.0**, a rewrite of CodeFlow 2.7.x as a single Go binary hosting a
[Wails v3](https://v3.wails.io) window. It replaces an Electron shell plus a .NET sidecar, and ships
as a drop-in replacement: it installs over 2.7.x and keeps the same database, the same keychain
entries and the same update feed.

> **Status: Phases 1, 2 and 3 complete.** The window, the desktop shell and the bridge; storage
> with its migrations, the credential store, workspaces, projects, settings, prompts, review
> contexts, agents, MCP servers, skills, chat and job history, and the review-run store; Git in
> full — status, diffs, history, branches, staging, committing, stash, merge and conflicts, AI
> checkpoints, remotes, the identity and clone/fetch/pull/push with their streamed progress; the
> file tree and its operations, the go-to-file palette, search and replace, the working-tree
> watcher, the pre-commit secret gate and the terminal. **128 of 246 backend commands answer**; 11
> are deferred on purpose and 107 remain. `MIGRATION-GO.md` is the plan and `docs/` is the
> authoritative specification of the behaviour being ported.
>
> Git no longer goes through libgit2 — every operation is a `git` invocation, so parity is proven
> by tests against real temporary repositories rather than by reasoning about library semantics.
> Three of `MIGRATION-GO.md` §8.6's proposed commands turned out to be wrong when measured, and the
> tests that caught them are in `backend/git`.
>
> Storage has been proven against a real 2.7.1 database: it opens, migrates and leaves the schema
> byte-identical (23 tables, 10 indexes, `integrity_check` clean). Doing that turned up two errors
> in the specification, both now corrected in `docs/business-rules/03-storage.md`: the table count
> was wrong, and the migration call order was missing two steps whose absence would have left four
> columns off every upgraded database.

## Why the rewrite

| | 2.7.1 (Electron + .NET) | 3.0 (Go + Wails) |
|---|---|---|
| Installed size | 439 MB | **53 MB** (measured, macOS arm64) |
| Processes | Electron main + renderer + GPU + utility, plus the .NET core | one |
| Backend languages | TypeScript + C# | Go |
| Renderer | bundled Chromium | the OS webview (WKWebView / WebView2) |

A whole class of defects disappears with the sidecar: the named-pipe address bug, socket errors
ending the app, the "core is down" state and the 64 MiB frame cap all lived in a transport that no
longer exists. The cost is the webview: Electron shipped its own Chromium, so the UI was identical
on both platforms; Wails uses the system's, which is why there is a minimum macOS below.

## Requirements

| | Version | Notes |
|---|---|---|
| Go | 1.27.1 | `mise use -g go@1.27.1` |
| wails3 CLI | v3.0.0-beta.23 | must match the module version exactly |
| Node | 24 | |
| pnpm | 11.20.0 | pinned by `frontend/package.json` |
| git | 2.40+ | a runtime dependency, not just a build one |
| macOS | **26 (Tahoe) or newer** | Apple stopped patching macOS 14 in September 2026; every supported version ships Safari 26+, which the renderer relies on for CSS anchor positioning, the Popover API and `light-dark()` |
| Windows | 10 or 11 | needs the WebView2 runtime (preinstalled on 11) |

```sh
mise use -g go@1.27.1
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.23
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
wails3 doctor          # must be clean before anything else
pnpm -C frontend install --frozen-lockfile
```

## Commands

Run with `task <name>` (or `wails3 task <name>`, which bundles the runner).

| Command | What it does |
|---|---|
| `task dev` | Hot reload: Go rebuild plus Vite on port 1420 |
| `task build` | Builds the renderer and the binary into `bin/` |
| `task check` | Everything the CI gate runs |
| `task go:check` | `go vet`, `golangci-lint`, `go test -race`, and the goroutine gate |
| `task frontend:check` | `pnpm typecheck` and `pnpm test` |
| `task smoke` | Runs the built binary's own environment probes |

`bin/CodeFlow --smoke-test` answers "is this binary viable on this machine?" — it checks that git is
reachable and that a repository can be created and read back — and exits 0 or 1. CI runs it on both
operating systems against the binary it just built.

## Layout

```
main.go              composition only
backend/
  bridge/            the one bound method: Service.Invoke(method, params) → a command registry
  desktop/           the only package that imports Wails: window, tray, menu, quit, dialogs
  app/               start-up stages, the state the window reports, the smoke test
  platform/          paths, the login-shell PATH, transient-network detection, the HTTP client
  diagnostics/       errors.log, startup.log, shell.log, and the redaction they share
  shared/            proc (child processes), safego (goroutines), sentinel (error prefixes)
frontend/            the React 19 renderer, embedded with go:embed
docs/                the specification: 246 commands, 13 events, the storage schema
```

The renderer reaches Go through exactly one file, `frontend/src/lib/bridge/host.ts`, and every
backend command travels through one bound method rather than one binding per command. That is
deliberate — the renderer already owns 246 typed wrappers keyed by command name, and keeping them
untouched is what made a host swap of this size possible. `MIGRATION-GO.md` §3.3 has the reasoning.

## Data locations

CodeFlow 3.0 reads and writes exactly where 2.7.x did. Both are literal and neither is configurable.

| | Path |
|---|---|
| macOS | `~/CodeFlow` |
| Windows | `C:\CodeFlow` |

Inside it: `codeflow.db` (SQLite), `logs/`, `repos/`, `workspaces/`, `tickets/`. Credentials live in
the macOS keychain or Windows Credential Manager under `com.codeflow.app`, never in the database.

## Known gaps

- **`pnpm lint` does not run.** typescript-eslint refuses to load against TypeScript 7
  (`typescript-eslint does not support TS 7.0`), and no release — canary included — supports it yet;
  its tracking issue targets TS ≥ 7.1. `eslint.config.js` is kept intact so this becomes one command
  again the day support lands. `pnpm typecheck` is the static check until then.
- **Windows is unverified for Phase 1.** The frameless window, the caption buttons, ConPTY and the
  console-window suppression compile and cross-compile cleanly but have not been run.
- **Unsigned builds.** As in 2.x: Gatekeeper and SmartScreen will warn on first run.
- **The first keychain read of a 2.7.x credential will prompt.** With ad-hoc signing the keychain
  partition is the code's own hash, which changes on every build, so macOS asks for the login
  password once per update before handing over a token it previously allowed. *Always Allow*
  settles it until the next build; a stable signing identity would settle it for good.

## A note on backups

CodeFlow's database runs in WAL mode, so `codeflow.db` on its own is **not** a backup — the
`-wal` sibling can hold entire tables that have not been checkpointed yet. On the install this was
measured against, the bare `.db` file did not contain the `workspaces` table at all. Copy
`codeflow.db`, `codeflow.db-wal` and `codeflow.db-shm` together, or quit the app first (shutdown
closes the database, which checkpoints the log).

## Licence

MIT.
