# 16 — Diagram editor

A diagramming tool for business processes and use cases: shapes with text in them, connectors
between them, containers around them — drawn by hand on a canvas, and **stored as Mermaid**.

The format is the point. A diagram here is a `.mmd` file that any Mermaid renderer draws and any
language model reads as what it is — nodes, arrows and labels in text — instead of a private JSON of
coordinates only this app understands. Direct manipulation and a text file are both first-class:
drag a shape and the text is rewritten; type into the text and the canvas follows.

**This document is not a port.** CodeFlow 2.x had no diagram editor, so there is no C# original to
match, no `BUG-*` to preserve and no `AMBIGUOUS-*` to resolve. Every rule below was decided here,
and where a decision could reasonably have gone the other way it says why it did not.

It is deliberately *not* the schema designer (`15-dbml.md`). That one renders what a `.dbml` text
says and is edited only as text. This one is a canvas whose file happens to be text, and the canvas
is the primary way in.

**The rules were written against the JSON format this module shipped with, and the ids are stable
(`00-conventions.md`).** `DIAG-001` … `DIAG-013` keep their numbers and say what they now say;
`DIAG-014` … `DIAG-017` are what the move to Mermaid added.

## Scope

- `backend/diagram/documents.go`, `backend/diagram/commands.go`
- `backend/shared/docwalk/docwalk.go` — shared with `15-dbml.md`, owned here jointly
- `frontend/src/lib/diagram/mermaid/` — `emit.ts` and `parse.ts`, the format
- `frontend/src/lib/diagram/` — the model, the catalogue, the geometry, the layout, the exporters
- `frontend/src/lib/canvas/viewport.ts` — shared with `15-dbml.md`
- `frontend/src/state/diagramStore.ts`
- `frontend/src/components/diagram/`
- `frontend/src/lib/documentPath.ts` — shared with `15-dbml.md`

---

### DIAG-001 A diagram is a file in the project, not a row in the database
**Implementation**: `frontend/src/lib/diagram/model.ts` · `frontend/src/state/diagramStore.ts`
**Behaviour**: A document is `<name>.mmd` inside the open project's folder. It is opened, saved and
created with the file commands that already exist — `read_file_text`, `write_file_text`,
`create_file` — which are path-guarded and work whether or not the folder is a git repository
(`GIT-039`). Nothing about a diagram is stored in `codeflow.db`.
**Inputs / outputs**: none of its own; see `DIAG-002`.
**Edge cases**: a project that is a plain folder works exactly as a repository does.
**Frontend dependency**: the module is `scope: "repo"` in `frontend/src/lib/modules.ts`, so it
follows the selected project and resets when there is none.
**Markers**: none.

**Why not the database.** Storing diagrams as rows would have made them workspace-scoped and
available with no project open, which the API client does for its collections. Files won three
things that outweigh it: a diagram is versioned in git beside the code it describes, it is reviewed
in the same pull request as the change it explains, and a model asked about the repository reads the
diagram without going through this app at all.

### DIAG-002 Finding a folder's diagrams is the only thing the backend does
**Implementation**: `backend/diagram/documents.go` · `backend/diagram/commands.go` ·
`backend/shared/docwalk/docwalk.go`
**Behaviour**: `diagram_list_documents` walks the folder for files whose name ends in `.mmd`,
case-insensitively, and answers them project-relative, sorted, with `/` separators on every
platform. Build and dependency directories are **pruned rather than filtered** — they are never
read — and the walk is bounded at 2 000 documents and 24 levels. A symlinked directory is never
descended into.
**Inputs / outputs**: `diagram_list_documents(rootPath: string)` → `[]string`. A root that is
missing, blank or not a directory answers `no such folder: <path>`. An empty folder answers `[]`,
never `null`.
**Edge cases**: a directory that cannot be read is skipped, not fatal — one unreadable folder must
not cost the user every document in the tree. A cancelled call answers the cancellation rather than
half a listing.
**Frontend dependency**: `frontend/src/lib/ipc/commands.ts` (`diagramListDocuments`).
**Markers**: none.

