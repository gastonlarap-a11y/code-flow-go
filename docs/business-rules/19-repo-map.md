# 19 — Repository map

The code map's drawn view: every code file of the open repository, what each declares — classes,
interfaces, functions, methods, their fields and what they extend — what it imports, and which other
files use what it declares. The view draws the project, or one folder, as cards joined by their
imports; the side panel lists a file's declarations, the uses of a name and what is used most.

**This document is not a port.** CodeFlow 2.x had no such view, so there is no C# original, no
`BUG-*` to preserve and no `AMBIGUOUS-*` to resolve. Every rule below was decided here.

**It is the code map's second index, and a different kind from the first** (`18-code-map.md`).
That one type-checks Go and TypeScript into a symbol graph — exact, and only with go and node
installed. This one reads any repository with nothing installed: six languages through tree-sitter
grammars linked into the binary, every other code file as a file. Its unit is the file: a
declaration is found by name, and a use is **confirmed** when the using file imports the declaring
one — or shares its package — rather than type-checked.

## Scope

- `backend/repomap/` — listing, reading, resolving imports, the graph, the questions, the cache and
  the six commands
- `backend/repomap/queries/` — this repository's own tree-sitter queries, one folder per language
- `backend/git/attrs.go` — `git check-attr`, for the files `.gitattributes` marks generated
- `backend/app/smoketest.go` — the `parser` probe (REPOMAP-003)
- `THIRD-PARTY-NOTICES.md` — the MIT notices of the linked runtime and grammars

---

### REPOMAP-001 One index per repository, of files rather than symbols
**Implementation**: `backend/repomap/facts.go` · `snapshot.go` · `service.go`
**Behaviour**: each file is read into its *facts* — language, line count, declarations, imports and
the names it mentions — which depend only on its content. A *snapshot* resolves every file's imports
into who imports whom, and indexes declarations and mentions by name. Snapshots are immutable;
asking again after a change builds a new one from the facts that did not change plus the files that
did.
**Inputs / outputs**: none of its own; the commands are REPOMAP-007.
**Edge cases**: two declarations of one name are two entries; which one a mention means is
REPOMAP-006's question.
**Frontend dependency**: the «Map» view of the code map module.
**Markers**: none

### REPOMAP-002 Which files are the repository's code
**Implementation**: `backend/repomap/list.go` · `backend/git/attrs.go` (`CheckAttrs`)
**Behaviour**: in a git work tree, one `git ls-files -z --cached --others --exclude-standard` —
tracked files and untracked ones git does not ignore, which is what a person means by "the code" —
then one `git check-attr -z --stdin linguist-generated linguist-vendored` for the whole batch: a file
either attribute marks is not the repository's own (the rule GitHub's language statistics follow).
Only regular files count: a path deleted from disk but still in the index, a symlink, a submodule
or an untracked nested repository is skipped. Outside git, the folder is walked, pruning the
directories `shared/docwalk` never reads (`node_modules`, `dist`, `.venv`…).
A file is code when its extension names a language the map knows (REPOMAP-003). Then it is skipped
when it is over 1 MiB, has a NUL in its first 8 KiB, or is minified — `.min.` in its name, a
`.bundle.js`, or lines averaging over 500 bytes. A repository with more than 20 000 code files is
mapped by its first 20 000 in path order, and `Build.truncated` says how many were left out.
Beside the code, the listing keeps the manifests the resolver reads (REPOMAP-004).
**Inputs / outputs**: `listing{files, manifests, truncated, git}`.
**Edge cases**: `check-attr` failing — no git — reads as "nothing marked", so the map shows a little
too much rather than hiding files. A file read and found not to be code is remembered as such and
not read again until it changes.
**Frontend dependency**: `RepoMapBuild.git`, `RepoMapBuild.truncated`.
**Markers**: none

### REPOMAP-003 Reading a file: six grammars, this repository's queries
**Implementation**: `backend/repomap/parse_treesitter.go` (cgo) · `parse_plain.go` (no cgo) ·
`extract.go` · `queries/<language>/{decls,imports,refs}.scm` · `probe.go`
**Behaviour**: TypeScript and TSX, JavaScript and JSX, Go, Python, Java and Dart are parsed with
their official tree-sitter grammars, linked into the binary:

