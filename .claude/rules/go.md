---
paths:
  - "backend/**/*.go"
  - "main.go"
---

# Go conventions here

Derived from this repository, not from generic Go advice. Where the two disagree, this wins.

## The wire is the contract

Every struct that crosses to the renderer is a contract with 125 TypeScript files. Four rules, each
of which fails silently when broken:

- **Explicit `json:"snake_case"` tag on every field.** A wrong name compiles and renders `undefined`.
  Exceptions that stay camelCase because 2.x shipped them that way: `HostService`, the secret
  commands, the stream registry, the Azure DTOs, the terminal registry, the skill installer.
- **Never a `nil` slice in a response.** Go marshals it as `null`; the renderer calls `.map` on it
  and the panel crashes — while the same handler with one row works. Build with `make([]T, 0, n)`.
- **Nullable → pointer** (`*string`, `*int64`), and never `omitempty` on a field the renderer types
  as `T | null`.
- **`Invoke` never returns an untyped `nil`.** Wails' transport writes `{}` for one; `jsonwire.Marshal`
  turns it into `null`.

## Errors

- Wrap with context: `fmt.Errorf("doing x: %w", err)`; match with `errors.Is` / `errors.As`, never
  on strings.
- **Except across the bridge.** Sentinel prefixes (`backend/shared/sentinel`) live at position 0 of
  the message and the renderer matches them with `startsWith`. A handler returning one must not wrap
  it, and nothing may be prepended on the way out.
- Translate a typed error into a sentinel **at the command boundary**, never at the throw site.

## Structure

- One package per feature, each exposing `Register(r *bridge.Registry, deps Deps)` with its own
  `Deps` struct. `main.go` is composition only.
- `bridge` knows no feature; features never import Wails (`backend/desktop` is the only one that does).
- Interfaces are declared at the consumer and kept small.
- `ctx context.Context` is the first parameter of anything doing I/O, and is never stored in a struct.
  The context Wails hands a call is cancelled when the call returns — work that outlives it (AI runs,
  streams, terminals, watchers) belongs to a registry with an application-lifetime context.

## Concurrency and processes

- **`safego.Go(name, fn)` is the only way to start a goroutine.** The CI gate fails on a bare `go`.
- Every child process goes through `shared/proc`: its own process group, tree kill, and an
  environment that cannot carry a credential.

## Tests

- Table-driven with named subtests; always run with `-race`.
- No mocking library — hand-written fakes, as in the C# original. `testify` for `require`/`assert` only.
- A test never writes into the real `~/CodeFlow`: pass a `t.TempDir()` through `platform.NewPaths`.
- `panic` belongs only to composition-time programming errors (a duplicate or late command
  registration). Library code returns errors.
