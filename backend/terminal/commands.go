package terminal

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Register adds the four terminal commands.
//
// They take a registry rather than building one, because a terminal outlives the command that
// opened it: the registry belongs to the application's lifetime, and its shutdown is what kills
// the shells.
func Register(r *bridge.Registry, registry *Registry) {
	r.Add("open_terminal", func(ctx context.Context, p bridge.Params) (any, error) {
		cwd, err := bridge.Arg[string](p, "cwd")
		if err != nil {
			return nil, err
		}
		return registry.Open(ctx, cwd)
	})

	r.Add("write_terminal", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		data, err := bridge.Arg[string](p, "data")
		if err != nil {
			return nil, err
		}
		return nil, registry.Write(id, data)
	})

	// The parameters are (cols, rows) and stay in that order all the way down: named on the wire,
	// named at the call site, never positional in between.
	r.Add("resize_terminal", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		cols, err := bridge.Arg[int](p, "cols")
		if err != nil {
			return nil, err
		}
		rows, err := bridge.Arg[int](p, "rows")
		if err != nil {
			return nil, err
		}
		return nil, registry.Resize(id, cols, rows)
	})

	// Closing an id that is not there is a no-op, not an error.
	r.Add("close_terminal", func(_ context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		registry.Close(id)
		return nil, nil
	})
}
