# 18 — Code map

A map of the open repository's code: every Go and TypeScript symbol, what calls what, which
interface method dispatches to which implementation, where TypeScript crosses into Go, and which SQL
tables the code reads and writes. From it the renderer asks four questions — *find a symbol*, *what
is around it*, *where does it lead*, *what breaks if it changes* — and gets answers with a file and a
line for every step.

**This document is not a port.** CodeFlow 2.x had no code map, so there is no C# original to match,
no `BUG-*` to preserve and no `AMBIGUOUS-*` to resolve. Every rule below was decided here.

**The rule the whole feature rests on: no relation is guessed.** Go is type-checked with `go/types`;
TypeScript is resolved by the repository's own TypeScript checker. A call lands on the declaration
the checker resolves it to, never on a function that merely shares its name. What only exists at run
time — a table name held in a variable — is recorded as *unresolved* and shown, never filled in.

## Scope

- `backend/codemap/` — the scan, the Go and TypeScript halves, the SQL tables, the links across the
  language boundary, the queries and the six commands
- `backend/codemap/extract.mjs` — the TypeScript extractor, embedded in the binary and run by Node

---

### CODEMAP-001 What the map holds
**Implementation**: `backend/codemap/graph.go`
**Behaviour**: a directed graph. Nodes are Go functions, methods, types, interfaces and interface
methods; TypeScript modules, functions, components, hooks, methods, classes, interfaces and
interface methods; test functions and test files; SQL tables; and endpoints (CODEMAP-004). Edges
are `call`, `ref` (a function used as a value), `render` (JSX), `dispatch` (interface method or
endpoint → implementation), `cross` (TypeScript → Go), and `read` / `write` / `mention` to a table.
**Inputs / outputs**: node IDs are stable across rebuilds — `go:<pkgpath>.<Recv>.<Name>`,
`ts:<relpath>#<Outer.Name>` (a bare `ts:<relpath>` is the file), `sql:<table>`, `ep:<key>`. Paths
are repository-relative with forward slashes.
**Edge cases**: an edge to code that was not indexed — the standard library, a dependency — is
dropped, not kept dangling. An edge is recorded once per (from, to, kind, site).
**Frontend dependency**: `CodemapKind`, `CodemapEdgeKind` in `frontend/src/types/domain.ts`.
**Markers**: none

### CODEMAP-002 One map per repository, rebuilt half by half
**Implementation**: `backend/codemap/index.go` · `backend/codemap/scan.go`
**Behaviour**: the first question about a repository builds its map; later ones are answered from
memory. Each question walks the tree at most once every two seconds — a search box asks on every
keystroke — fingerprinting the Go half (`.go`, `go.mod`, `go.sum`, `.sql`) and the TypeScript half
(`.ts`/`.tsx`/`.mts`/`.cts`, `tsconfig*.json`, `package.json`) by path, size and modification time.
Only the half whose fingerprint moved is rebuilt; the two are then joined again. Four repositories
keep a map at once; the least recently asked about is dropped first.

