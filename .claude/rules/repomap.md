---
paths:
  - "backend/repomap/**"
  - "THIRD-PARTY-NOTICES.md"
---

# The repository map's index

The specification is `docs/business-rules/19-repo-map.md` (REPOMAP-*). What reading the package
does not tell you:

## Two builds, one package

- tree-sitter lives only in files tagged `//go:build cgo` (`parse_treesitter.go`, its tests, the
  embedded `queries/`). `parse_plain.go` is `!cgo` and imports **neither go-tree-sitter nor a
  grammar** — that is what keeps `GOOS=windows go build ./...` and the cross-platform lint compiling
  on a Mac. A helper both builds need goes in an untagged file.
- Test both: `go test -race ./backend/repomap/` (cgo, the default here) and
  `CGO_ENABLED=0 go test ./backend/repomap/`. `--smoke-test`'s `parser` probe fails on a binary built
  without the grammars — the release must never ship one.

## Changing what is read

- **Bump the revision in `extractorID`** (`treesitter/N`) whenever a query, a `*Members` reader, a
  signature or a line changes what a file yields. The cache is keyed on it; without the bump every
  user keeps the old facts until each file changes.
- The `.scm` files are this repository's own, written from the grammars' node types — not vendored
  `tags.scm`. In `decls.scm` the first pattern listed wins for a node; the capture on the node names
  the `Kind`.
- A grammar is pinned in `go.mod` and named, with its version, in `extractorID` and in
  `THIRD-PARTY-NOTICES.md`; change all three together. go-tree-sitter is pinned at an explicit tag:
  the proxy's `@latest` resolves to an older one. Dart has no tags and is pinned by commit.
- Each worker owns its `Parser`, `QueryCursor` and compiled queries, and closes them; none of them
  is shared between goroutines. Workers start through `safego.Go`, and each file is read inside
  `safego.Do`, so one grammar panicking costs that file, not the map.

## Who writes the cache

- The app's `NewService` writes it; the MCP server's process (`codemap-mcp`) uses
  `NewReadOnlyService` and never does. `Flush` before handing a run that server, so it starts from
  what the app knows.