**`.mmd` is Mermaid's own extension**, not something invented here, which is the whole argument for
it: an editor colours the file, a viewer draws it, a diff reads it, and nothing needs to be told
what it is. It was `.diagram.json` while the editor wrote a private format, and the walk matched a
suffix rather than an extension because `filepath.Ext("checkout.diagram.json")` is `.json`.

**Why `docwalk` exists.** The walk was `backend/dbml/documents.go` and was identical except for the
extension, down to the pruned directories and the refusal message. It moved to `backend/shared/` on
its second caller, not in anticipation of one; `dbml.ListDocuments` keeps its signature and
delegates.

### DIAG-003 The file is Mermaid, and this app reads the subset it writes plus the subset a person writes
**Implementation**: `frontend/src/lib/diagram/mermaid/parse.ts`
**Behaviour**: A document is a Mermaid `flowchart`. Reading one distinguishes two failures. **Not
Mermaid at all** and **a Mermaid diagram of another kind** — a `sequenceDiagram`, a `gantt` — are
refused by reason, and nothing is opened. Anything else opens with what the scanner understood, and
what it could not use is counted so the view can say so before the file is saved over.

What is read:

| Written as | Example |
|---|---|
| Extended shapes | `n1@{ shape: diam, label: "Approved?" }` |
| Classic brackets | `a[Task]`, `b(Rounded)`, `c([Start])`, `d{Decision}`, `e[(Store)]`, `f((Circle))`, `g{{Hexagon}}`, `h[/Input/]`, `i>Flag]` |
| A bare id | a node introduced only by an arrow: `a --> b` |
| Containers | `subgraph n4 [Finance]` … `end`, nested |
| Arrows | `-->`, `---`, `-.->`, `==>`, each with an optional `\|label\|` |
| Fills | `classDef`, `class a,b accent`, and inline `a:::accent` |
| Positions | `%% codeflow: pos <id> <x> <y> <w> <h>` (`DIAG-014`) |

**Inputs / outputs**: `parseMermaid(text)` → `{ ok: true, value: { doc, dropped } }` or
`{ ok: false, reason: "notMermaid" | "notFlowchart" }`. The reason is a discriminated value,
rendered through `translations.ts`, never English prose from the scanner.
**Edge cases**: an edge naming a node that does not exist is dropped with a count, not a refusal. A
shape name Mermaid has and this catalogue does not falls back to a rectangle **and is counted**, so
the user is told before saving flattens it. `graph` is accepted as a synonym of `flowchart`, which
is what older Mermaid and most existing files say.
**Frontend dependency**: `frontend/src/state/diagramStore.ts`, which reports the count as
`diagram.droppedShapes`.
**Markers**: none.

**Why the scanner is written by hand.** Mermaid publishes no API that returns a flowchart's
structure: `mermaid.parse()` answers only "is this valid", `@mermaid-js/parser` does not cover
flowcharts, and `flowDb` is an internal of a Jison grammar. Depending on any of them would tie this
feature to an implementation detail across versions and pull ~2 MB of renderer in for a job that is
a scanner — and the canvas is drawn here anyway, so none of that renderer would be used.

**Why the classic forms are read as well as written.** The editor emits the extended `@{ shape: … }`
syntax, which is unambiguous. Nobody writing by hand uses it, and no model writing Mermaid uses it
either: they write `a[Task] --> b{Decision?}`. Accepting only what we emit would mean pasting real
Mermaid into the text pane produced an empty canvas, which defeats the format.

**Why a shape is counted rather than substituted in silence.** A document naming a stencil this
version does not have could simply be drawn as a rectangle. Drawing it that way and then saving
would turn every such shape in the file into a rectangle permanently, and nothing would have said so.

### DIAG-004 The version marker is a comment, and it is written from the first release
**Implementation**: `frontend/src/lib/diagram/mermaid/emit.ts` (`METADATA_TAG`)
**Behaviour**: Every document this editor writes ends with a `%% codeflow: v1` line. A file without
one is still opened — it is ordinary Mermaid, which is the point — and simply has no positions to
recover (`DIAG-016`).
**Inputs / outputs**: the marker is the first line of the trailing comment block.
**Edge cases**: when a second layout format exists, this is where the migration goes, and there will
be documents to migrate, in git history, because the marker was written from the start.
**Frontend dependency**: none beyond the parser.
**Markers**: none.