A build reports its stages on `codemap:progress` — `scanning`, `go`, `typescript`, `linking`,
`ready` — and ends in a `Status`: per language, `mapped`, `partial` (mapped, with notes),
`missing_tool`, `absent` or `failed`, with the file and symbol counts and a detail (the TypeScript
version and which checker API was used).
**Inputs / outputs**: `codemap_status(rootPath)` → `Status`. The status and the progress events
name the repository by `rootPath` **exactly as it was sent**, though the map is built from the
resolved real path: the go command reports files by their real path, and a repository opened through
a symbolic link (macOS's `/var` is one) would otherwise have every file fall outside its own root.
**Edge cases**: a root that is not a folder answers `ErrNotADirectory`. A walk skips version
control, dependencies, build output and any directory starting with a dot. Monorepos: at most twelve
Go modules and twelve tsconfig files, shallowest first.
**Frontend dependency**: `codemapStatus`, `onCodemapProgress`.
**Markers**: none

### CODEMAP-003 The Go half is type-checked from source
**Implementation**: `backend/codemap/golist.go` · `backend/codemap/goload.go`
**Behaviour**: the user's own `go` — found like the AI CLIs (AI-005), plus Go's own install
directories — lists each module's packages, their test variants and their dependencies
(`go list -e -json -deps -test ./...`, through `shared/proc`). The module's packages are then
type-checked one at a time, in dependency order, from source. A dependency's types come from one of
two places:

| mode | when | how |
|---|---|---|
| export data | the user's toolchain is not newer than the one CodeFlow was built with | `go list -export`, read with the standard library's importer |
| source | the user's toolchain is newer, or its export data turns out unreadable | the dependency's declarations type-checked from its source, bodies skipped |

Both map a module identically; which one runs depends only on whether this binary can read the
format the user's toolchain writes. The standard library's reader **panics** on a newer format
(observed: go1.27.1 reading go1.27.2's export data); that panic is caught and turned into the switch
to source mode, and never reaches the caller.
**Inputs / outputs**: none of its own; it feeds CODEMAP-004.
**Edge cases**: a package that does not type-check cleanly is still mapped as far as its types go,
and named in the status notes (the first five, then a count). One at a time on purpose: `go/types`
on Go 1.27 races when two checkers share an imported named type.
**Frontend dependency**: none
**Markers**: none

### CODEMAP-004 What the Go half sees, and where TypeScript crosses into it
**Implementation**: `backend/codemap/golang.go` · `backend/codemap/link.go`
**Behaviour**:
- A call's edge goes to the function the type checker says it calls. A call through an interface
  lands on the interface's method, which `dispatch`es to every method of the module that implements
  it — through the method set, so a method promoted from an embedded field counts.
- A function used as a value is a `ref`. A closure belongs to the function around it, **except** a
  handler registered under a string key: `x.Add("key", func…)` — the shape of HTTP routers, RPC
  registries and command buses — gives the literal a node of its own. Only keys shaped like a route
  (`/api/items/{id}`, `GET /api/items`) or a compound command name (`diagram_list_documents`,
  `repo:fs-changed`) count: a subtest name or a goroutine label beside a function is not a key.
- Wails v3's `application.NewService(x)` marks the exported methods of x's type as bound to
  TypeScript (its lifecycle methods excepted).

TypeScript crosses into Go three ways, and none is matched by a bare method name:

| TypeScript | Go | edge |
|---|---|---|
| a call into Wails bindings — the import path under `bindings/` names the Go package, the imported name or file names the service | the bound method | `cross` |
| an interface of the repository satisfied by a binding as a whole (`const X: XContract = Bound`) | each member → the bound method | `cross` |
| a call whose first argument is a literal key equal to a registered one — `invoke("diagram_list_documents")`, ``fetch(`/api/items/${id}`)`` | an endpoint node, which `dispatch`es to the handler | `cross` |

Routes match segment by segment, a parameter on either side (`{id}`, `:id`, a template hole)
matching any one segment; a route made only of parameters matches nothing, and a string that is not
a path with a named segment is not a request.
**Inputs / outputs**: none of its own.
**Edge cases**: a Wails service whose type cannot be resolved binds nothing.
**Frontend dependency**: none
**Markers**: none

### CODEMAP-005 The TypeScript half is resolved by the repository's own TypeScript
**Implementation**: `backend/codemap/extract.mjs` · `backend/codemap/ts.go`
**Behaviour**: the extractor is embedded in the binary and handed to the user's own `node` on stdin
(`--input-type=module -`): nothing is written to disk to run it. For each tsconfig it loads
`typescript` the way that project would — resolved from the tsconfig's folder — and uses whichever
checker API that version has: the classic compiler API (TypeScript 5 and 6) or the native one
TypeScript 7 exposes as `typescript/unstable/sync`, which is resolved a file at a time in one round
trip. A file belongs to the first project that walks it (`tsconfig.json` and `tsconfig.web.json`
overlap). Generated Wails bindings and `node_modules` are never walked.

Symbols: functions, arrow functions and function expressions assigned to a variable, methods of
classes and object literals, classes, interfaces and their callable members. A PascalCase function
in a `.tsx` file is a component; `use…` is a hook. A variable wrapping a function — `memo`,
`forwardRef`, `useCallback`, `observer`, `debounce`, `throttle`, `once`, `with…` — names that
function; any other call's result is a value, and what it declares (a store's methods) is named
under the variable: `useDiagramStore.save`.

Relations: calls, `new`, JSX tags (a lower-case tag is DOM), and references — including a method
read off an object without being called (`(s) => s.load`, the selector of a store). An object literal
contextually typed as an interface of the repository, and a class that `implements` one, give each
of its methods a `dispatch` from the member it shares a name with.
**Inputs / outputs**: the extractor answers one JSON document (version 1).
**Edge cases**: no `node` → `missing_tool`. A project without `typescript` installed is skipped
with a note naming it, the rest of the map stands.
**Frontend dependency**: none
**Markers**: none

### CODEMAP-006 Tables, and the SQL that touches them
**Implementation**: `backend/codemap/sql.go` · `backend/codemap/link.go`
**Behaviour**: the schema is what the repository's `.sql` files leave behind, replayed in file-name
order: `CREATE`, `ALTER … RENAME TO`, `DROP`. Down-migrations are skipped. A `CREATE TABLE` in a
string of the code — an embedded schema, a table a frontend creates for itself — adds its table too.

A string a function uses is SQL when it names a known table after `FROM`, `JOIN` (read) or
`INSERT/REPLACE INTO`, `UPDATE`, `DELETE FROM` (write). Keywords count in upper case only — prose
says "read from items" — unless the string opens like a statement (`select …`). A string that is
exactly a table name is a `mention`; so is a table named as a call's first argument, read or write
when the callee's name says which (`db.selectFrom("users")`). A table slot holding a placeholder —
`%s`, `?`, `$1`, `${table}`, or the end of the string — makes the site *unresolved*.

ORM models name their table without SQL: bun's `bun:"table:…"` on the embedded `BaseModel` (its
`Model` on a select / insert / update / delete query), and GORM's finishers (`Find`, `First`,
`Create`, `Save`, `Delete` …) on the model they take — the table its `TableName` method returns, or
GORM's convention (snake_case, plural), which counts only when the schema has such a table. A model
handed to a helper as `any` is resolved one call up, at each caller.
**Inputs / outputs**: none of its own.
**Edge cases**: a model whose table is written down counts even when no DDL in the repository
creates it (the schema may live elsewhere); a conventional name never does.
**Frontend dependency**: none
**Markers**: none

