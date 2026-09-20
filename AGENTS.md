# CodeFlow (Go + Wails v3)

Desktop code-review and API workbench: **one Go binary** hosting a Wails v3 window with the React
renderer embedded. A port of CodeFlow 2.7.x (Electron shell + .NET sidecar), shipping as **3.0.0**,
a drop-in replacement that keeps every user's database, credentials and update path.

**Phases 1–8 complete; Phase 9 next** — **the command surface is done**: 235 registered + 11
deferred on purpose = the 246 the renderer calls, and nothing is left pending.
The PR review pipeline runs, reconciles and publishes; the work items cache, mirror, judge and — on
a button press, and only there — comment. The API workbench is complete: its stores, HTTP with
Digest and SigV4, and all three streaming transports behind one connection registry. So is the
schema designer: documents, layouts, connections, the assistant and all four introspectors. The
updater checks, verifies against the release's own digest and hands over. Phase 9 has done everything
one machine can: the differential oracle is green (`task parity` — 38 requests against the installed
2.7.1 core, zero unexplained differences), the test audit is done (`task inventory` — 1 232 C#
behaviours against 1 906 here, one real gap found and closed), the specification sweep is done for
the three documents it listed, and the performance record is in the README. **What is left needs a
person**: the manual acceptance checklists on macOS and Windows, cold start and idle memory, the
2.7.1 → 3.0.0 upgrade drills, and the cutover — which happens only on an explicit instruction.
`MIGRATION-GO.md` is the plan; `docs/` is the authoritative specification (~11 000 lines) and
outranks any assumption about behaviour. `backend/app/contract_test.go` is the progress meter: it
re-derives all 246 names from the renderer and fails if one is registered while still listed as
pending.

## Layout

| Path | What |
|---|---|
| `main.go` | Composition only — no behaviour |
| `backend/bridge/` | The one bound method: `Service.Invoke(method, params)` → command registry |
| `backend/desktop/` | **The only package that imports Wails**: window, tray, menu, quit, dialogs |
| `backend/<feature>/` | One package per feature; each exposes `Register(r, deps)` |
| `backend/shared/` | `proc` (child processes), `safego` (goroutines), `sentinel` (error prefixes) |
| `frontend/` | The React 19 renderer, copied from 2.x; reaches Go only through `src/lib/bridge/host.ts` |
| `docs/business-rules/` | The specification: 246 commands, 13 events, the storage schema |
| `tools/parity/` | The differential oracle: drives the installed 2.7.x core and this one, compares |
| `tools/inventory/` | The test audit: 1 232 C# behaviours against this tree's 1 906 |
| `build/` | Packaging assets, **generated** by `wails3 generate build-assets` — excluded from lint, and the generator overwrites `appicon.png` and `config.yml`, so never re-run it blind |

## Commands

```sh
task check            # everything the CI gate runs
task go:check         # go vet + golangci-lint + go test -race + the goroutine gate
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

## Hard rules

- **Do not guess.** Behaviour marked `AMBIGUOUS-*` in `docs/` is not resolved by this port.
- **Do not fix.** `BUG-*` rows still open in `docs/business-rules/91-known-bugs.md` are preserved on
  purpose — existing installs and the renderer depend on them. Fixing one is a separate, named change.
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
