# 21 — Requirements: what the features need installed

CodeFlow ships as one binary, but several features drive programs of the user's own: git for every
repository feature, Node.js and Go for the code map's symbol view, Archify for the code map's
diagrams, and an AI engine's command-line tool for everything the AI does. Someone opening the app
on a new machine — or someone else's — has to be able to read, in the app, what to install for what,
whether this machine already has it, and the vendor's own command to get it.

**This document is not a port.** CodeFlow 2.x had no such view, so there is no C# original, no
`BUG-*` to preserve and no `AMBIGUOUS-*` to resolve. Every rule below was decided here.

**The app never installs anything.** It finds, explains, and hands over the command — to copy, or
typed into its own terminal for the person to run. Installing software on someone's machine is
their decision, and a command they read before pressing Enter is one they chose.

## Scope

- `backend/requirements/` — finding git, Node.js, Go and Archify, and the `requirements_status`
  command
- `frontend/src/lib/requirements.ts` — the catalogue: what each tool is for and how to install it
- `frontend/src/lib/aiProviders.ts` (`setup`) — the same for each AI engine
- `frontend/src/components/settings/RequirementsSettings.tsx` · `CommandLine.tsx` ·
  `frontend/src/components/common/RequirementNotice.tsx` · `frontend/src/lib/ui/typeInTerminal.ts`

---

### REQ-001 The tools, and what each is needed for
**Implementation**: `backend/requirements/requirements.go` · `frontend/src/lib/requirements.ts`
**Behaviour**: four tools besides the AI engines, each with what stops working without it:

| Tool | Needed for | Will do |
|---|---|---|
| Git | every repository feature — changes, branches, history, reviews, the code map | any version |
| Node.js | the symbol map's TypeScript reader; running Archify; **installing** Archify | 22.20 or later — the `skills` CLI's own `engines` (skills 1.7.2); 24 LTS recommended |
| Archify | the code map's diagrams: the class diagram and the five the AI draws (`20-archify-diagrams.md`) | 3.1 or a later 3.x (ARCHIFY-002) |
| Go | the symbol map's Go reader; not the drawn map, which reads Go itself (REPOMAP-003) | any version |

Git is the one *required*; the others are *for some features*. The AI engines — Claude Code,
Antigravity (`agy`), Codex, opencode, Ollama — are listed beside them as *one is enough*.
**Inputs / outputs**: none of its own.
**Edge cases**: an Archify older than 3.1 is *installed but needs updating*, not *missing* — it is
found by `archify.Located`, judged by `archify.Supported`.
**Frontend dependency**: `TOOL_REQUIREMENTS`, `AI_PROVIDERS`.
**Markers**: none

### REQ-002 Finding them
**Implementation**: `backend/requirements/requirements.go` (`Finder.Status`) · `commands.go` ·
`backend/app/registry.go`
**Behaviour**: `requirements_status` answers the four, in that order. Each program is looked for
where every child process of the app looks — Go and Node.js through the code map's discovery
(`codemap.DiscoverTools`: the install folders, then `PATH`), git through `ai.FindOnPath` — and asked
its version (`git --version`, `node --version`, `go version`), whose first dotted number is kept.
Archify is looked for in every folder `npx skills add` installs into, and the open repository's own
(ARCHIFY-002). The finder is built per call, so a program installed while the app runs is found on
the next check.
**Inputs / outputs**: `requirements_status(rootPath?)` → `[{ id, installed, version, path, supported }]`,
always four entries.
**Edge cases**: a program found but not answering within five seconds, or printing no version, is
*installed* with an empty version and judged supported — there, and nothing known against it. The
version runs with the app's `PATH`-only environment (SEC-007).
**Frontend dependency**: `requirementsStatus`, `requirementsStore`.
**Markers**: none

### REQ-003 Settings → Requirements
**Implementation**: `frontend/src/components/settings/RequirementsSettings.tsx` · `SettingsView.tsx`
**Behaviour**: the last of the global settings sections. A card of tools and a card of AI engines;
each row names the program, how much it matters, what it is for, and its state — *installed* with
its version, *update needed*, or *not installed*. A row that is not ready shows the install command
for this platform (REQ-004), what to run afterwards where there is a sign-in (`codex login`), and
the vendor's documentation. *Check again* asks both `requirements_status` and every engine's
`check_ai_provider` anew; the view checks once when it opens.
**Inputs / outputs**: REQ-002's command and `check_ai_provider`.
**Edge cases**: a row whose state is not known yet shows a spinner rather than *not installed*. A
program installed into a folder the app did not have on its `PATH` at start may need the app
restarted to be found; the section says so.
**Frontend dependency**: this is it.
**Markers**: none

### REQ-004 The install commands, per platform, never run by the app
**Implementation**: `frontend/src/lib/requirements.ts` (`PlatformCommand`, `commandFor`) ·
`frontend/src/lib/aiProviders.ts` · `frontend/src/components/settings/CommandLine.tsx` ·
`frontend/src/lib/ui/typeInTerminal.ts`
**Behaviour**: every command is the vendor's own recommended one as of October 2026, one for macOS
and one for Windows: the native installer where there is one (Claude Code, Codex, Antigravity,
opencode, Ollama), Homebrew on macOS and winget on Windows otherwise (Node.js, Go, Git on Windows),
`xcode-select --install` for Git on macOS, and `npx skills add tt-a1i/archify -g` for Archify. A
Homebrew command says that it needs Homebrew, with its address. Each command can be **copied**, or
**typed into the app's terminal**: a new terminal opens in the open project, Settings closes so it
shows, and the command is written without a newline — the person reads it and presses Enter.
**Inputs / outputs**: `open_terminal`, `write_terminal`.
**Edge cases**: with no project open there is no terminal to open, and the button says so. The
AI settings' own setup help shows the same commands (`SetupHelp`). Until this change the Claude
Code command was the npm one and Codex's was `winget` on every platform.
**Frontend dependency**: `CommandLine`.
**Markers**: none

### REQ-005 A feature says what it lacks where it is
**Implementation**: `frontend/src/components/common/RequirementNotice.tsx` ·
`frontend/src/components/codemap/map/AiDiagramDialog.tsx` · `frontend/src/components/diagram/ArchifyPane.tsx` ·
`frontend/src/components/codemap/SymbolsView.tsx`
**Behaviour**: before the person presses the button, not as the error after it:
- *Draw a diagram with AI* checks Node.js and Archify when it opens and, for the first one lacking —
  Node.js first, since without it Archify can neither be installed nor run — says so with its
  install command, and does not offer to draw;
- the diagram editor, opening an Archify document with no Archify installed, shows the install
  command (as it did) and a way to the full list;
- the symbol view, when a language's tool is missing, links the full list under its note.
Each notice leads to Settings → Requirements.
**Inputs / outputs**: REQ-002's command.
**Edge cases**: while the check has not answered, nothing is blocked.
**Frontend dependency**: `RequirementNotice`.
**Markers**: none
