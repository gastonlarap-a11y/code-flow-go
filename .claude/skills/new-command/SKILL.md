---
name: new-command
description: Port one backend command from CodeFlow 2.x to Go — handler, registration, the renderer's wrapper, the contract list, the spec row and the tests. Use whenever a command name moves from "not yet ported" to working, which is the repeating unit of this migration.
---

# Porting one command

234 commands remain. This is the unit of work that repeats, so doing it the same way every time is
what keeps the wire contract intact across all of them.

## Before writing anything

1. **Find the command's owning document** in `docs/business-rules/` (the table in `MIGRATION-GO.md`
   §0.4 maps C# folder → Go package → document). Read its rules.
2. **Read the markers it touches** in `docs/business-rules/90-ambiguities.md` and `91-known-bugs.md`.
   An `AMBIGUOUS-*` is not resolved here; a `BUG-*` is preserved on purpose. Getting this wrong is
   the most expensive mistake available — it looks like an improvement.
3. **Read the renderer's wrapper** in `frontend/src/lib/ipc/commands.ts` (or `apiCommands.ts`). It
   declares the exact parameter names and the exact result type. The renderer is authoritative for
   both; the specification's prose is not always in step with it.
4. **Look for test vectors**: `docs/business-rules/test-vectors/*.vectors.json`. If the command has
   any, they are the test, and they are language-neutral — the Go test consumes them exactly as the
   xUnit test did.

## The handler

In `backend/<feature>/commands.go`:

```go
r.Add("list_branches", func(ctx context.Context, p bridge.Params) (any, error) {
    repoPath, err := bridge.Arg[string](p, "repoPath")   // the renderer's spelling, camelCase
    if err != nil {
        return nil, err
    }
    return listBranches(ctx, repoPath)
})
```

Four things to check every time:

- **Parameter names are the renderer's**, character for character. A typo produces
  `missing required parameter 'repoPath'` at runtime and nothing at build time.
- **The result's struct tags are `snake_case`**, unless the command is in one of the camelCase
  groups (`.claude/rules/go.md` lists them).
- **Slices are built with `make([]T, 0, n)`.** Never `var s []T` — a nil slice marshals as `null`
  and crashes the panel that maps over it.
- **A sentinel-bearing error is returned unwrapped.** Translate the typed error at this boundary,
  never at the throw site.

## Registration

The feature's `Register(r *bridge.Registry, deps Deps)` adds it. If the package is new, wire it into
`app.BuildRegistry` — `main.go` is composition only and never grows a command.

## The contract list

Delete the command's name from its phase block in `backend/app/contract_test.go` (`notYetPorted`).
That test is the port's progress meter: it re-derives all 246 names from the renderer's source on
every run and fails if a name is registered while still listed as pending.

## Tests

- Port the named xUnit class from `docs/verbatim/test-inventory.md`; tick it there.
- Table-driven, named subtests, `-race`.
- Add a wire-shape golden for any new response type, and run it through
  `jsonwire.AssertNoNilSlices`.
- If the command can produce a sentinel, assert it survives at position 0 through `Service.Invoke`.

## The specification

Changed behaviour → update the owning document in the same change. A Go implementation path that
differs from the C# one (the `git` CLI instead of libgit2, RE2 instead of .NET regex) is a
`DIVERGENCE-*` with the next free letter in that document's ledger — never a reused id.

## Finish

`task check`, and report the real result.
