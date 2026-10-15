package codemap

import "github.com/gastonlarap-a11y/code-flow/backend/archify"

// The user's Archify, as the map's two diagrams use it (CODEMAP-017). Finding, rendering and the
// repository pin live in package archify; what is here adapts the map's own types to it.

// ErrNoArchify means no supported Archify install was found; the diagram editor's Mermaid export
// does not need one.
var ErrNoArchify = archify.ErrNotInstalled

// runtimeOf is the part of the discovered tools an Archify run uses.
func runtimeOf(tools Tools) archify.Runtime {
	return archify.Runtime{Node: tools.Node, Path: tools.Path}
}

// cite answers up to three citations of symbols, skipping files not committed as they are.
func cite(repo archify.Repo, symbols []SymbolRef) []archify.Source {
	refs := make([]archify.Ref, 0, len(symbols))
	for _, s := range symbols {
		refs = append(refs, archify.Ref{Path: s.File, Line: s.Line, EndLine: s.EndLine})
	}
	return repo.Cite(refs)
}
