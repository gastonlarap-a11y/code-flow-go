package requirements

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what the requirements command needs.
type Deps struct {
	// Finder is built per call, so a program installed while the app runs is found on the next check.
	Finder func() Finder
}

// Register adds `requirements_status` (REQ-002). It needs no database.
func Register(r *bridge.Registry, deps Deps) {
	r.Add("requirements_status", func(ctx context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.ArgOr(p, "rootPath", "")
		if err != nil {
			return nil, err
		}
		return deps.Finder().Status(ctx, rootPath), nil
	})
}