### CODEMAP-007 Search
**Implementation**: `backend/codemap/query.go`
**Behaviour**: ranks every node by how its name matches — whole name or last segment exactly, then
a prefix of the last segment, then its camel-hump initials (`CE` → `CreateExpense`), then a
substring, then a subsequence — and, within a rank, functions and methods before types, tables and
files, shorter names first.
**Inputs / outputs**: `codemap_search(rootPath, query, kind?, lang?, limit?)` → `SymbolRef[]`,
thirty by default. An unknown `kind` is refused.
**Edge cases**: a blank query answers `[]`.
**Frontend dependency**: `codemapSearch`.
**Markers**: none

### CODEMAP-008 A symbol and what is around it
**Implementation**: `backend/codemap/query.go`
**Behaviour**: a name resolves by ID, by full or trailing name (`Service.CreateExpense`,
`CreateExpense`), by a TypeScript file path, or by `file:line` (the innermost symbol there). One
match answers its detail; several answer the candidates. The detail lists callers, callees, the
implementations it dispatches to and the hubs it implements, the tables it reaches (directly or
through what it calls, nearest first, with the nearest symbol that touches each), the tests that
reach it, and its unresolved sites. A caller of an interface method or of an endpoint is listed as
a caller of each implementation, *via* that hub. One row per neighbour, at its first site.
**Inputs / outputs**: `codemap_symbol(rootPath, symbol)` → `SymbolLookup`.
**Edge cases**: tests are never listed among the callers — they have their own list, and a test
file calling a method twenty times would otherwise bury the real callers. Each list stops at 200.
**Frontend dependency**: `codemapSymbol`.
**Markers**: none