### DIAG-005 What is written is ordered, grouped and byte-identical for the same document
**Implementation**: `frontend/src/lib/diagram/mermaid/emit.ts` (`emitMermaid`)
**Behaviour**: The file is generated from the model in a fixed order — header, the `classDef` lines
for the fills **actually used**, node declarations, `subgraph` blocks, grouped `class` lines, edges,
then the comment block. Two spaces of indent, one trailing newline, whole-pixel coordinates. The
same document always produces the same bytes.

Labels are escaped on the way out and unescaped on the way in: `#` → `#35;`, `"` → `#quot;`, a
newline → `<br/>`. Unescaped, a quotation mark in a label produces a file Mermaid cannot parse.

A connector stores no ports. Which sides it leaves and arrives on is derived from where the two
shapes are (`DIAG-017`), because Mermaid has nowhere to write a port and a stored one would go stale
the moment either shape moved.
**Inputs / outputs**: `emitMermaid(doc)` → the exact text written.
**Edge cases**: a document with no nodes is a valid, empty `flowchart TD`.
**Frontend dependency**: none.
**Markers**: none.

**Why it matters more than it looks.** The file lives in a git repository. Emission in map-iteration
order would produce a diff on every save that touched nothing a person did, and a drag that stored
`123.45600000000002` would produce one every time floating-point rounding landed differently.

**Ids are readable and never renumbered.** A node is `n1`, `n2`… assigned when it is created and
kept for the life of the document, so inserting a shape does not rewrite half the file in the diff.

### DIAG-006 The catalogue is Mermaid's, and its geometry has one definition
**Implementation**: `frontend/src/lib/diagram/stencils.ts` · `frontend/src/lib/diagram/shapes.ts` ·
`frontend/src/lib/diagram/palette.ts` · `frontend/src/lib/diagram/markers.ts` ·
`frontend/src/lib/diagram/routing.ts`
**Behaviour**: 17 stencils in three groups — basic, flowchart, container — and **a stencil's id is
its Mermaid shape name**: `rect`, `rounded`, `stadium`, `diam`, `lean-r`, `div-rect`, `cyl`, `doc`,
`brace`, `text`, `circle`, `dbl-circ`, `fr-circ`, `sm-circ`, `hex`, `person`, and `subgraph` for the
container. There is no translation table to keep in step, because there is nothing to translate.

Four connector kinds, which are Mermaid's: `arrow` (`-->`), `line` (`---`), `dotted` (`-.->`) and
`thick` (`==>`), each with an optional label.

A stencil holds its default size, its default fill, where its text goes, whether it is a container
and whether it keeps its proportions when resized. `shapeElements(kind, width, height)` is **the
only description of a shape's outline in the app**, and the canvas and the SVG exporter both render
it; the same is true of the three arrowheads and of the connector routing.

A node stores its `kind`, never an appearance, and its fill as a **token** (`accent`), never a
colour. `palette.ts` resolves a token per theme.
**Inputs / outputs**: pure functions; `shapeElements` answers a list of rects, ellipses, paths and
lines, each marked `body` (carries the fill) or `detail` (outline only, drawn on top).
**Edge cases**: every stencil is asserted drawable at 1×1 and 2000×12 without producing `NaN`,
because `NaN` in a path attribute is a shape that silently does not render.
**Frontend dependency**: `components/diagram/ShapeGlyph.tsx`, `DiagramCanvas.tsx`.
**Markers**: none.

**The property this buys.** "What I exported is not what I drew" is not a class of bug that exists
here: there is nothing to keep in step.

**What the catalogue lost, and why that was the right trade.** It was 30 stencils across four
notations, including BPMN's three gateways and UML's generalization. Mermaid expresses one diamond,
not three, and no hollow-triangle arrowhead. Keeping them would have meant encoding them in comments
the way positions are — private information in a file whose whole value is that it is not private.
The exclusive, parallel and inclusive gateways therefore collapse to one `diam`, generalization
becomes a labelled arrow, and ellipse and use-case become `stadium`. A `.diagram.json` imported from
before the change is translated (`DIAG-010`) and says how much it lost.