| Language | Grammar | Version |
|---|---|---|
| TypeScript, TSX | `tree-sitter/tree-sitter-typescript` | v0.23.2 |
| JavaScript, JSX | `tree-sitter/tree-sitter-javascript` | v0.25.0 |
| Go | `tree-sitter/tree-sitter-go` | v0.25.0 |
| Python | `tree-sitter/tree-sitter-python` | v0.25.0 |
| Java | `tree-sitter/tree-sitter-java` | v0.23.5 |
| Dart | `UserNobody14/tree-sitter-dart` | commit `be07cf7118d3`, the last one more than a week old when it was pinned — it has no tags |

Kotlin, C#, Rust, PHP, Ruby, Swift, C, C++, Scala, Vue, Svelte, SQL and shell files are on the map
too, with their lines; their declarations and imports are unknown (`parsed: false`).

What is read from each tree is decided by **this repository's own queries**: `decls.scm` captures each
declaration under the name of its kind (`@class`, `@interface`, `@enum`, `@struct`, `@record`,
`@mixin`, `@type`, `@function`, `@method`, `@const`, `@var`) with its `@name`; `imports.scm` captures
what is imported (and, for Go and Java, the `@package`); `refs.scm` captures every identifier. When two
patterns match one node, the one listed first wins — a Go struct is a struct before it is a type, and
a TypeScript `let` a variable before it is a constant. A constant is a Go `const`, a TypeScript or
JavaScript `const`, an upper-case Python module name; a variable is a Go `var` or a `let`.
Declarations nest by their byte ranges: a method's parent is the class it is written inside, a
function inside a class is a method (a Python `def`, a Dart function). A Go method's `owner` is its
receiver's type, its parent only when that type is declared in the same file. A declaration's lines
start at its decorators, or at the `export` / `const` statement that declares it — except one of a
parenthesised Go block (`const (`, `var (`, `type (`), which starts at its own line, its signature
written as itself after its keyword (`const maxItems = 40`); a Dart function's
body is the node after its signature, so its lines run to the end of that body. Its signature is its
head on one line, from where it starts to where its body does, at most 160 characters.

What a query cannot say well is read from the declaration's node by the language's own reader: a
type's fields with their types, what it extends (`bases`) and implements (`implements`; a Dart mixin
counts as one), visibility (an access modifier where the language has one; Go by capitalisation;
Python and Dart by leading underscores), `static`, and `abstract`. A Python constant is a
module-level assignment whose name is upper case. Mentions are each distinct identifier of at least
two characters, at its first line, at most 3 000 a file.
**Inputs / outputs**: `Facts{lang, lines, parsed, package, decls, imports, refs}`.
**Edge cases**: code that does not parse still yields what does. A parse that panics is reported and
its file recorded as not code; the worker starts a new parser and carries on. A build compiled
without cgo — the cross-compile check and the cross-platform lint — reads no file (`GrammarsLinked`
false, `Build.grammars` false). A release must have cgo, which the `parser` smoke probe checks on
every packaged binary before it is published: it parses a one-class snippet and fails a build with no
grammars (`ErrNoGrammars`). Each worker owns its parser, query cursor and compiled queries; none is
shared between goroutines.
**Frontend dependency**: `RepoMapFileDetail.parsed`, `RepoMapBuild.grammars`.
**Markers**: none. The Dart grammar is pinned before its support for Dart 3.12 and 3.13 syntax
(primary and concise constructors), which a file using them parses partially until it is bumped.

### REPOMAP-004 Imports resolve by the repository's own rules
**Implementation**: `backend/repomap/resolve.go` · `manifests.go`
**Behaviour**: each language resolves with its own rules and the repository's manifests, never by
matching names:

| Language | A specifier resolves to |
|---|---|
| TypeScript / JavaScript | a relative path, tried with `.ts .tsx .d.ts .js .jsx .mjs .cjs .mts .cts` and as a folder's `index` (and `./x.js` as the source `./x.ts`); else the nearest `tsconfig.json` / `jsconfig.json`'s `paths` aliases and `baseUrl`, following `extends` inside the repository; else another package of the repository by the `name` in its `package.json` (its path, or its `src/`) |
| Go | the module of the `go.mod` whose path the import starts with — the longest one, so a nested module wins — and the non-test `.go` files of that folder |
| Python | relative imports by their dots; absolute ones from the repository, `src/`, and every folder with a `pyproject.toml`, `setup.py` or `setup.cfg` (and its `src/`), as `a/b.py` or `a/b/__init__.py`, shortened from the right until a module answers (`a.b.Class` is `a/b.py`) |
| Java | `a.b.Type` to the file named `Type.java` declaring `package a.b`; `a.b.*` to every file of the package; a static member to its type's file |
| Dart | `package:name/…` to `<folder>/lib/…` of the `pubspec.yaml` whose `name` it is; a relative path to the file |

What does not resolve to a file is a package from outside the repository, recorded under its name
(`react`, `@scope/pkg`, `net/http`, `github.com/owner/repo`, `requests`, `java.util`, `flutter`,
`dart:async`), or **unknown** — an import that should be in the repository and is not there. An
import of a stylesheet, an image or JSON has nothing to follow.
**Inputs / outputs**: a file's `imports` (sorted file indexes), `external` (sorted names) and whether
it *knows* its imports — all resolved to a file or to a package.
**Edge cases**: a tsconfig's comments and trailing commas are accepted, outside strings only. An
`extends` chain stops after eight steps, which also breaks a cycle. A manifest over 256 KiB is not
read. A Java import under one of the repository's own package roots that names no file is unknown,
not a library.
**Frontend dependency**: `RepoMapFileDetail.imports` / `imported_by` / `external`.
**Markers**: none

### REPOMAP-005 The project and a folder, as cards joined by imports
**Implementation**: `backend/repomap/graph.go`
**Behaviour**: the **folder view** shows one folder's direct contents: each subfolder as one node
holding everything below it, and each file directly inside. A folder holding nothing but one
subfolder opens that subfolder instead, as many times as it takes. The **project view** shows every
file when there are at most 120; past that, files are folded into their folder cut at the deepest
level that still fits in 120 nodes, files at the root staying their own nodes. In both, an edge
counts the file-to-file imports from one node into another; imports inside one node are not drawn.
A node counts its files, declarations and lines, the language most of its files are written in (the
first by name on a tie), and the files **outside it** that import something in it — from outside the
folder shown too. Its group is its top-level folder, `""` for a file at the root.
**Inputs / outputs**: `MapGraph{focus, nodes, edges, files, declarations, languages}`; `files`,
`declarations` and `languages` describe the whole repository whatever the focus.
**Edge cases**: a focus outside every file answers no nodes. Nodes are sorted by path and edges by
their two ends, so the same repository answers the same graph.
**Frontend dependency**: the «Map» view's canvas and its two modes.
**Markers**: none

### REPOMAP-006 A file, a name, its uses, and what is used most
**Implementation**: `backend/repomap/query.go` · `snapshot.go` (`sees`, `samePackage`)
**Behaviour**:
- **A file** answers its declarations in document order with their depth, its imports, the files
  importing it, and its packages from outside. A path is matched exactly, or by a unique trailing
  part.
