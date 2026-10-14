package codemap

import (
	"context"
	"errors"
	"fmt"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Deps is what the code map's commands need.
type Deps struct {
	Service *Service
}

// ErrUnknownSymbol is a flow or an impact asked of a name that is no symbol, or of one that names
// several — the renderer always sends an ID, so either means the map moved under it.
var ErrUnknownSymbol = errors.New("no single symbol has that name") //nolint:staticcheck // ST1005: shown to the user as a sentence

// Register adds the code map's six commands (CODEMAP-007 … CODEMAP-011).
//
// Every one takes the repository's folder as `rootPath`, like the diagram editor's: the map is of a
// folder on this machine, whichever project it belongs to. Registered unconditionally — a start-up
// whose database failed can still map a repository.
func Register(r *bridge.Registry, deps Deps) {
	service := deps.Service

	r.Add("codemap_status", func(ctx context.Context, p bridge.Params) (any, error) {
		_, status, err := graphFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return status, nil
	})

	r.Add("codemap_overview", func(ctx context.Context, p bridge.Params) (any, error) {
		g, status, err := graphFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return g.Overview(status), nil
	})

	r.Add("codemap_search", func(ctx context.Context, p bridge.Params) (any, error) {
		query, err := bridge.Arg[string](p, "query")
		if err != nil {
			return nil, err
		}
		options, err := searchOptions(p)
		if err != nil {
			return nil, err
		}
		g, _, err := graphFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return g.Search(query, options), nil
	})

	r.Add("codemap_symbol", func(ctx context.Context, p bridge.Params) (any, error) {
		name, err := bridge.Arg[string](p, "symbol")
		if err != nil {
			return nil, err
		}
		g, _, err := graphFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return g.Lookup(name), nil
	})

	r.Add("codemap_flow", func(ctx context.Context, p bridge.Params) (any, error) {
		depth, err := bridge.ArgOr(p, "depth", 0)
		if err != nil {
			return nil, err
		}
		dataOnly, err := bridge.ArgOr(p, "dataOnly", false)
		if err != nil {
			return nil, err
		}
		g, n, err := symbolFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return g.Flow(n, FlowOptions{Depth: depth, DataOnly: dataOnly}), nil
	})

	r.Add("codemap_impact", func(ctx context.Context, p bridge.Params) (any, error) {
		depth, err := bridge.ArgOr(p, "depth", 0)
		if err != nil {
			return nil, err
		}
		g, n, err := symbolFor(ctx, service, p)
		if err != nil {
			return nil, err
		}
		return g.Impact(n, depth), nil
	})
}

func graphFor(ctx context.Context, service *Service, p bridge.Params) (*Graph, Status, error) {
	rootPath, err := bridge.Arg[string](p, "rootPath")
	if err != nil {
		return nil, Status{}, err
	}
	return service.Graph(ctx, rootPath)
}

// symbolFor resolves the `symbol` parameter to exactly one node.
func symbolFor(ctx context.Context, service *Service, p bridge.Params) (*Graph, *Node, error) {
	name, err := bridge.Arg[string](p, "symbol")
	if err != nil {
		return nil, nil, err
	}
	g, _, err := graphFor(ctx, service, p)
	if err != nil {
		return nil, nil, err
	}
	found := g.Resolve(name)
	if len(found) != 1 {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownSymbol, name)
	}
	return g, found[0], nil
}

func searchOptions(p bridge.Params) (SearchOptions, error) {
	var options SearchOptions
	kind, err := bridge.ArgOr(p, "kind", "")
	if err != nil {
		return options, err
	}
	if kind != "" {
		parsed, ok := ParseKind(kind)
		if !ok {
			return options, fmt.Errorf("unknown symbol kind %q", kind) //nolint:err113 // names what was sent
		}
		options.Kind = parsed
	}
	lang, err := bridge.ArgOr(p, "lang", "")
	if err != nil {
		return options, err
	}
	for known, name := range langNames {
		if name == lang {
			options.Lang = known
		}
	}
	options.Limit, err = bridge.ArgOr(p, "limit", 0)
	return options, err
}