### DIAG-007 A container carries what is inside it
**Implementation**: `frontend/src/lib/diagram/edits.ts`
**Behaviour**: A `subgraph` is a container. A shape dropped on one becomes its child and is stored
relative to it, so dragging the container moves its contents; dropping a shape on bare canvas takes
it out again. The position is rewritten between the two frames as it moves, so nothing jumps at the
moment it is released. The **innermost** container wins — a subgraph inside a subgraph is what a
task dropped on it belongs to.

Nothing can be put inside itself or inside something it holds. **Deleting a container deletes its
contents**, and deleting a shape deletes the connectors on either end of it.
**Inputs / outputs**: pure functions over the document.
**Edge cases**: a parent chain that forms a cycle — which a hand-edited file can contain —
terminates rather than looping, both when reading and when ordering for the canvas. A container is
drawn before what it holds, or it paints over it.
**Frontend dependency**: `components/diagram/DiagramCanvas.tsx`.
**Markers**: none.

**Deleting the contents is the destructive choice, and it is deliberate.** A pool *is* the tasks in
it, and leaving them behind as a heap of orphans is not what anybody meant. Undo restores the
container and everything in it in one step, which is what makes it defensible (`DIAG-008`).

### DIAG-008 Undo is the whole document, and one gesture is one step
**Implementation**: `frontend/src/lib/diagram/history.ts` · `frontend/src/state/diagramStore.ts`
**Behaviour**: The store keeps past, present and future documents, capped at 100 steps. Every change
goes through `edit`, so there is no way to change a diagram that does not pass the stack. The
continuous half of a gesture — every frame of a drag or a resize, every letter typed into a label —
**amends** the present instead of pushing a step; the frame where the gesture ends commits. A new
edit made after an undo discards the redo branch.

An undo that lands back on what is on disk **leaves the document dirty**. The write that would
reconcile them has not happened, and claiming otherwise loses the work at the next document switch.
**Inputs / outputs**: `undo`/`redo` on the store; `⌘Z` and `⇧⌘Z` on the window, ignored while a text
field has focus so that undo inside a label is the field's own.
**Edge cases**: opening another document starts a fresh history — undo cannot reach across
documents. Typing in the text pane amends rather than pushing, so undo there is the editor's own.
**Frontend dependency**: `components/diagram/DiagramView.tsx`.
**Markers**: none.

**Snapshots, not inverse operations.** A document is a few hundred small objects and a hundred of
them is nothing beside Monaco in the same window. The alternative needs an inverse per kind of edit,
and the one nobody wrote is discovered by losing work.

### DIAG-009 Saving is explicit
**Implementation**: `frontend/src/state/diagramStore.ts` (`save`)
**Behaviour**: `⌘S` or the toolbar button writes the document; the toolbar shows whether there are
unsaved changes. Switching to another document with unsaved changes asks first. A write that fails
is reported and the document stays dirty. An edit made **while** the write is in flight leaves the
document dirty, because the text that was written is compared against the document that was written,
not against the current one.
**Inputs / outputs**: `write_file_text(repoPath, relPath, contents)`, with the text buffer as it
stands (`DIAG-015`).
**Edge cases**: saving a clean document does nothing at all.
**Frontend dependency**: `components/diagram/DiagramView.tsx`.
**Markers**: none.

**Why not autosave.** Every other diagram tool autosaves, and every other diagram tool stores its
documents in its own database. These are files in the user's repository: a debounced write on every
drag would mean a working tree that changes while someone reads a diagram, and `git status` noise
they did not ask for. The schema designer makes the same choice for the same reason.

### DIAG-010 Exporting produces a picture drawn from the document
**Implementation**: `frontend/src/lib/diagram/toSvg.ts` · `frontend/src/lib/diagram/toPng.ts` ·
`frontend/src/lib/diagram/exportFile.ts` · `frontend/src/lib/diagram/serialize.ts` ·
`frontend/src/lib/diagram/text.ts`
**Behaviour**: Three formats — SVG, PNG and JSON — written through the native save dialog and
`write_file_bytes`. **No new backend command.** Mermaid itself is not an export format: it is what
the document already is, on disk, and there is nothing to convert.

