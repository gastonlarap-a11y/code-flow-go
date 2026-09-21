package diagram

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Register adds the diagram editor's one command (DIAG-002).
//
// No `Deps` parameter, unlike every other feature: this one needs no database, no credential and no
// emitter, and a struct with no fields would be a promise of injection that never happens. It is
// registered unconditionally in `app.BuildRegistry` for the same reason — a start-up whose database
// failed can still open a folder's diagrams.
func Register(r *bridge.Registry) {
	r.Add("diagram_list_documents", func(ctx context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.Arg[string](p, "rootPath")
		if err != nil {
			return nil, err
		}
		return ListDocuments(ctx, rootPath)
	})
}
