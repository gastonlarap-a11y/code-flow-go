# CodeFlow

A desktop code-review and API workbench: pull-request review driven by AI engines, a Git client, a
terminal, an HTTP/WebSocket/MQTT client and a database schema designer, in one window.

This repository is **CodeFlow 3.0**, a rewrite of CodeFlow 2.7.x as a single Go binary hosting a
[Wails v3](https://v3.wails.io) window. It replaces an Electron shell plus a .NET sidecar, and ships
as a drop-in replacement: it installs over 2.7.x and keeps the same database, the same keychain
entries and the same update feed.

> **Status: Phases 1–4 complete.** The window, the desktop shell and the bridge; storage
> with its migrations, the credential store, workspaces, projects, settings, prompts, review
> contexts, agents, MCP servers, skills, chat and job history, and the review-run store; Git in
> full — status, diffs, history, branches, staging, committing, stash, merge and conflicts, AI
> checkpoints, remotes, the identity and clone/fetch/pull/push with their streamed progress; the
> file tree and its operations, the go-to-file palette, search and replace, the working-tree
> watcher, the pre-commit secret gate and the terminal; and the AI layer in full — the routing
> cascade, binary discovery, the run lifecycle with its streaming and cancellation, all six
> engines, chat with its history and session handling, and the commit-message, inline-edit,
> conflict-resolution, finding-fix and pull-request-description operations. **142 of 246 backend
> commands answer**; 11 are deferred on purpose and 93 remain. `MIGRATION-GO.md` is the plan and
> `docs/` is the authoritative specification of the behaviour being ported.
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
| Installed size | 440 MB | **56 MB** (measured, macOS arm64: the `.app` bundle `task package:mac` produces) |
| Processes | Electron main + renderer + GPU + utility, plus the .NET core | one |
| Backend languages | TypeScript + C# | Go |
| Renderer | bundled Chromium | the OS webview (WKWebView / WebView2) |

A whole class of defects disappears with the sidecar: the named-pipe address bug, socket errors
ending the app, the "core is down" state and the 64 MiB frame cap all lived in a transport that no
longer exists. The cost is the webview: Electron shipped its own Chromium, so the UI was identical
on both platforms; Wails uses the system's, which is why there is a minimum macOS below.

## What got faster, and what did not

Measured on this machine by `task parity -- -time`, which sends the same request to the installed
2.7.1 core and to 3.0 and times both. 2.7.1's figure includes one round trip over its unix socket,
because that is what the command cost a user; 3.0's is an in-process call, because there is no
transport left to include.

| Request | 2.7.1 | 3.0 | |
|---|---:|---:|---|
| `list_workspaces` (empty) | 2.39 ms | **82 µs** | 29× |
| `api_load_tree` | 4.42 ms | **229 µs** | 19× |
| `get_setting` (absent) | 233 µs | **35 µs** | 7× |
| an unknown command | 6.95 ms | **1 µs** | — |
| `get_status` | 12.6 ms | **9.7 ms** | 1.3× |
| `list_branches` | 5.67 ms | 8.06 ms | **0.7×** |
| `get_staged_diff` | 1.99 ms | 8.49 ms | **0.2×** |
| `get_working_diff` | 8.05 ms | 22.7 ms | **0.4×** |
| `get_commit_diff` | 748 µs | 14.8 ms | **0.05×** |

**Everything that reads the database or answers from memory is an order of magnitude faster**, and
for one reason: it is a function call now. The transport was most of what those commands cost.

**Everything that touches git is slower**, for the reason named in `backend/git`'s package comment:
this port replaced libgit2 with the `git` command line, so each git read is a process spawn — about
5–7 ms on macOS before git does any work. The fixture these numbers come from holds five files, so
what the git rows measure is almost entirely that fixed cost. On a large repository the work
dominates and the gap should narrow; **that has not been measured**, and until it is, the honest
claim is the narrow one: the port pays a per-command spawn, not that it is slower on real
repositories.

One consequence is worth knowing because it multiplies: `get_working_diff` renders each untracked
file with its own `git diff --no-index`, so a tree with fifty new files spawns fifty processes. It
is correct and it is what makes untracked content appear in the diff at all, but it is linear in
something a user controls.

Cold start, idle memory and a 100 000-file repository are **not** in the table: they need the window
open on each operating system, and they are part of the manual acceptance pass rather than something
this machine can answer alone.

## Installing a published build

The builds are ad-hoc signed and not notarized (§14 D4), so a `.dmg` **downloaded with a browser**
is refused on first launch — every time, on every version, because Gatekeeper evaluates each
quarantined bundle it is set on. `curl` sets no such flag, which is what `scripts/install-macos.sh`
is for:

```sh
curl -fsSL https://raw.githubusercontent.com/gastonlarap-a11y/code-flow-go/main/scripts/install-macos.sh | bash
```

It resolves the latest release, checks the disk image against the `.sha256` published beside it —
the same digest contract the in-app updater enforces (BOOT-021) — and copies the bundle into
`/Applications`. `CODEFLOW_DMG=<path>` installs from an image you already have, verifying it the
same way. The release workflow smoke-tests it against every image it publishes.

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

`bin/CodeFlow --smoke-test` answers "is this binary viable on this machine?" and exits 0 or 1. Three
probes, the same three 2.x had, each doing the thing rather than checking the library loads:

- **git** — `git --version`, then a repository initialised and read back in a temp directory with an
  isolated `HOME`, which is what a locked-down laptop actually fails;
- **storage** — a database opened and every migration run against it, because `modernc.org/sqlite`
  is SQLite transpiled to Go and its failures are platform-shaped;
- **pty** — a pseudo-terminal allocated and handed back. The terminal has no fallback: if the OS
  refuses, the panel simply never opens.

CI runs it on both operating systems against the binary it just built, and the release workflow runs
it against each installer before uploading — a probe missing here is a class of broken build that
ships.

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
- **Unsigned builds.** As in 2.x: Gatekeeper and SmartScreen warn on first run. On macOS the
  warning is not once per machine but once per **browser-downloaded copy**, so it returns with each
  version fetched from the releases page; an update the app downloads itself carries no quarantine
  flag and is never refused. `scripts/install-macos.sh` avoids it altogether (see *Installing a
  published build*).
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