The SVG is generated from the document by a pure function, not captured from the canvas: same
shapes, same routing, same arrowheads. The PNG is that SVG rasterised at 2×. Both are drawn in the
**light** palette whatever the app is set to, because an exported diagram lands on a white page — a
ticket, a document, a chat.

JSON is the interchange format, and it is also the **migration path**: a `.diagram.json` written
before the format changed is read through the same code, its stencils and connectors translated to
the Mermaid catalogue. A translation that loses information — the three BPMN gateways, UML
generalization — is counted and reported; a pure rename is not, because nothing was lost.
**Inputs / outputs**: `exportDiagram(doc, baseName, format)` → the path written, or `null` when the
dialog was dismissed. `parseDocument(text)` → `{ ok, value: { doc, dropped } }` or a reason.
**Edge cases**: an empty document exports a blank page rather than failing. A label containing `&`
or `<` is escaped; unescaped, it would produce a file no parser opens.
**Frontend dependency**: `apiPickFile`/`apiReadTextFile` for the import side.
**Markers**: none.

**The intermediate image must be a `data:` URL.** The production CSP (`frontend/vite.config.ts`)
allows `img-src 'self' data:` and does not allow `blob:`, so `URL.createObjectURL` works in `pnpm
dev`, where no policy is injected, and fails in the packaged app.

**SVG text does not wrap**, so `text.ts` breaks labels into lines against an estimated glyph width
rather than a measured one. The estimate errs wide, so a label may break one word earlier than the
canvas does. `<foreignObject>` would wrap natively and was rejected twice over: half the tools
people open an SVG in ignore it, and rasterising one through a canvas is blocked or blank depending
on the engine.

**The exported SVG does not embed its font.** Opened elsewhere it falls back to the reader's own
sans-serif. Embedding is a local change to `toSvg.ts` if it turns out to matter.

### DIAG-011 An import creates a document and never overwrites the open one
**Implementation**: `frontend/src/components/diagram/DiagramView.tsx` ·
`frontend/src/components/diagram/NewDiagramModal.tsx`
**Behaviour**: Importing picks a `.json` file anywhere, validates it through `DIAG-010`, and then
asks for a name — the same dialog "new diagram" uses. The document that was open stays open and
untouched until the new one is created.
**Inputs / outputs**: `apiPickFile(["json"])`, `apiReadTextFile(path)`.
**Edge cases**: a file that is not a diagram is refused with its reason and nothing is created.
**Frontend dependency**: none beyond the above.
**Markers**: none. Same rule as `DBML-022`, for the same reason: "import" and "replace what I am
looking at" are not the same instruction.

**There is no Mermaid import**, because there is nothing to import: a `.mmd` anywhere in the project
is already in the picker, and a Mermaid diagram from elsewhere is pasted into the text pane.

### DIAG-012 Shapes are placed by clicking the palette, not by dragging from it
**Implementation**: `frontend/src/components/diagram/DiagramPalette.tsx` ·
`frontend/src/components/diagram/DiagramView.tsx` (`place`)
**Behaviour**: Clicking a stencil puts it in the middle of the current view and selects it, ready to
be dragged where it belongs and typed into.
**Inputs / outputs**: none.
**Edge cases**: none.
**Frontend dependency**: `DiagramCanvas.centreOfView`.
**Markers**: none.

**Why not drag from the palette.** HTML5 drag-and-drop is swallowed by a native webview drag handler
in this app — the reason `lib/pointerDrag.ts` exists and the reason the editor's tabs and the file
tree hand-roll their drags. A palette whose only way in is a gesture that silently does nothing on
one of the two platforms is worse than a click. Pointer-based drag-to-place is a possible addition;
it is not what v1 depends on.

### DIAG-013 Text is readable on every fill, in both themes
**Implementation**: `frontend/src/lib/diagram/palette.ts` · `frontend/src/lib/ui/contrast.ts`
**Behaviour**: Six fill tokens, each with its own outline and text colour per theme. Every pairing
meets WCAG AA (4.5:1) for its text and stays at least 1.6:1 for its outline, measured in
`palette.test.ts` with the same `contrastRatio` the accent options are checked with. A token is
written into the file as a `classDef` whose colours are the **light** variant, so a viewer that is
not this app draws the diagram the way the export does.
**Inputs / outputs**: `diagramPalette(theme)`.
**Edge cases**: `none` has no fill, so its text is checked against the canvas background — which is
what it is actually written on.
**Frontend dependency**: `useThemeStore(s => s.resolved)`.
**Markers**: none.

