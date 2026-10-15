# 20 — Repository diagrams with Archify

Diagrams of the open repository as documents in it: a class diagram drawn from the code map's
index, and architecture, workflow, sequence, data-flow and lifecycle diagrams an AI draws by reading
the code. Each is an [Archify](https://github.com/tt-a1i/archify) document — `*.archify.json`,
written into the repository beside the code it describes — that the user's own Archify checks and
draws, and the diagram editor shows in a frame of its own.

**This document is not a port.** CodeFlow 2.x had none of this, so there is no C# original, no
`BUG-*` to preserve and no `AMBIGUOUS-*` to resolve. Every rule below was decided here.

**Archify stays the user's install.** The app finds it, hands it documents and frames what it draws;
it does not bundle, update or patch it. Without it the documents still list and open — the editor
says how to install it — and the class diagram is still written, only unchecked.

## Scope

- `backend/archify/` — finding the CLI and pinning the repository (`cli.go`, `repo.go`), validating
  and rendering (`render.go`), the viewer (`viewer.go`), the class diagram (`classdoc.go`), the AI's
  diagrams (`generate.go`, `answer.go`, `ground.go`, `prompts/`), the suggestions (`suggest.go`) and
  the commands (`commands.go`)
- `main.go` — the viewer composed into the asset server
- `frontend/src/lib/archify/` · `frontend/src/state/archifyStore.ts` · `archifyDrawStore.ts` ·
  `frontend/src/components/diagram/ArchifyPane.tsx` · `ArchifyRefineDialog.tsx` ·
  `frontend/src/components/codemap/map/AiDiagramDialog.tsx`

---

### ARCHIFY-001 The diagram task, and who may run it
**Implementation**: `backend/ai/routing.go` (`TaskDiagram`) · `backend/ai/operations.go`
(`RequireReadOnly`) · `frontend/src/lib/aiTasks.ts`
**Behaviour**: the AI's diagrams run as their own task, `diagram`, routed like the other ten
(XLANG-004): `ai_provider_diagram` and `{provider}_diagram_model`. Every run of it asks to be held to
reading (AI-057), so it runs only on an engine that can be — Claude Code and Codex. The routing
table and the dialogs' model picker offer only those two for it.
**Inputs / outputs**: none of its own.
**Edge cases**: a setting that routes the task elsewhere — written by hand, or before this rule —
is refused at the run with AI-057's error, never run with write access.
**Frontend dependency**: `TaskRouting`, `ChatModelPicker task="diagram"`.
**Markers**: none

### ARCHIFY-002 Which Archify, and what it can draw
**Implementation**: `backend/archify/cli.go` (`Find`) · `render.go` (`StatusFor`, `InstallCommand`)
**Behaviour**: the install is found as CODEMAP-017 finds it — `ARCHIFY_BIN`, else
`archify/bin/archify.mjs` under the skills folder of any agent `npx skills add -g` installs for
(Claude Code, Codex, opencode, Cursor, Antigravity) or the repository's own — and offered only for
a supported version, 3.1 or a later 3.x. Its status says whether it is installed, its version,
whether it ships the `class` schema, and the command that installs it:
`npx skills add tt-a1i/archify -g`.
**Inputs / outputs**: `archify_status(rootPath)` → `{ installed, version, has_class, install }`.
**Edge cases**: Node is looked up on every call, so a Node installed while the app runs is found.
**Frontend dependency**: `archifyStatus`; `ArchifyPane`'s install notice.
**Markers**: none

### ARCHIFY-003 Checking a document, and drawing it once
**Implementation**: `backend/archify/render.go` (`Validate`, `Render`)
**Behaviour**: **validating** runs `archify validate <type> doc.json --json --quality standard` in a
scratch folder and answers Archify's own report — `{ok, error, diagnostics[]}`; a document Archify
rejects is a report, not an error. The quality is the one every page is drawn at: below it Archify
skips the layout checks `deliver` runs, and a document it validated would then not draw. **Rendering** runs `archify deliver` on a staged copy whose `meta.output` names the page
— `deliver` requires one, a document kept in the repository need not carry it — and keeps the page
in the viewer's folder (`<data>/codemap/archify/view/`) as `<sha256(document, version, root)>.html`.
The same document is drawn once; a new version of Archify or of the document draws it again. A
document that pins a commit is checked against the checkout (`--repo-root`), every cited line
included. Archify runs with the app's `PATH`-only environment (SEC-007) and its update check off.
**Inputs / outputs**: `archify_render(rootPath, path)` → `{ view_url, html_path, report }`;
`report` is empty for a page already drawn. Refusals: a path outside the repository, a file over
2 MiB, `this is not an Archify document` (not JSON, or no `diagram_type` Archify draws), Archify not
installed, Node not found, and Archify's own first complaint.
**Edge cases**: staging and scratch folders are removed on every exit; `deliver`'s receipt never
lands beside the page.
**Frontend dependency**: `archifyRender`.
**Markers**: none

### ARCHIFY-004 The viewer
**Implementation**: `backend/archify/viewer.go` · `main.go` · `frontend/src/components/diagram/ArchifyPane.tsx`
**Behaviour**: the asset server answers `GET`/`HEAD` of `/archify-view/<64 hex>.html` from the
viewer's folder, under a policy of its own, and passes every other request on (SEC-015). The editor
frames the page `sandbox="allow-scripts"` in the app's theme (`?theme=light|dark`): it zooms,
searches and shows its own details, and reaches neither the app nor the network. *Open in the
browser* hands the page to the system's browser — only a page inside the viewer's folder.
**Inputs / outputs**: `archify_open_external(htmlPath)`.
**Edge cases**: `pnpm dev` sets no policy; the rule is checked against `task build`.
**Frontend dependency**: `ArchifyPane`, `ArchifyActions`.
**Markers**: none

### ARCHIFY-005 Documents in the repository, shown by the diagram editor
**Implementation**: `frontend/src/lib/archify/documents.ts` · `save.ts` ·
`frontend/src/state/archifyStore.ts` · `frontend/src/components/diagram/DiagramView.tsx` ·
`backend/diagram/documents.go`
**Behaviour**: a document is saved as `codemap/<slug>-<type>.archify.json` — the slug from its
title, accents dropped, lower case, `-` between words, 60 characters at most, `diagram` when nothing
is left — and **never over another**: a name already taken tries `-2`, `-3`… up to `-20`, and the
file is created before it is written. The diagram editor lists these documents beside its Mermaid
ones (DIAG-002) and opens one in the viewer instead of the canvas. Its bar offers *Adjust with AI*
and its undo (ARCHIFY-008), *Draw it again*, *Open in the browser* and an export: the interactive
page (`.html`, self-contained) or the document (`.archify.json`).
**Inputs / outputs**: the editor's file commands; ARCHIFY-003's render.
**Edge cases**: without Archify the editor shows the install command, with a copy button and *Draw
it again*. A document closed while it was being drawn is not shown.
**Frontend dependency**: `DiagramView`, `ArchifyPane`.
**Markers**: none

### ARCHIFY-006 The class diagram, from the map
**Implementation**: `backend/archify/classdoc.go` · `classroute.go` · `backend/repomap/types.go` ·
`frontend/src/components/codemap/map/MapView.tsx`
**Behaviour**: the «Map» view's *Class diagram* draws the types of the picked card, else of the
open folder, else of the repository — classes, abstract classes, interfaces, enums, records,
mixins, Go structs as classes — with their fields and methods (signatures cut to their parameters
and result), and four relations, the strongest kept when two types have several: **inheritance**
(extends, and an embedded interface), **realization** (implements, Dart's `with`), **composition** (a
field of a drawn type, and a Go struct embedded in another) and **dependency** (a drawn type in a
method's signature). A Go type's methods are gathered from every file of its package.
- **A name means the type its code means.** A folder holds several packages, and they reuse names
  — every one may have a `Deps` and a `Service`. A qualified name (`storage.Store`) means the drawn
  type of that package; an unqualified one means, in Go, the type of its own package and nothing
  else; elsewhere the type in its own folder, else the only drawn type of that name. Two that fit
  equally is no relation rather than a guess.
- **Placement.** Each box sits below what it extends or implements, six to a row before the row
  continues, in whole columns only, inside Archify's 12 × 12 grid. Past 40 types the most used are
  drawn and the subtitle says how many were left out.
- **Routing.** With Archify installed, every relationship is routed along the grid's gaps: it leaves
  its box into the gap above or below it, runs along that gap — and, when rows lie between, up or
  down the vertical gap between two columns — each route on a lane of its own, and enters its target
  from the gap beside it. Archify's own routes do not steer around a third box, and on a crowded
  folder its validator refused some of them for running through one. Where the boxes are is asked of
  Archify's class renderer (`renderers/class/render-class.mjs --layout-json`, on the document without
  its relationships, which do not move a box); where each line meets its box is Archify's, so an end
  its report says leaves the wrong way is moved onto the port it names and the document checked again,
  four times at most. An install without that renderer keeps Archify's own routes.

Citations follow CODEMAP-017's pin: committed files only, three per box at most. The document is
checked by Archify when it is installed — a refused one is never written — saved (ARCHIFY-005) and
opened.
**Inputs / outputs**: `archify_class_diagram(rootPath, scope, language)` →
`{ document, title, types, omitted }`. Refusals: a scope with no types, and Archify's complaint.
**Edge cases**: Go's implicit `implements` is not inferred: no declaration says it, and the index
does not type-check. A build without grammars offers no class diagram.
**Frontend dependency**: `archifyClassDiagram`.
**Markers**: none

### ARCHIFY-007 Diagrams an AI draws by reading the repository
**Implementation**: `backend/archify/generate.go` · `answer.go` · `ground.go` · `prompts/` ·
`frontend/src/components/codemap/map/AiDiagramDialog.tsx` · `frontend/src/state/archifyDrawStore.ts`
**Behaviour**: the «Map» view's *Draw a diagram with AI* asks for one of five kinds —
architecture, workflow, sequence, data flow, lifecycle — about a topic the user types, picks from
the suggestions (ARCHIFY-009) or asked before, or leaves empty for the model to choose. The model
is briefed with this app's own prompt for the kind, the JSON Schema **of the user's own install**
(so the brief matches the version that checks the answer), the map's overview (REPOMAP-010) and the
topic, in the interface's language. It reads the repository read-only (ARCHIFY-010) and answers one
document, which is then made ready:
1. **The document is found** in the answer — the answer itself, else the first balanced `{…}` that
   parses, a fence or a sentence around it ignored.
2. **What is the app's is set**: the diagram type, `schema_version` 1, `meta.output`; a model's
   `meta.repository` is dropped. A sequence message without a `y` is placed 40 below the one before
   it (the first at 160) — a `y` the model gave stays, because segments and activations span it —
   and a data-flow node with a stage and no row takes the next free row of its stage.
3. **Every citation is checked**: a file inside the repository (no absolute path, no `..`, no
   symlink out), regular, with lines inside it — a line past the end is dropped, the file kept —
   committed (CODEMAP-017), three per element at most. With no commit pinned nothing is cited, and a
   document that cites something pins the commit.
4. **Archify validates it** (ARCHIFY-003). What it refuses goes back to the model with Archify's
   own messages and the document — in the same conversation when the engine keeps one, with the
   original brief again when it does not — **twice at most**; a third refusal is the run's error.
The accepted document is saved (ARCHIFY-005) under its own title and opened in the diagram editor.
**Inputs / outputs**: `archify_generate({rootPath, kind, topic, runId, language})` →
`{ document, title, diagram_type, rounds }`, `rounds` counting the repairs. Nothing is written by
the command; the renderer saves what it answers. Refusals: an unknown kind, no engine, Archify not
installed (its schema is the brief), AI-057, and `archify would not draw this diagram: <its
problems>`.
**Edge cases**: one run per repository at a time. The run belongs to the repository, not to the
dialog: closing the dialog leaves it running, reopening it shows the run's log and its stop, and the
diagram opens when it is ready. A stop is not reported as a failure. The topics asked for are kept,
six per kind, in the app setting `archify_recent_topics` (a JSON object of lists), on this machine
only.
**Frontend dependency**: `archifyGenerate`, `AiRunLog`.
**Markers**: none

### ARCHIFY-008 Adjusting a document with AI, and one undo
**Implementation**: `backend/archify/generate.go` (`Refine`) · `frontend/src/state/archifyStore.ts`
(`refine`, `undo`) · `frontend/src/components/diagram/ArchifyRefineDialog.tsx`
**Behaviour**: *Adjust with AI*, on an open document, sends the document and the change asked for
to the diagram task with the same brief, tools and checks as ARCHIFY-007 — the model is told to
change only what is asked and to read whatever it adds. The version Archify accepts replaces the
document on disk and is drawn; the text it replaced is kept, and *Undo the last adjustment* writes
it back. The kinds that can be adjusted are the five of ARCHIFY-007 and the class diagram. It is
offered for a document Archify could not draw, too: the version the model answers is checked at the
quality Archify draws at, so whatever still keeps it from drawing goes back to the model in the
repair rounds of ARCHIFY-007.
**Inputs / outputs**: `archify_refine({rootPath, path, instruction, runId, language})` → the same
shape as ARCHIFY-007; the renderer writes it. Refusals: an empty instruction, a document outside the
repository or of another kind, and ARCHIFY-007's.
**Edge cases**: one undo, for the document open: opening another forgets it. An answer that arrives
after the user opened another document is not written over that one. The dialog stays while the
run goes — it holds the stop — and a refusal is shown in it, the document unchanged.
**Frontend dependency**: `archifyRefine`, `ArchifyActions`.
**Markers**: none

### ARCHIFY-009 Topics suggested by the map
**Implementation**: `backend/archify/suggest.go` · `backend/repomap/types.go` (`DeclsMatching`)
**Behaviour**: up to twelve topics per kind, cheap by design — names the index already holds, and
three files a repository keeps its processes in:
- **sequence**: top-level types and functions named like an entry point — `…Controller`,
  `…Handler`, `…Router`, `…Route(s)`, `…Resolver`, `…Endpoint`, `…Gateway`;
- **lifecycle**: types and type aliases named `…State`, `…Status`, `…Phase`, `…Stage`, `…Step`;
- **data flow**: types named `…Model`, `…Entity`, `…Repository`, `…Repo`, `…Store`, `…Schema`,
  `…Record`, `…Dto`;
- **workflow**: the `package.json` scripts, `.github/workflows/*.yml` and the `Makefile`,
  `Taskfile.yml` or `justfile` targets whose name starts like a process — build, release, deploy,
  publish, migrate, seed, package, ci, test, e2e, lint, start, dev;
- **architecture**: none; it is of the whole repository.
Declarations come the most used first, and never from a test file — `_test.go`, `*.test.ts`,
`*.spec.ts`, `__tests__/`, `test_*.py`, `*Test.java`, a `test/` or `tests/` folder: what a test
declares is about the code, not a part of it. Each topic carries what its chip says, where it comes
from (`path:line` or the file) and the sentence sent as the topic.
**Inputs / outputs**: `archify_suggestions(rootPath, kind)` → `[{ label, hint, topic }]`, `[]` when
there are none.
**Edge cases**: a configuration file over 256 KiB, or not a regular file, is not read.
**Frontend dependency**: `archifySuggestions`.
**Markers**: none

### ARCHIFY-010 What a diagram's run may use
**Implementation**: `backend/archify/generate.go` (`draw`) · `backend/app/registry.go` ·
`backend/codemap/ask.go` (`WriteMCPConfig`)
**Behaviour**: a run works in the repository's folder, read-only and held to it (ARCHIFY-001), with
`Read`, `Grep`, `Glob` and, on Claude Code, the code map's MCP server (`mcp__codemap`,
CODEMAP-015) — this binary, started by the engine with a one-server configuration written for the
run, readable by its owner only, and removed on every exit: an answer, a refusal, a stop. Codex
reads the files itself, with the overview in its brief. Before the run, the app writes its
file index to disk, so that server starts from what the app already knows (REPOMAP-010).
**Inputs / outputs**: none of its own.
**Edge cases**: a binary whose own path the OS cannot give runs without the map's tools. No
credential reaches the run's environment (SEC-007).
**Frontend dependency**: none.
**Markers**: none

## Deliberately not in this version

- **Bundling Archify.** It stays a separate install the user updates.
- **Editing a document by hand in the app.** A document is JSON in the repository — any editor
  changes it, and the editor draws it again on request.
- **Comparing two versions of a diagram.** Git shows the document's change; the drawing is
  Archify's.
- **The map's tools for engines other than Claude Code.** Codex runs the diagram task with the
  overview in its brief and reads the files itself.
