// Package diagram is the process-diagram editor's backend, and it is deliberately one command.
//
// A diagram is a file in the user's folder — `checkout.mmd` beside the code it describes —
// so opening, saving and creating one are the file commands that already exist
// (`read_file_text`, `write_file_text`, `create_file`), the same ones the schema designer uses and
// the same path guards. Drawing, the stencils and the export live in the renderer, which is where
// the canvas is.
//
// That leaves exactly one thing a webview cannot do: walk the folder. Hence no `Deps` struct — the
// package has nothing to inject, and an empty one would only look like something was missing.
//
// The feature is specified in `docs/business-rules/16-diagrams.md` (`DIAG-*`). It is not a port:
// CodeFlow 2.x had no diagram editor, so there is no C# original to match and no `BUG-*` to
// preserve.
package diagram

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/docwalk"
)

// documentSuffix is what a diagram document is called (DIAG-002).
//
// `.mmd` is Mermaid's own extension, and the file really is Mermaid — the point of the format is
// that anything which reads Mermaid can read these, a model included. It was `.diagram.json` while
// the editor wrote a private format of its own.
//
// Matched as a suffix rather than an extension because `docwalk` takes one either way, and a
// multi-dot name would need it. `filepath.Ext` would do here; the shared walk does not care.
const documentSuffix = ".mmd"

// ListDocuments walks a folder for diagram documents (DIAG-002).
//
// Project-relative, sorted, `/`-separated on every platform, bounded by `docwalk`'s limits, with
// the directories nobody keeps documents in pruned rather than filtered.
func ListDocuments(ctx context.Context, rootPath string) ([]string, error) {
	return docwalk.List(ctx, rootPath, documentSuffix)
}