### DIAG-014 Positions ride in comments, because Mermaid has none
**Implementation**: `frontend/src/lib/diagram/mermaid/emit.ts` ·
`frontend/src/lib/diagram/mermaid/parse.ts`
**Behaviour**: Mermaid describes a graph, not a drawing: it has no coordinates, and a renderer lays
one out with its own algorithm. The arrangement a person dragged into place is written as one
comment per node at the **end** of the file, after the diagram:

```
%% codeflow: v1
%% codeflow: title Checkout
%% codeflow: pos n1 40 40 140 56
%% codeflow: pos n2 40 160 160 72
```

Coordinates are **absolute**, including a node inside a container, so a line can be read on its own.
The model stores a child's position relative to its container and converts on both sides.
**Inputs / outputs**: `<id> <x> <y> <width> <height>`, whole pixels, `x`/`y` signed.
**Edge cases**: a `pos` line for a node that does not exist is ignored. A node with no `pos` line is
laid out (`DIAG-016`).
**Frontend dependency**: none.
**Markers**: none.

**Why the end of the file and not interleaved.** The Mermaid above has to read as one block: it is
what a person opens the file to read and what a model is given. Metadata between the statements
would make every second line noise.

**The trade this accepts.** A diagram opened in any other Mermaid viewer is drawn by that viewer's
layout, not the one that was dragged. That is the cost of the file being Mermaid rather than a
drawing format, and it is the right way round — the content survives everywhere, the arrangement
survives here.

### DIAG-015 The text pane is Mermaid, and the model is what the canvas draws
**Implementation**: `frontend/src/components/diagram/DiagramView.tsx` ·
`frontend/src/state/diagramStore.ts` (`setSource`, `edit`) · `frontend/src/lib/monacoSetup.ts`
**Behaviour**: The view is three panes — palette, canvas, Mermaid — with the text pane resizable and
collapsible exactly as the schema designer's is (`DBML-008`): the width is persisted in
`useLayoutStore`, the collapsed state is local and deliberately not.

The text is reparsed on every keystroke. **The canvas keeps the last model that parsed**: half-way
through typing, text is invalid more often than valid, and tearing the drawing down on each key
would throw away the zoom and the selection. The failure is shown in a band over the previous
drawing.

While the text does not parse, **every edit from the canvas is refused** (`editable` is false).
There is no dragging against a model that is no longer what the text says.

The two directions are not symmetric, and the asymmetry is the rule:

- **A canvas gesture regenerates the whole file** from the model (`DIAG-005`) and replaces the
  buffer. That is what "the model is authoritative" means.
- **Typing amends the model and leaves the text exactly as typed.** The buffer is what is saved, so
  hand-written formatting, comments and statement order survive until the next canvas gesture
  rewrites them.

**Inputs / outputs**: `setSource(text)` parses and amends; `edit(change)` commits and re-emits.
**Edge cases**: a node typed into the text with no `pos` comment is placed by `DIAG-016`, and only
acquires a `pos` line when the file is next regenerated.
**Frontend dependency**: Monaco, with a Mermaid language registered in `monacoSetup.ts` — comments,
keywords, arrows and the `@{ … }` attribute form. Monaco ships no Mermaid grammar, and unlike the
schema designer, which borrows `sql`, there is nothing close enough to borrow.
**Markers**: none.

### DIAG-016 A file with no positions is laid out, not stacked
**Implementation**: `frontend/src/lib/diagram/layout.ts` (`placeUnpositioned`)
**Behaviour**: A node the file gave no `pos` comment for is placed by a layered top-down layout:
ranks follow the arrows, a rank is a row, rows are centred on each other, and the result is measured
so nothing overlaps. Nodes the file **did** position are never moved, and new ones are placed below
the existing drawing. Children of a container are laid out inside it, and a container the file did
not size grows to hold them.
**Inputs / outputs**: `placeUnpositioned(nodes, edges, positioned)`, mutating nodes the caller has
just built. Coordinates are absolute and whole-pixel.
**Edge cases**: a cycle terminates — its back edge simply points up the page — as does a self-edge
and a parent chain naming a node that does not exist.
**Frontend dependency**: none; the parser calls it.
**Markers**: none.