### CODEMAP-009 A flow
**Implementation**: `backend/codemap/query.go`
**Behaviour**: a tree of everything a symbol leads to — calls, references, renders, dispatches and
hops into Go — ending at the tables. Ten steps deep and four hundred steps wide by default: a
component reaching a table through a store, an IPC wrapper, an endpoint, a handler and a repository
is eight or nine steps, and the budget, not the depth, keeps a wide flow small. A symbol already
expanded is shown again as a *repeat* rather than expanded twice; one cut off by the depth or the
budget is marked *truncated*. Tests are never part of a flow.

`dataOnly` keeps just the branches that reach a table or cross into Go, plus the handler behind an
endpoint — a flow that crosses should show where it lands.
**Inputs / outputs**: `codemap_flow(rootPath, symbol, depth?, dataOnly?)` → `Flow`. A symbol that
does not resolve to exactly one node answers `ErrUnknownSymbol`.
**Edge cases**: none beyond the above.
**Frontend dependency**: `codemapFlow`.
**Markers**: none

### CODEMAP-010 An impact, and the tests to run
**Implementation**: `backend/codemap/query.go`
**Behaviour**: walks upstream from a symbol — callers, renderers, references, and from an
implementation to the hub it serves — six levels by default, listing each level, the entry points
reached (components, endpoints, Wails-bound methods, `main`) and the tests that would notice:
- Go tests grouped by package, each with the command that runs exactly them, from the module that
  holds the package: `go test -run '^(TestA|TestB)$' ./pkg`;
- TypeScript test files grouped by the package whose `vitest` or `jest` runs them, with its command.

A test helper declared in a test file is walked through, so the tests behind it count.
**Inputs / outputs**: `codemap_impact(rootPath, symbol, depth?)` → `Impact`. `ErrUnknownSymbol` as
in CODEMAP-009.
**Edge cases**: four hundred symbols at most; `truncated` says when there were more, or more levels.
**Frontend dependency**: `codemapImpact`.
**Markers**: none

### CODEMAP-011 The overview
**Implementation**: `backend/codemap/query.go`
**Behaviour**: the map at a glance — the thirty folders holding the most symbols, per language; the
Go services Wails binds, with how many methods each has and how many TypeScript symbols call them;
the number of endpoints; every table with its readers and writers; and the fifteen symbols with the
most distinct callers.
**Inputs / outputs**: `codemap_overview(rootPath)` → `Overview`.
**Edge cases**: tests and files are left out of the folders and the hotspots.
**Frontend dependency**: `codemapOverview`.
**Markers**: none

### CODEMAP-012 Registered whether or not the database opened
**Implementation**: `backend/codemap/commands.go` · `backend/app/registry.go`
**Behaviour**: the six commands are registered beside the diagram editor's, outside the storage
stage: the map reads a folder and runs the user's own toolchains, and needs no database and no
credential. Every command takes the repository's folder as `rootPath`.
**Inputs / outputs**: see `01-ipc-surface.md`.
**Edge cases**: none
**Frontend dependency**: `frontend/src/lib/ipc/commands.ts`.
**Markers**: none. These are commands this repository invented, so they are listed in
`newSincePort` in `backend/app/contract_test.go`.

## Deliberately not in this version

- **Languages other than Go and TypeScript.** Each would need a checker of the same grade; a
  pattern-matched language would be the one place the map guesses.
- **Relations only known at run time.** A function stored in a map and called later, reflection,
  dependency injection containers. A function handed over as a value is a `ref`; what calls it later
  is not traced.
- **Endpoints declared by decorators or generated clients** — NestJS's `@Get()`, gRPC, GraphQL. Only
  a string key shared by both sides joins them.
- **A map kept on disk.** A cold build takes seconds; a cache would add invalidation to get them
  back.
