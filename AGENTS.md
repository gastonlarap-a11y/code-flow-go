# CodeFlow (Go + Wails v3)

Desktop code-review and API workbench: **one Go binary** hosting a Wails v3 window with the React
renderer embedded. It began as a port of CodeFlow 2.7.x (Electron shell + .NET sidecar) and is now
the product: **this repository is the only active one**, and `gastonlarap-a11y/code-flow` — the
Electron/.NET tree it replaced — is deprecated.

**The port is finished and shipped.** `v3.0.0` through `v3.3.1` were published from here, so the
cutover is history rather than a plan: what 3.0.0 had to be, a drop-in replacement keeping every
user's database, credentials and update path, it was. The command surface is closed at 235
registered + 11 deferred on purpose = the 246 the 2.x renderer called. The PR review pipeline runs,
reconciles and publishes; the work items cache, mirror, judge and — on a button press, and only
there — comment. The API workbench is complete: its stores, HTTP with Digest and SigV4, and all
three streaming transports behind one connection registry. So is the schema designer: documents,
layouts, connections, the assistant and all four introspectors. The updater checks, verifies against
the release's own digest and hands over.

**What came after the port grows this tree rather than matching a C# one.** The diagram editor
(`backend/diagram/`, `frontend/src/lib/diagram/`, `docs/business-rules/16-diagrams.md`) is the first
of these: a canvas for process flows and use cases whose documents are **Mermaid `flowchart` files**,
`*.mmd`, readable by any Mermaid viewer and by a model. It owns the one command this repository
invented, `diagram_list_documents`, which is why `backend/app/contract_test.go` now separates
`portedCommandCount` — closed history — from `newSincePort`.

**What the migration still owes, and what it does not.** Two of Phase 9's steps need a person at
each of the two operating systems and block nothing: the manual acceptance checklists, and the half
of the performance record a headless run cannot measure (cold start, idle memory, `get_status` on a
100 000-file repository). One is a real backlog: the `Implementation` lines of twelve
`docs/business-rules/` documents still cite only C# paths (§11 Phase 9 step 5 in `MIGRATION-GO.md`).

`MIGRATION-GO.md` is the record of how the port was done, not a plan to follow. `docs/` is the
authoritative specification (~11 000 lines) and outranks any assumption about behaviour.
`backend/app/contract_test.go` re-derives every command name from the renderer and fails on drift in
either direction; `portedCommandCount` is closed, and a feature written here rather than ported adds
its name to `newSincePort`.

## Layout

| Path | What |
|---|---|
| `main.go` | Composition only — no behaviour |
| `backend/bridge/` | The one bound method: `Service.Invoke(method, params)` → command registry |
| `backend/desktop/` | **The only package that imports Wails**: window, tray, menu, quit, dialogs |
| `backend/<feature>/` | One package per feature; each exposes `Register(r, deps)` |
| `backend/shared/` | `proc` (child processes), `safego` (goroutines), `sentinel` (error prefixes), `docwalk` (finding a folder's documents) |
| `frontend/` | The React 19 renderer, copied from 2.x and grown since; reaches Go only through `src/lib/bridge/host.ts` |
| `docs/business-rules/` | The specification: every command, the 13 events, the storage schema |
| `scripts/` | What the release page tells users to run: `install-macos.sh` installs without the quarantine flag that makes Gatekeeper refuse an unnotarized build |
| `tools/parity/` | The differential oracle: drives the installed 2.7.x core and this one, compares |
| `tools/inventory/` | The test audit: the 1 232 C# behaviours against this tree's, re-counted on each run |
| `build/` | Packaging assets, **generated** by `wails3 generate build-assets` — excluded from lint, and the generator overwrites `appicon.png` and `config.yml`, so never re-run it blind |

## Commands

```sh
task check            # everything the CI gate runs
task go:check         # go vet + golangci-lint + go test -race + the goroutine gate + govulncheck
task frontend:check   # pnpm typecheck + pnpm test
task parity           # replay the scripted requests against the installed 2.7.x core (needs it)
task inventory        # audit the Go tests against the C# suite they replace
task build            # renderer + binary into bin/
task package:mac:dmg  # the published CodeFlow-<v>-arm64.dmg and its digest
task package:win      # the NSIS installer, the portable build and their digests (on Windows)
task dev              # hot reload (Go + Vite on 1420)
task smoke            # the packaged binary's own environment probes
```

Go needs `GOROOT`/`PATH` on 1.27.1; `task` sets the macOS deployment target for you.

Two things that bite on a fresh machine: **`task` itself may not be installed** — `wails3` embeds the
same runner, so `wails3 task check` runs the Taskfile as written. And **the `wails3` CLI must be the
exact Wails version in `go.mod`** — install it with the line in README, which reads it from there.

`task dev` runs the `dev:*` tasks listed under `dev_mode.executes` in `build/config.yml`. That block
is hand-written like the rest of that file's edits: `wails3 generate build-assets` would replace it
with commands pointing at generated tasks the root Taskfile does not include. Through 3.7.0 it
used an older schema, which the CLI no longer reads, and every `task dev` failed with `root path is
required`.

## Hard rules

- **Do not guess.** Behaviour marked `AMBIGUOUS-*` in `docs/` was not resolved by the port and is not
  resolved by reading the Go code either — it is still open.
- **Do not fix.** `BUG-*` rows still open in `docs/business-rules/91-known-bugs.md` are preserved on
  purpose — existing installs and the renderer depend on them. Fixing one is a separate, named change.
- **A new feature is not a port.** There is no C# original to match, so `AMBIGUOUS-*`/`BUG-*` do not
  apply to it: it gets its own document under `docs/business-rules/`, and any command it adds goes in
  `newSincePort` in `backend/app/contract_test.go`. The two rules above govern ported behaviour only.
- **`VERBATIM` is byte-level**: sentinel prefixes, prompts, regexes, keychain key formats, and the
  Spanish field names in review findings (`tipo`, `categoria`, `archivo`…). Never translate them.
- **Never wrap a sentinel-bearing error.** The renderer matches eight of them with `startsWith`.
- **No credential ever reaches a child process's environment** (SEC-007). `proc.Environment` is the
  only way to build one and cannot add a variable.
- **Goroutines start through `shared/safego.Go` only.** An unrecovered panic anywhere ends the whole
  process. The CI gate greps for bare `go` statements.
- **Features never import Wails.** They take `bridge.Emitter` and small interfaces instead.
- Changed behaviour → update the owning document under `docs/` in the same change.
- Commits, pushes and PRs only on explicit request. No AI attribution anywhere.

## Engineering standards

- Every feature ships with its tests. Run `task check` before declaring work done; report real results.
- Handle errors explicitly at boundaries; never swallow exceptions or ignored error returns.
- No speculative abstractions: introduce a pattern only for a problem this repo has, and say which and why.
- Ambiguous request → ask targeted questions first. Requested approach wrong or beatable → say why and
  let the requester choose before proceeding.