**This is the rule that makes the format worth having.** The file a model writes has no `pos`
comments in it at all, and neither does anything pasted from a Mermaid document. Without a layout
they all arrive as a pile of boxes on the origin, and "paste Mermaid, see a diagram" — the reason
the format changed — would not work. It is not a replacement for Mermaid's layout engine and does
not try to be: no edge-crossing reduction, no splines. It produces something readable that a person
then drags into the shape they wanted, and the moment they do, the position becomes a comment and
the layout never touches that node again.

### DIAG-017 The canvas is this app's own, and its geometry is pure
**Implementation**: `frontend/src/components/diagram/DiagramCanvas.tsx` ·
`frontend/src/lib/canvas/viewport.ts` · `frontend/src/lib/diagram/picking.ts` ·
`frontend/src/lib/diagram/resize.ts` · `frontend/src/lib/diagram/routing.ts` ·
`frontend/src/lib/pointerDrag.ts`
**Behaviour**: Shapes are absolutely-positioned elements inside a transformed wrapper, with one SVG
overlay above them for the connectors — the same construction as the schema designer's canvas. One
pointer gesture at a time, as a discriminated state: pan, marquee, move, resize, connect.

Connecting is a drag from one of a shape's four ports, revealed on hover, to another shape; the
sides each end attaches to are **chosen from how the two boxes lie**, not stored (`DIAG-005`), and
the route is orthogonal. Selection is click, shift-click and marquee; resize is eight handles, with
⇧ or a fixed-ratio stencil keeping proportions; the wheel with ⌘ zooms about the pointer;
double-click edits a label.
**Inputs / outputs**: `viewport.ts`, `picking.ts`, `resize.ts` and `routing.ts` are pure and tested
in node — no DOM.
**Edge cases**: a zoom listener must be registered non-passively, or the browser scrolls the page
instead. Ports sit above the resize handles, or the handle swallows the press. A marquee dragged in
the empty middle of a container selects what is in it, not the container.
**Frontend dependency**: none.
**Markers**: none.

**Why not a library, and what replaced it.** This editor was built on React Flow and **connectors
never worked in it**: React Flow never ran its node-measurement pass inside WKWebView, so every
node's `handleBounds` stayed `null` — nodes with no ports, as far as its connection logic was
concerned. It was not this app's node component; a stock React Flow node, side by side, was equally
unmeasured. Eight hypotheses were tested in the running window against a probe reading
`useStore(s => s.nodeLookup)` — the observer, the stylesheet, `isConnectable`, the resizer's
handles, `connectionRadius`, `selectionOnDrag`, the event kind, and every way of sizing a node —
and every one was ruled out. The remaining question was inside `NodeWrapper`'s measurement effect.

Rewriting the canvas retired that question rather than answering it. The geometry was already
here — pure and tested — because the exporter needed it; what the library was providing was event
handling over that geometry. Connecting worked in the first build of the replacement. The dependency
is gone, and with it 2.3 MB, `flow.ts`, `ShapeNode.tsx`, `ShapeEdge.tsx` and `canvasContext.ts`.

---

## Deliberately not in this version

| Not built | Why |
|---|---|
| Mermaid diagram types other than `flowchart` | A sequence diagram or a gantt is a different canvas, not a different stencil. `usecase-beta` is worth revisiting when it stops being beta. |
| Layers | The container nesting covers what layers were wanted for here. |
| Templates | Worth doing once there are diagrams to learn the common shapes from. |
| An AI assistant over the diagram | Less necessary now than it was: the document *is* text a model can read and rewrite, so the general assistant reaches it without a feature here. |
| draw.io XML compatibility | Would pin the model to another tool's format. Mermaid is the interchange format; the JSON export covers moving a diagram between projects. |
| Waypoints on a connector | Mermaid cannot express them, so they would have to live in comments like positions do. The orthogonal routing handles what has come up. |
| Drag-to-place from the palette | `DIAG-012`. |