- **A search** reads `save`, `save()`, `Cart.total` or `Cart::total` (a member, by its owner's name)
  and ranks exact names first, then the same name in another case, then a prefix, then — for three
  characters or more — a substring; among equals, the most used first. At most 30.
- **The uses of a name** — of the declarations in one file when a path is given — are the other files
  that mention it. One that imports a declaring file, or shares its package (a Go folder, a Java
  package), is a *confirmed* use. One that does not is kept as a use *by name only*, unless its own
  imports are all known and in the same language family: then it means another declaration of the
  name, and it is counted as `excluded` rather than listed. Each use says the line it is first
  mentioned on and the signature of the declaration it sits inside. Confirmed first; at most 200.
- **The most used** are top-level declarations other than methods, at least three characters long
  and not a name too common to mean one thing (`main`, `get`, `data`, `props`, `handler`…), ranked
  by how many other files use them, confirmed. At most 40.
**Inputs / outputs**: `FileDetail`, `DeclHit[]`, `UsageReport{declarations, usages, excluded}`.
**Edge cases**: TypeScript, TSX and JavaScript are one family; a use across families is never
excluded. A name a file declares itself is not a use of another file's.
**Frontend dependency**: the «Map» view's side panel.
**Markers**: none

### REPOMAP-007 Six commands, none needing the database
**Implementation**: `backend/repomap/commands.go` · `wire.go` · `backend/app/registry.go`
**Behaviour**: `repomap_project`, `repomap_folder`, `repomap_file`, `repomap_search`, `repomap_usages`
and `repomap_most_used`, each taking the repository's folder as `rootPath`
(`01-ipc-surface.md`). They share one service, registered outside the storage stage, so they
survive a start-up whose database failed.
**Inputs / outputs**: snake_case JSON, no slice ever `null`; `repomap_file` answers `null` for a file
the map does not hold.
**Edge cases**: a root that is not a folder answers `not a folder on this machine`.
**Frontend dependency**: `repomapProject`, `repomapFolder`, `repomapFile`, `repomapSearch`,
`repomapUsages`, `repomapMostUsed` (`frontend/src/lib/ipc/commands.ts`).
**Markers**: none

### REPOMAP-008 Kept current by stat, kept on disk by content
**Implementation**: `backend/repomap/service.go` · `cache.go`
**Behaviour**: a map looks for changes at most every 2 s, the burst of a search box's keystrokes
being one look: it lists the files again and reads only those whose size or modification time
changed, on up to eight workers. A file's facts are filed under the SHA-256 of its content, so a
rename or a revert costs no parse. A snapshot is rebuilt only when a file was added, removed or
changed, or a manifest was. The map is kept on disk at
`<data>/codemap/repomap/<sha256 of the root, 24 hex>.json.gz` — written at most every 15 s, at once
on `Flush`, through a temporary file renamed over the old one — and read back on the first question
after a start, so a second start reads only what changed. A cache another extractor wrote (another
build mode, other grammars, other queries) is not read. Four repositories keep a map in memory; the
least recently asked about is written and dropped first.
**Inputs / outputs**: `Build{files, parsed, millis, grammars, truncated, git}` beside every graph.
**Edge cases**: the MCP server, another process, reads the cache and never writes one (CODEMAP-015).
A cache that cannot be written is only a slower next start; the question still answers.
**Frontend dependency**: `RepoMapBuild`.
**Markers**: none

### REPOMAP-009 The «Map» view
**Implementation**: `frontend/src/components/codemap/map/MapView.tsx` · `MapCanvas.tsx` ·
`MapPanel.tsx` · `useAnswer.ts` · `frontend/src/lib/repomap/layout.ts` · `labels.ts` ·
`frontend/src/state/repomapStore.ts`
**Behaviour**: the code map module opens on this view. Its bar switches between **Whole project**
and **By folder**, shows the open folder's path (each part opens it), the repository's counts — its
languages on hover — a search over declarations, and a refresh.
- **The canvas** lays the cards out in columns, importers to the left of what they import: cycles
  broken by a depth-first walk, a column per longest import chain, each column ordered by the
  average height of its neighbours over six alternating passes, folders and the most imported first
  on a tie, and a column over twelve cards folded into the next. Lines run from the right of the
  importer to the left of the imported card, with an arrowhead, thicker for more imports. Hovering a
  card lights its lines and its neighbours and dims the rest, for 70 ms after the pointer leaves;
  a picked card stays lit. The whole-project view colours each card by its top-level folder — the
  biggest folder the theme's accent — with a legend. Wheel pans, ⌘/Ctrl+wheel zooms at the
  pointer, dragging the background pans, and the corner buttons zoom and fit. The view fits itself
  whenever the folder or the mode changes — once the view for it has arrived, since until then the
  last one stays drawn as what it was — not on a refresh.
- **A click** picks a card: a file opens its outline in the panel, a folder shows its summary with
  *Open this folder*. **A double-click** — or Enter — opens a folder in the folder view (from the
  whole-project view too) and a file in the editor at its first line. A click on the background
  lets go of the picked card.
- **The panel** opens on what is used most. A file shows its declarations as an indented outline —
  each row opens the editor at its line, and its signature on hover — then what it imports, what
  imports it, and its packages; a language the map does not parse says so instead. A search (from
  two characters, after 200 ms, or at once on Enter; Escape clears it) lists declarations as
  `Owner.name`, kind, `path:line` and how many files use them. *Where it's used* lists the
  declarations of the name and the files using them, a use by name only marked so, and how many
  files mentioning a different declaration were left out. Every screen but the first has a way back.
- **A folder view draws at most 30 cards** — folders first, then the most imported; the rest wait
  behind a *N more* chip that lists them in the panel.
- **The states**: indexing, a folder that cannot be read (with the reason), a repository with no code
  to draw, a build without grammars, and code files past the bound — the last two said in a band
  over the canvas.
- The way each repository was last drawn — the mode and the folder — is remembered for the session,
  and forgotten when the last project closes. An answer for a folder the user has already left is
  dropped rather than drawn.
**Inputs / outputs**: REPOMAP-007's commands, nothing else.
**Edge cases**: a view of the repository just left is never drawn for the one opened. A panel answer
is keyed by its question, so it never shows the previous file's outline for a render.
**Frontend dependency**: this is it.
**Markers**: none

### REPOMAP-010 The map as text a model reads
**Implementation**: `backend/repomap/text.go` · `backend/codemap/mcp.go` (`addMapTools`) · `main.go`
**Behaviour**: four answers in plain text, every line naming a file and — where there is one — a
line, so that what a model cites can be checked:
- **Overview** (`Overview(budget)`): the number of code files and declarations, the languages with
  their file counts, the code files left past the bound, the folders three levels deep with their
  files and declarations (at most 80, each folder right after its parent and the biggest first), and
  the most used declarations as `name — path:line · files using it`, the last section cut where the
  budget runs out — always on a line.
- **Outline** (`OutlineText(path)`): the file's language and lines, its declarations indented under
  their owner with `line-endLine kind name — signature`, then *Imports*, *Imported by* (20 of each
  at most, and how many more) and its packages. A path the map does not hold says why it might not.
- **Declarations** (`DeclarationsText(query)`): REPOMAP-006's search, as `path:line-endLine name —
  signature (used from N files)`.
- **Usages** (`UsagesText(name, path)`): where the name is declared, the files using it with the
  declaration they use it in, `[name only]` on a use no import confirms, and how many files meant a
  different declaration.
They are served to an AI run as the code map's MCP tools `codemap_files`, `codemap_outline`,
`codemap_declarations` and `codemap_name_usages` (CODEMAP-015), and the overview goes, within
12 000 characters, into the brief of a diagram a model draws (ARCHIFY-007).
**Inputs / outputs**: none of its own; the tools take `{}`, `{path}`, `{query}` and `{name, path?}`.
**Edge cases**: the MCP server is another process with its own read-only service (REPOMAP-008): it
reads the app's cache and parses only what changed since, writing nothing. The app writes its cache
before it starts a diagram's run, so that server starts from what the app already knows.
**Frontend dependency**: none.
**Markers**: none

## Deliberately not in this version

- **Languages without a linked grammar** — Kotlin and C# among them — are files without
  declarations. Each grammar is C compiled into every build; these two alone are more source than
  the six linked together.
- **A use proven by the type checker.** A use is confirmed by an import, which is right for a
  module's exports and approximate for a member: `cart.total` and `report.total` are both uses of
  every `total` their file can see. The symbol view (`18-code-map.md`) is the exact one, for Go and
  TypeScript.
- **A file watcher.** Two seconds of staleness at most, and no background work.
